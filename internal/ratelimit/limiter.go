package ratelimit

import (
	"container/list"
	"context"
	"crypto/sha256"
	"sync"
	"time"

	"jamieslibrary/internal/clock"
)

// Counter is one of the limits: each counts separately per key.
type Counter int

const (
	// SignIn counts failed sign-ins, and wrong current passwords on the change-password route.
	SignIn Counter = iota
	// Registration counts failed registrations and first-run setup attempts,
	// which share a counter. A valid invitation or setup code must only Forgive
	// its attempt, never Succeed: a reset would give whoever holds one valid
	// code 9 more guesses each time they use it.
	Registration
	numCounters
)

const (
	// MaxKeys bounds the keys held; the oldest unblocked are evicted first.
	MaxKeys = 50_000
	// BlockFor is how long a key stays blocked from the failure that reached the limit.
	BlockFor = 15 * time.Minute
	// IdleReset is how long a key's failures count before they are forgotten.
	IdleReset = 15 * time.Minute
	// SweepEvery is how often Run drops forgotten keys.
	SweepEvery = time.Minute
)

var maxFailures = [numCounters]int{SignIn: 5, Registration: 10}

type counter struct {
	fails        int
	last         time.Time // the latest attempt counted
	blockedUntil time.Time
	// who is the SHA-256 of the first account name a failure since the last
	// reset was attributed to, and mixed says a different one has been seen
	// (or an attempt named none). The name itself is never kept.
	who   [sha256.Size]byte
	named bool
	mixed bool
}

// nameHash folds a typed username as SQLite NOCASE does (ASCII lower case) and hashes it.
func nameHash(name string) [sha256.Size]byte {
	b := []byte(name)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return sha256.Sum256(b)
}

func (ct *counter) attribute(name string) {
	if name == "" {
		if ct.named {
			ct.mixed = true
		}
		return
	}
	h := nameHash(name)
	switch {
	case !ct.named:
		ct.who, ct.named = h, true
	case ct.who != h:
		ct.mixed = true
	}
}

type entry struct {
	key      string
	c        [numCounters]counter
	touched  time.Time
	position *list.Element
}

func (e *entry) blocked(now time.Time) bool {
	for i := range e.c {
		if now.Before(e.c[i].blockedUntil) {
			return true
		}
	}
	return false
}

// Limiter counts attempts per key, in memory: a restart clears it. It is safe
// for concurrent use.
type Limiter struct {
	mu      sync.Mutex
	max     int
	entries map[string]*entry
	order   *list.List // front is the most recently touched
}

// New returns an empty Limiter holding at most MaxKeys keys.
func New() *Limiter { return newWithMax(MaxKeys) }

func newWithMax(n int) *Limiter {
	return &Limiter{max: n, entries: map[string]*entry{}, order: list.New()}
}

// Allow says whether the key may make an attempt now. If it may, the attempt is
// counted as a failure straight away, so attempts that run at the same time
// can't slip past the limit; the caller calls Succeed or Forgive once it knows
// the attempt wasn't a failure. If it may not, retryAfter is how long the block
// has left, and nothing is counted. name is the account the attempt is for, as
// typed (a sign-in's username, or the caller's own for a password change), or
// "" where there is none: it decides when Succeed may reset the count. The attempt that reaches the limit is let
// through and starts the block. An empty key, an address that couldn't be read,
// is never limited.
func (l *Limiter) Allow(c Counter, key, name string) (ok bool, retryAfter time.Duration) {
	if key == "" {
		return true, 0
	}
	now := clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	if e == nil {
		if e = l.insert(key, now); e == nil {
			return true, 0 // every slot holds a blocked key: this one goes untracked
		}
	} else {
		l.order.MoveToFront(e.position)
		e.touched = now
	}
	ct := &e.c[c]
	if now.Before(ct.blockedUntil) {
		return false, ct.blockedUntil.Sub(now)
	}
	if !ct.blockedUntil.IsZero() || now.Sub(ct.last) >= IdleReset {
		*ct = counter{} // the block is over, or the old failures are forgotten
	}
	ct.fails++
	ct.last = now
	ct.attribute(name)
	if ct.fails >= maxFailures[c] {
		ct.blockedUntil = now.Add(BlockFor)
	}
	return true, 0
}

// Succeed is for an attempt that was right, for the account name that Allow
// was given. It clears the key's count only when every failure counted since
// the last reset named that account; otherwise anyone with an account of their
// own could reset the count by signing in as themselves between guesses at
// someone else's. When it doesn't clear, it takes back the attempt's own count,
// as Forgive does.
func (l *Limiter) Succeed(c Counter, key, name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	if e == nil {
		return
	}
	ct := &e.c[c]
	if ct.named && !ct.mixed && ct.who == nameHash(name) {
		*ct = counter{}
		return
	}
	l.forgive(ct, c)
}

// Forgive takes back the failure Allow counted, for an attempt that turned out
// not to be one. A block lifts if that brings the count under the limit.
func (l *Limiter) Forgive(c Counter, key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	if e == nil {
		return
	}
	l.forgive(&e.c[c], c)
}

func (l *Limiter) forgive(ct *counter, c Counter) {
	if ct.fails > 0 {
		ct.fails--
		if ct.fails < maxFailures[c] {
			ct.blockedUntil = time.Time{}
		}
	}
}

// insert adds key, evicting the oldest unblocked key if the map is full. It
// returns nil when every key is blocked.
func (l *Limiter) insert(key string, now time.Time) *entry {
	if len(l.entries) >= l.max {
		victim := l.order.Back()
		for victim != nil && victim.Value.(*entry).blocked(now) {
			victim = victim.Prev()
		}
		if victim == nil {
			return nil
		}
		l.remove(victim.Value.(*entry))
	}
	e := &entry{key: key, touched: now}
	e.position = l.order.PushFront(e)
	l.entries[key] = e
	return e
}

func (l *Limiter) remove(e *entry) {
	l.order.Remove(e.position)
	delete(l.entries, e.key)
}

// Sweep drops every key whose blocks are over and whose failures are old enough to forget.
func (l *Limiter) Sweep() {
	now := clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for el := l.order.Back(); el != nil; {
		e := el.Value.(*entry)
		prev := el.Prev()
		if !e.blocked(now) && now.Sub(e.touched) >= IdleReset {
			l.remove(e)
		}
		el = prev
	}
}

// Len is the number of keys held.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// Run sweeps every minute until ctx ends.
func (l *Limiter) Run(ctx context.Context) {
	t := time.NewTicker(SweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.Sweep()
		}
	}
}

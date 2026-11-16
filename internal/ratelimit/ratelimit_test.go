package ratelimit

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"jamieslibrary/internal/clock"
)

type fakeClock struct{ now time.Time }

func useClock(t *testing.T) *fakeClock {
	f := &fakeClock{now: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	t.Cleanup(clock.SetSource(func() time.Time { return f.now }))
	return f
}

func fail(t *testing.T, l *Limiter, c Counter, key string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if ok, _ := l.Allow(c, key, ""); !ok {
			t.Fatalf("attempt %d of %s was refused early", i+1, key)
		}
	}
}

// SEC-T1 "Guessing invitation codes": 10 failures block registration for 15 minutes, for that address only.
func TestGuessingInvitationCodes(t *testing.T) {
	f := useClock(t)
	l := New()
	fail(t, l, Registration, "198.51.100.7", 10)
	ok, retry := l.Allow(Registration, "198.51.100.7", "")
	if ok || retry != BlockFor {
		t.Fatalf("11th attempt: ok=%v retry=%v", ok, retry)
	}
	if ok, _ := l.Allow(Registration, "203.0.113.9", ""); !ok {
		t.Fatal("another address was blocked")
	}
	if ok, _ := l.Allow(SignIn, "198.51.100.7", ""); !ok {
		t.Fatal("registration failures blocked sign-in")
	}
	f.now = f.now.Add(BlockFor - time.Second)
	if ok, _ := l.Allow(Registration, "198.51.100.7", ""); ok {
		t.Fatal("block ended early")
	}
	f.now = f.now.Add(time.Second)
	if ok, _ := l.Allow(Registration, "198.51.100.7", ""); !ok {
		t.Fatal("block did not end after 15 minutes")
	}
}

func TestSignInBlocksOnTheFifthFailureFromThatMoment(t *testing.T) {
	f := useClock(t)
	l := New()
	fail(t, l, SignIn, "k", 4)
	f.now = f.now.Add(10 * time.Minute)
	fail(t, l, SignIn, "k", 1) // the 5th, 10 minutes after the first
	f.now = f.now.Add(14 * time.Minute)
	if ok, _ := l.Allow(SignIn, "k", ""); ok {
		t.Fatal("blocked for less than 15 minutes from the 5th failure")
	}
	f.now = f.now.Add(2 * time.Minute)
	if ok, _ := l.Allow(SignIn, "k", ""); !ok {
		t.Fatal("still blocked after 15 minutes")
	}
}

func TestBlockedAttemptsAreNotCountedAndDoNotExtendTheBlock(t *testing.T) {
	f := useClock(t)
	l := New()
	fail(t, l, SignIn, "k", 5)
	f.now = f.now.Add(10 * time.Minute)
	for i := 0; i < 20; i++ {
		l.Allow(SignIn, "k", "")
	}
	f.now = f.now.Add(5*time.Minute + time.Second)
	if ok, _ := l.Allow(SignIn, "k", ""); !ok {
		t.Fatal("refused attempts extended the block")
	}
}

func failAs(t *testing.T, l *Limiter, c Counter, key, name string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if ok, _ := l.Allow(c, key, name); !ok {
			t.Fatalf("attempt %d of %s was refused early", i+1, name)
		}
	}
}

func TestSucceedAndForgive(t *testing.T) {
	useClock(t)
	l := New()
	failAs(t, l, SignIn, "k", "Ann", 3)
	l.Allow(SignIn, "k", "ann") // the right attempt: same account, folded to lower case
	l.Succeed(SignIn, "k", "ann")
	fail(t, l, SignIn, "k", 4) // 4 again, not 8
	l.Forgive(SignIn, "k")     // one of them wasn't a failure: 3 left
	fail(t, l, SignIn, "k", 2)
	if ok, _ := l.Allow(SignIn, "k", ""); ok {
		t.Fatal("expected 5 failures to block")
	}
	l.Forgive(SignIn, "k") // an unknown key is fine
	l.Forgive(SignIn, "nobody")
	l.Succeed(SignIn, "nobody", "ann")
}

// Signing in as yourself doesn't clear failures that were guesses at someone else.
func TestMalloryCannotSwitchTheLimitOff(t *testing.T) {
	useClock(t)
	l := New()
	failAs(t, l, SignIn, "k", "admin", 4)
	l.Allow(SignIn, "k", "mallory")
	l.Succeed(SignIn, "k", "mallory") // her success takes back its own count and nothing more
	failAs(t, l, SignIn, "k", "admin", 1)
	if ok, _ := l.Allow(SignIn, "k", "admin"); ok {
		t.Fatal("the 5th guess at admin did not start the block")
	}
}

func TestOnlyTheNameIsHashedNotKept(t *testing.T) {
	useClock(t)
	l := New()
	l.Allow(SignIn, "k", strings.Repeat("x", 60_000))
	if nameHash("ADMIN") != nameHash("admin") || nameHash("admin") == nameHash("admin2") || nameHash("É") == nameHash("é") {
		t.Fatal("names fold as NOCASE does: ASCII only")
	}
}

// A valid code only takes back its own attempt: 9 failures, a valid one, and one more failure blocks.
func TestValidRegistrationCodeNeverResetsTheCount(t *testing.T) {
	useClock(t)
	l := New()
	fail(t, l, Registration, "k", 9)
	if ok, _ := l.Allow(Registration, "k", ""); !ok {
		t.Fatal("the valid attempt was refused")
	}
	l.Forgive(Registration, "k")
	fail(t, l, Registration, "k", 1) // the 10th failure
	if ok, _ := l.Allow(Registration, "k", ""); ok {
		t.Fatal("a valid code reset the registration count")
	}
}

func TestOldFailuresAreForgotten(t *testing.T) {
	f := useClock(t)
	l := New()
	fail(t, l, SignIn, "k", 4)
	f.now = f.now.Add(IdleReset)
	fail(t, l, SignIn, "k", 4)
}

func TestAnUnreadableAddressIsNeverLimited(t *testing.T) {
	useClock(t)
	l := New()
	fail(t, l, SignIn, "", 100)
}

func TestConcurrentAttemptsCannotSlipPastTheLimit(t *testing.T) {
	useClock(t)
	l := New()
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := l.Allow(SignIn, "k", ""); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 5 {
		t.Fatalf("%d concurrent attempts got through, want 5", allowed)
	}
}

// The bound: 60,000 distinct addresses keep the map at its cap.
func TestMapStaysAtItsCap(t *testing.T) {
	useClock(t)
	l := New()
	for i := 0; i < 60_000; i++ {
		l.Allow(SignIn, fmt.Sprintf("10.%d.%d.%d", i>>16, (i>>8)&255, i&255), "")
		if l.Len() > MaxKeys {
			t.Fatalf("%d keys after %d addresses", l.Len(), i+1)
		}
	}
	if l.Len() != MaxKeys {
		t.Fatalf("%d keys, want the cap %d", l.Len(), MaxKeys)
	}
}

func TestEvictionKeepsBlockedKeysAndDropsTheOldestOthers(t *testing.T) {
	f := useClock(t)
	l := newWithMax(3)
	fail(t, l, SignIn, "blocked", 5)
	f.now = f.now.Add(time.Second)
	l.Allow(SignIn, "old", "")
	f.now = f.now.Add(time.Second)
	l.Allow(SignIn, "newer", "")
	l.Allow(SignIn, "newest", "") // full: "blocked" is oldest but blocked, so "old" goes
	if _, there := l.entries["old"]; there {
		t.Fatal("the oldest unblocked key was kept")
	}
	if _, there := l.entries["blocked"]; !there {
		t.Fatal("a blocked key was evicted")
	}
	if ok, _ := l.Allow(SignIn, "blocked", ""); ok {
		t.Fatal("eviction lifted a block")
	}
}

func TestWhenEveryKeyIsBlockedANewOneIsUntrackedNotEvicting(t *testing.T) {
	useClock(t)
	l := newWithMax(2)
	fail(t, l, SignIn, "a", 5)
	fail(t, l, SignIn, "b", 5)
	fail(t, l, SignIn, "c", 100)
	if l.Len() != 2 {
		t.Fatalf("%d keys", l.Len())
	}
	for _, k := range []string{"a", "b"} {
		if ok, _ := l.Allow(SignIn, k, ""); ok {
			t.Fatalf("%s's block was lost", k)
		}
	}
}

func TestSweepDropsOnlyWhatIsForgotten(t *testing.T) {
	f := useClock(t)
	l := New()
	fail(t, l, SignIn, "blocked", 5)
	fail(t, l, SignIn, "idle", 2)
	f.now = f.now.Add(IdleReset - time.Second)
	l.Allow(SignIn, "recent", "")
	l.Sweep()
	if l.Len() != 3 {
		t.Fatalf("swept too early: %d keys", l.Len())
	}
	f.now = f.now.Add(2 * time.Second) // blocked's block ends at +15m too; idle and blocked are now old
	l.Sweep()
	if l.Len() != 1 {
		t.Fatalf("%d keys after the sweep, want just the recent one", l.Len())
	}
}

func req(remote string, xff ...string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = remote
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

func trusted(t *testing.T, list string) *Clients {
	t.Helper()
	p, err := ParseTrusted(list)
	if err != nil {
		t.Fatal(err)
	}
	return NewClients(p)
}

func TestClientAddress(t *testing.T) {
	proxy := trusted(t, "10.0.0.0/8, 192.168.1.5")
	cases := []struct {
		name string
		c    *Clients
		r    *http.Request
		want string
	}{
		{"direct", NewClients(nil), req("198.51.100.7:1234"), "198.51.100.7"},
		{"no proxy trusted: header ignored", NewClients(nil), req("198.51.100.7:1234", "1.2.3.4"), "198.51.100.7"},
		{"untrusted peer: header ignored", proxy, req("198.51.100.7:1234", "1.2.3.4"), "198.51.100.7"},
		{"trusted peer", proxy, req("10.0.0.1:1", "203.0.113.5"), "203.0.113.5"},
		{"right-most untrusted", proxy, req("10.0.0.1:1", "9.9.9.9, 203.0.113.5, 10.2.2.2"), "203.0.113.5"},
		{"several header lines", proxy, req("10.0.0.1:1", "9.9.9.9", "203.0.113.5"), "203.0.113.5"},
		{"single-host entry", proxy, req("192.168.1.5:1", "203.0.113.5"), "203.0.113.5"},
		{"no header", proxy, req("10.0.0.1:1"), "10.0.0.1"},
		{"unparseable entry", proxy, req("10.0.0.1:1", "203.0.113.5, garbage"), "10.0.0.1"},
		{"all trusted", proxy, req("10.0.0.1:1", "10.1.1.1, 10.2.2.2"), "10.0.0.1"},
		{"entry with a port", proxy, req("10.0.0.1:1", "203.0.113.5:4444"), "203.0.113.5"},
		{"IPv4 as IPv6", proxy, req("[::ffff:10.0.0.1]:1", "::ffff:203.0.113.5"), "203.0.113.5"},
		{"IPv6 by /64", NewClients(nil), req("[2001:db8:1:2:aaaa:bbbb:cccc:dddd]:1"), "2001:db8:1:2::/64"},
		{"IPv6 same /64", NewClients(nil), req("[2001:db8:1:2:1::9]:1"), "2001:db8:1:2::/64"},
		{"IPv6 other /64", NewClients(nil), req("[2001:db8:1:3::1]:1"), "2001:db8:1:3::/64"},
		{"IPv6 behind proxy", proxy, req("10.0.0.1:1", "2001:db8:1:2:aaaa::1"), "2001:db8:1:2::/64"},
		{"Forwarded and X-Real-IP ignored", proxy, func() *http.Request {
			r := req("10.0.0.1:1")
			r.Header.Set("Forwarded", "for=1.2.3.4")
			r.Header.Set("X-Real-IP", "5.6.7.8")
			return r
		}(), "10.0.0.1"},
		{"unreadable peer", NewClients(nil), req("not an address"), ""},
	}
	for _, tc := range cases {
		if got := tc.c.Key(tc.r); got != tc.want {
			t.Errorf("%s: key %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestParseTrusted(t *testing.T) {
	p, err := ParseTrusted("")
	if err != nil || len(p) != 0 {
		t.Fatalf("empty: %v %v", p, err)
	}
	p, err = ParseTrusted("10.1.2.3/8 fd00::/8,172.16.0.9")
	want := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("fd00::/8"), netip.MustParsePrefix("172.16.0.9/32")}
	if err != nil || fmt.Sprint(p) != fmt.Sprint(want) {
		t.Fatalf("%v %v", p, err)
	}
	for _, bad := range []string{"nope", "10.0.0.0/33", "10.0.0.0/8, banana", "fe80::1%eth0", "::ffff:0.0.0.0/8", "0.0.0.0/0", "::/0", "10.0.0.0/8, 0.0.0.0/0", "::ffff:0.0.0.0/96"} {
		if _, err := ParseTrusted(bad); err == nil || !strings.Contains(err.Error(), "JL_TRUSTED_PROXIES") {
			t.Errorf("%q: error %v", bad, err)
		}
	}
}

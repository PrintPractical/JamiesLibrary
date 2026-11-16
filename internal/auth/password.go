package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"jamieslibrary/internal/apperr"
)

// Password length, counted in Unicode code points (security design §2).
const (
	MinPasswordLength = 10
	MaxPasswordLength = 256
)

// The argon2id parameters new hashes use: the OWASP minimum (security design §2).
const (
	argonMemoryKiB = 19456
	argonTime      = 2
	argonThreads   = 1
	saltLength     = 16
	keyLength      = 32
)

// Bounds on what a stored hash may ask of the server when it is verified, so
// a damaged or planted row can't make one sign-in use gigabytes.
const (
	maxStoredMemoryKiB = 256 * 1024
	maxStoredTime      = 10
	maxStoredThreads   = 8
	minStoredSalt      = 8
	maxStoredKey       = 64
	minStoredKey       = 16
)

// Hashing concurrency: at most this many argon2id runs at once, and a request
// waits this long for its turn (security design §12).
const (
	MaxConcurrentHashes = 2
	HashWait            = 10 * time.Second
)

// CheckPassword is the one rule for a new password: 10 to 256 code points,
// nothing else. It runs at registration, own change, an administrator's reset
// and recovery. field names the input in the error.
func CheckPassword(field, password string) error {
	if !utf8.ValidString(password) {
		return apperr.OnField(apperr.Invalid, field, "The password isn't valid text.")
	}
	n := utf8.RuneCountInString(password)
	switch {
	case n < MinPasswordLength:
		return apperr.OnField(apperr.Invalid, field,
			fmt.Sprintf("A password must be at least %d characters.", MinPasswordLength))
	case n > MaxPasswordLength:
		return apperr.OnField(apperr.Invalid, field,
			fmt.Sprintf("A password can be at most %d characters.", MaxPasswordLength))
	}
	return nil
}

// Passwords hashes and verifies passwords with argon2id, never more than
// MaxConcurrentHashes at once.
type Passwords struct {
	slots chan struct{}
	wait  time.Duration
}

// NewPasswords makes a Passwords with the standard limits.
func NewPasswords() *Passwords {
	return &Passwords{slots: make(chan struct{}, MaxConcurrentHashes), wait: HashWait}
}

var errBusy = apperr.New(apperr.Unavailable, "The server is busy: try again in a moment.")

// acquire waits for a hashing slot, for at most p.wait, and gives it back with the returned function.
func (p *Passwords) acquire(ctx context.Context) (func(), error) {
	select {
	case p.slots <- struct{}{}:
		return func() { <-p.slots }, nil
	default:
	}
	t := time.NewTimer(p.wait)
	defer t.Stop()
	select {
	case p.slots <- struct{}{}:
		return func() { <-p.slots }, nil
	case <-t.C:
		return nil, errBusy
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Hash checks the password against CheckPassword and returns its PHC-form hash.
func (p *Passwords) Hash(ctx context.Context, field, password string) (string, error) {
	if err := CheckPassword(field, password); err != nil {
		return "", err
	}
	return p.hash(ctx, password)
}

func (p *Passwords) hash(ctx context.Context, password string) (string, error) {
	release, err := p.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, keyLength)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version,
		argonMemoryKiB, argonTime, argonThreads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

type phc struct {
	memory, time uint32
	threads      uint8
	salt, key    []byte
}

// parseHash reads a PHC argon2id string. An empty, foreign or out-of-bounds
// hash is an error, which sign-in treats as a wrong password.
func parseHash(s string) (phc, error) {
	var h phc
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return h, fmt.Errorf("not an argon2id hash")
	}
	if parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return h, fmt.Errorf("unsupported argon2 version")
	}
	var m, t, th uint64
	var err error
	params := strings.Split(parts[3], ",")
	if len(params) != 3 || !strings.HasPrefix(params[0], "m=") || !strings.HasPrefix(params[1], "t=") || !strings.HasPrefix(params[2], "p=") {
		return h, fmt.Errorf("bad parameters")
	}
	if m, err = strconv.ParseUint(params[0][2:], 10, 32); err != nil {
		return h, err
	}
	if t, err = strconv.ParseUint(params[1][2:], 10, 32); err != nil {
		return h, err
	}
	if th, err = strconv.ParseUint(params[2][2:], 10, 8); err != nil {
		return h, err
	}
	if m < 8 || m > maxStoredMemoryKiB || t < 1 || t > maxStoredTime || th < 1 || th > maxStoredThreads {
		return h, fmt.Errorf("parameters out of bounds")
	}
	enc := base64.RawStdEncoding
	if h.salt, err = enc.DecodeString(parts[4]); err != nil || len(h.salt) < minStoredSalt {
		return h, fmt.Errorf("bad salt")
	}
	if h.key, err = enc.DecodeString(parts[5]); err != nil || len(h.key) < minStoredKey || len(h.key) > maxStoredKey {
		return h, fmt.Errorf("bad key")
	}
	h.memory, h.time, h.threads = uint32(m), uint32(t), uint8(th)
	return h, nil
}

// needsRehash is whether a stored hash was made with other parameters than today's.
func (h phc) needsRehash() bool {
	return h.memory != argonMemoryKiB || h.time != argonTime || h.threads != argonThreads ||
		len(h.salt) != saltLength || len(h.key) != keyLength
}

// Verify reports whether password matches the stored hash, and whether the
// hash should be replaced by one with today's parameters. An empty or
// unparseable hash never matches: it is checked against the dummy hash so the
// time taken is the same (a row made with no password must not let anyone in).
func (p *Passwords) Verify(ctx context.Context, password, stored string) (match, rehash bool, err error) {
	h, perr := parseHash(stored)
	if perr != nil {
		return false, false, p.VerifyDummy(ctx, password)
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return false, false, err
	}
	defer release()
	got := argon2.IDKey([]byte(password), h.salt, h.time, h.memory, h.threads, uint32(len(h.key)))
	if subtle.ConstantTimeCompare(got, h.key) != 1 {
		return false, false, nil
	}
	return true, h.needsRehash(), nil
}

// dummyHash is a hash of a password nobody has, made with today's parameters.
// Checking a sign-in against it costs what a real check costs.
const dummyHash = "$argon2id$v=19$m=19456,t=2,p=1$Hn/l/7f1GbuiUnvBCp8jjw$z7aabKkx3PSnIrOzDSZ7Duiol5JkjhHjF1zaA4utNNg"

// VerifyDummy does the work of a verification against a fixed hash and says
// nothing matches, so a sign-in for an unknown name takes as long as a real one.
func (p *Passwords) VerifyDummy(ctx context.Context, password string) error {
	h, err := parseHash(dummyHash)
	if err != nil {
		return err
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	_ = argon2.IDKey([]byte(password), h.salt, h.time, h.memory, h.threads, uint32(len(h.key)))
	return nil
}

// TemporaryPasswordLength is the length of a password made for someone else to
// sign in with once (recovery, an administrator's reset).
const TemporaryPasswordLength = 16

// tempAlphabet leaves out the characters that are easy to misread aloud or on
// paper: 0 O 1 l I.
const tempAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// TemporaryPassword draws a password from crypto/rand. It passes CheckPassword.
func TemporaryPassword() (string, error) {
	max := big.NewInt(int64(len(tempAlphabet)))
	out := make([]byte, TemporaryPasswordLength)
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("drawing a temporary password: %w", err)
		}
		out[i] = tempAlphabet[n.Int64()]
	}
	return string(out), nil
}

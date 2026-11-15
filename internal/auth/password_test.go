package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"

	"jamieslibrary/internal/apperr"
)

func wantInvalid(t *testing.T, err error, field string) *apperr.Error {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Code != apperr.Invalid || e.Field != field {
		t.Fatalf("error = %v, want an invalid error on %q", err, field)
	}
	return e
}

// ACCESS-6 "Too short": a password of fewer than 10 characters is refused and the message says 10.
func TestCheckPasswordTooShortSaysTheMinimum(t *testing.T) {
	e := wantInvalid(t, CheckPassword("new_password", "short1"), "new_password")
	if !strings.Contains(e.Message, "10") {
		t.Fatalf("message %q does not say the minimum", e.Message)
	}
}

func TestCheckPasswordCountsCodePoints(t *testing.T) {
	cases := []struct {
		name string
		pw   string
		ok   bool
	}{
		{"9 ascii", strings.Repeat("a", 9), false},
		{"10 ascii", strings.Repeat("a", 10), true},
		{"9 emoji (36 bytes)", strings.Repeat("😀", 9), false},
		{"10 emoji (40 bytes)", strings.Repeat("😀", 10), true},
		{"256", strings.Repeat("é", 256), true},
		{"257", strings.Repeat("é", 257), false},
		{"invalid utf-8", "abcdefghij\xff", false},
		{"empty", "", false},
		{"spaces only is allowed: no composition rules", strings.Repeat(" ", 12), true},
	}
	for _, c := range cases {
		if err := CheckPassword("password", c.pw); (err == nil) != c.ok {
			t.Errorf("%s: err = %v, ok want %v", c.name, err, c.ok)
		}
	}
}

func TestHashIsPHCArgon2idWithTheDesignParameters(t *testing.T) {
	p := NewPasswords()
	h, err := p.Hash(context.Background(), "password", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash %q", h)
	}
	parsed, err := parseHash(h)
	if err != nil || len(parsed.salt) != 16 || len(parsed.key) != 32 || parsed.needsRehash() {
		t.Fatalf("parsed %+v err %v", parsed, err)
	}
	if strings.Contains(h, "correct horse") {
		t.Fatal("the hash holds the password")
	}
	again, _ := p.Hash(context.Background(), "password", "correct horse battery")
	if again == h {
		t.Fatal("two hashes of one password are equal: the salt is not random")
	}
}

func TestHashRefusesAPasswordTheRuleRefuses(t *testing.T) {
	if _, err := NewPasswords().Hash(context.Background(), "password", "short"); err == nil {
		t.Fatal("hashed a short password")
	}
}

func TestVerify(t *testing.T) {
	ctx := context.Background()
	p := NewPasswords()
	h, _ := p.Hash(ctx, "password", "correct horse battery")
	if ok, rehash, err := p.Verify(ctx, "correct horse battery", h); !ok || rehash || err != nil {
		t.Fatalf("right password: %v %v %v", ok, rehash, err)
	}
	if ok, _, err := p.Verify(ctx, "correct horse batterY", h); ok || err != nil {
		t.Fatalf("wrong password: %v %v", ok, err)
	}
}

// hashWith makes a hash with chosen parameters, as an older version of the program would have.
func hashWith(pw string, m, t uint32, p uint8) string {
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte(pw), salt, t, m, p, 32)
	e := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", m, t, p, e.EncodeToString(salt), e.EncodeToString(key))
}

func TestVerifyReportsAHashWithOtherParametersForRehash(t *testing.T) {
	ctx := context.Background()
	old := hashWith("correct horse battery", 8192, 1, 1)
	ok, rehash, err := NewPasswords().Verify(ctx, "correct horse battery", old)
	if !ok || !rehash || err != nil {
		t.Fatalf("got %v %v %v, want a match that wants a rehash", ok, rehash, err)
	}
	if ok, rehash, _ := NewPasswords().Verify(ctx, "nope nope nope", old); ok || rehash {
		t.Fatal("a wrong password matched or asked for a rehash")
	}
}

// An empty or damaged stored hash is a wrong password, never a match.
func TestVerifyNeverMatchesAnEmptyOrDamagedHash(t *testing.T) {
	ctx := context.Background()
	p := NewPasswords()
	good, _ := p.Hash(ctx, "password", "correct horse battery")
	for name, stored := range map[string]string{
		"empty":         "",
		"x":             "x",
		"plain text":    "correct horse battery",
		"bcrypt":        "$2a$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ012345",
		"truncated":     good[:len(good)-10],
		"no key":        strings.Join(strings.Split(good, "$")[:5], "$") + "$",
		"huge memory":   "$argon2id$v=19$m=4294967295,t=2,p=1$MDEyMzQ1Njc4OWFiY2RlZg$" + strings.Split(good, "$")[5],
		"huge time":     "$argon2id$v=19$m=19456,t=4000000000,p=1$MDEyMzQ1Njc4OWFiY2RlZg$" + strings.Split(good, "$")[5],
		"zero threads":  "$argon2id$v=19$m=19456,t=2,p=0$MDEyMzQ1Njc4OWFiY2RlZg$" + strings.Split(good, "$")[5],
		"other version": strings.Replace(good, "v=19", "v=16", 1),
		"argon2i":       strings.Replace(good, "argon2id", "argon2i", 1),
	} {
		for _, pw := range []string{"", "correct horse battery", stored} {
			ok, _, err := p.Verify(ctx, pw, stored)
			if ok || err != nil {
				t.Errorf("%s with %q: match %v, err %v", name, pw, ok, err)
			}
		}
	}
}

func TestHashingWaitsForASlotThenSaysBusy(t *testing.T) {
	p := NewPasswords()
	p.wait = 50 * time.Millisecond
	for i := 0; i < MaxConcurrentHashes; i++ {
		p.slots <- struct{}{}
	}
	start := time.Now()
	_, err := p.Hash(context.Background(), "password", "correct horse battery")
	e, ok := apperr.As(err)
	if !ok || e.Code != apperr.Unavailable || !strings.Contains(e.Message, "busy") {
		t.Fatalf("err = %v, want 503 busy", err)
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Fatal("gave up before waiting")
	}
	if _, _, err := p.Verify(context.Background(), "x", hashWith("x", 8192, 1, 1)); err == nil {
		t.Fatal("verified while every slot was taken")
	}
	<-p.slots // one frees up: hashing works again
	if _, err := p.Hash(context.Background(), "password", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
}

func TestHashingStopsWaitingWhenTheRequestIsCancelled(t *testing.T) {
	p := NewPasswords()
	for i := 0; i < MaxConcurrentHashes; i++ {
		p.slots <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Hash(ctx, "password", "correct horse battery"); err == nil {
		t.Fatal("no error for a cancelled request")
	}
}

func TestTheDummyHashParsesWithTodaysParameters(t *testing.T) {
	h, err := parseHash(dummyHash)
	if err != nil || h.needsRehash() {
		t.Fatalf("dummy hash: %+v, %v", h, err)
	}
}

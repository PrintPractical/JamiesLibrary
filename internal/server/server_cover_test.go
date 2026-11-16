package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/store"
	"jamieslibrary/internal/testutil"
	"jamieslibrary/internal/users"
)

const foxISBN = "9780140328721"

// coverSite is a running server with the owner signed in and a patron account.
type coverSite struct {
	s      *testutil.Server
	fake   *testutil.FakeOL
	owner  *http.Client
	patron *http.Client
}

func newCoverSite(t *testing.T) *coverSite {
	t.Helper()
	fake := testutil.FakeOpenLibrary(t)
	s := testutil.StartServer(t, testutil.Options{OpenLibrary: fake})
	code, ok := s.SetupCode()
	if !ok {
		t.Fatal("no setup code")
	}
	owner := s.Client()
	if resp, body := postJSON(t, owner, s.URL+"/api/setup/admin", setupBody(code, "jamie")); resp.StatusCode != 200 {
		t.Fatalf("setup: %d %s", resp.StatusCode, body)
	}
	// a patron, made straight in the database: nothing else makes one yet
	ctx := context.Background()
	st, err := store.Open(ctx, s.DataDir, store.Options{NoLock: true})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := auth.NewPasswords().Hash(ctx, "password", adminPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.New(st).Create(ctx, users.NewUser{Username: "pat", DisplayName: "Pat", PasswordHash: hash, Role: perm.Patron}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	patron := s.Client()
	if resp, body := postJSON(t, patron, s.URL+"/api/auth/signin", map[string]string{"username": "pat", "password": adminPassword}); resp.StatusCode != 200 {
		t.Fatalf("patron sign-in: %d %s", resp.StatusCode, body)
	}
	return &coverSite{s: s, fake: fake, owner: owner, patron: patron}
}

func (c *coverSite) lookup(t *testing.T) (token string) {
	t.Helper()
	resp, body := postJSON(t, c.owner, c.s.URL+"/api/catalog/lookup", map[string]string{"isbn": foxISBN})
	var look struct {
		CoverToken string `json:"cover_token"`
	}
	if resp.StatusCode != 200 || json.Unmarshal([]byte(body), &look) != nil || look.CoverToken == "" {
		t.Fatalf("lookup: %d %s", resp.StatusCode, body)
	}
	return look.CoverToken
}

func (c *coverSite) create(t *testing.T, token string) (int, string) {
	t.Helper()
	resp, body := postJSON(t, c.owner, c.s.URL+"/api/books", map[string]any{
		"title": "Fantastic Mr Fox", "authors": []string{"Roald Dahl"}, "isbn": foxISBN, "cover_token": token})
	return resp.StatusCode, body
}

// imageFiles counts the images stored directly in DATA_DIR/images.
func imageFiles(t *testing.T, dataDir string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataDir, "images"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			n++
		}
	}
	return n
}

// CAT-T1 "Browsing after the provider disappears": a book saved from a lookup
// keeps its cover on the installation, so a patron still sees it when the
// provider is gone, and nothing the book points at is another host's.
func TestCATT1_BrowsingAfterTheProviderDisappears(t *testing.T) {
	c := newCoverSite(t)
	status, body := c.create(t, c.lookup(t))
	if status != 201 {
		t.Fatalf("create: %d %s", status, body)
	}
	var made struct {
		ID       int64  `json:"id"`
		CoverURL string `json:"cover_url"`
	}
	if err := json.Unmarshal([]byte(body), &made); err != nil {
		t.Fatal(err)
	}

	c.fake.Close() // the provider is gone

	st, detail := getStatus(t, c.patron, c.s.URL+"/api/books/"+strconv.FormatInt(made.ID, 10))
	if st != 200 {
		t.Fatalf("book detail: %d %s", st, detail)
	}
	var d struct {
		CoverURL string `json:"cover_url"`
	}
	if err := json.Unmarshal([]byte(detail), &d); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(d.CoverURL, "/media/") {
		t.Fatalf("cover_url %q is not on the installation", d.CoverURL)
	}
	if strings.Contains(detail, "http://") || strings.Contains(detail, "https://") || strings.Contains(detail, c.fake.URL) {
		t.Fatalf("the book detail points off the installation: %s", detail)
	}
	resp, err := c.patron.Get(c.s.URL + d.CoverURL)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") || len(img) == 0 {
		t.Fatalf("cover: %d %s, %d bytes", resp.StatusCode, resp.Header.Get("Content-Type"), len(img))
	}
}

// A cover token buys one book's cover, once.
func TestCATT1_ACoverTokenIsUsedOnce(t *testing.T) {
	c := newCoverSite(t)
	token := c.lookup(t)
	if status, body := c.create(t, token); status != 201 {
		t.Fatalf("create: %d %s", status, body)
	}
	before := imageFiles(t, c.s.DataDir)
	resp, body := postJSON(t, c.owner, c.s.URL+"/api/books", map[string]any{
		"title": "Another", "authors": []string{"A"}, "cover_token": token})
	if resp.StatusCode != 422 || !strings.Contains(body, "cover_token") {
		t.Fatalf("reused token: %d %s", resp.StatusCode, body)
	}
	if n := imageFiles(t, c.s.DataDir); n != before {
		t.Errorf("%d image files, was %d", n, before)
	}
}

// A create that fails after the cover was taken leaves no image behind.
func TestCATT1_AFailedCreateLeavesNoImage(t *testing.T) {
	c := newCoverSite(t)
	token := c.lookup(t)
	// the book is added without the cover first, so saving with the token is refused as a duplicate
	resp, body := postJSON(t, c.owner, c.s.URL+"/api/books", map[string]any{
		"title": "Fantastic Mr Fox", "authors": []string{"Roald Dahl"}, "isbn": foxISBN})
	if resp.StatusCode != 201 {
		t.Fatalf("first create: %d %s", resp.StatusCode, body)
	}
	if status, body := c.create(t, token); status != 409 {
		t.Fatalf("duplicate: %d %s", status, body)
	}
	if n := imageFiles(t, c.s.DataDir); n != 0 {
		t.Errorf("%d image files after a refused create", n)
	}
}

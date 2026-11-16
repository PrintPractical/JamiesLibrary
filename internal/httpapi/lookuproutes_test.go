package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"jamieslibrary/internal/catalog"
	"jamieslibrary/internal/clock"
	"jamieslibrary/internal/metadata"
	"jamieslibrary/internal/perm"
)

// fakeProvider answers as Open Library does for one book, or badly on request.
type fakeProvider struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []string
	mode string // "", "error" or "slow"
}

const foxISBN = "9780140328721"

func newFakeProvider(t *testing.T) *fakeProvider {
	f := &fakeProvider{}
	var cover bytes.Buffer
	_ = jpeg.Encode(&cover, image.NewRGBA(image.Rect(0, 0, 40, 60)), nil)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs = append(f.reqs, r.URL.RequestURI())
		mode := f.mode
		f.mu.Unlock()
		switch mode {
		case "error":
			http.Error(w, "boom", 500)
			return
		case "slow":
			select {
			case <-r.Context().Done():
			case <-time.After(time.Minute):
			}
			return
		}
		switch {
		case r.URL.Path == "/api/books" && strings.Contains(r.URL.RawQuery, "ISBN:"+foxISBN):
			fmt.Fprint(w, `{"ISBN:`+foxISBN+`":{"title":"Fantastic Mr Fox","authors":[{"name":"Roald Dahl"}],"cover":{"large":"x"}}}`)
		case r.URL.Path == "/api/books":
			fmt.Fprint(w, `{}`)
		case r.URL.Path == "/isbn/"+foxISBN+".json":
			fmt.Fprint(w, `{"description":"Three farmers, one fox."}`)
		case r.URL.Path == "/b/isbn/"+foxISBN+"-L.jpg":
			w.Write(cover.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeProvider) setMode(m string) { f.mu.Lock(); f.mode = m; f.mu.Unlock() }
func (f *fakeProvider) Requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reqs...)
}

type lookupSite struct {
	*site
	cat  *catalog.Service
	fake *fakeProvider
	tmp  string
}

func newLookupSite(t *testing.T) *lookupSite {
	t.Helper()
	l := &lookupSite{fake: newFakeProvider(t)}
	l.site = newSite(t, func(o *Options) {
		l.cat = catalog.New(o.Store, catalog.Options{Settings: o.Settings, Now: clock.Now})
		tmp, err := o.Media.Sibling(o.Store.TmpDir())
		if err != nil {
			t.Fatal(err)
		}
		l.tmp = o.Store.TmpDir()
		o.Catalog = l.cat
		o.Metadata, err = metadata.New(metadata.Options{Images: o.Media, Tmp: tmp, Version: "test", BaseURL: l.fake.srv.URL,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: clock.Now})
		if err != nil {
			t.Fatal(err)
		}
	})
	return l
}

func (l *lookupSite) lookup(cookie, isbn string) *httptest.ResponseRecorder {
	return l.req("POST", "/api/catalog/lookup", cookie, body(map[string]string{"isbn": isbn}))
}

func TestLookupNeedsCatalogManage(t *testing.T) {
	l := newLookupSite(t)
	l.addUser("pat", goodPassword, perm.Patron)
	pat := l.signIn("pat", goodPassword)
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/catalog/lookup", body(map[string]string{"isbn": "9780140328721"})},
		{"GET", "/media-tmp/" + strings.Repeat("a", 32) + ".jpg", ""},
	} {
		if rec := l.req(c.method, c.path, pat, c.body); rec.Code != 403 {
			t.Errorf("%s as a patron: %d", c.path, rec.Code)
		}
		if rec := l.req(c.method, c.path, "", c.body); rec.Code != 401 {
			t.Errorf("%s signed out: %d", c.path, rec.Code)
		}
	}
	if reqs := l.fake.Requests(); len(reqs) != 0 {
		t.Errorf("the provider was asked: %v", reqs)
	}
}

// SCAN-2 "Found": the prefill comes back, the cover can be previewed by the
// caller, and nothing is saved.
func TestSCAN2_FoundThroughTheAPI(t *testing.T) {
	l := newLookupSite(t)
	l.addUser("lib", goodPassword, perm.Librarian)
	l.addUser("other", goodPassword, perm.Librarian)
	lib, other := l.signIn("lib", goodPassword), l.signIn("other", goodPassword)

	got := decodeAs[lookupBody](t, l.lookup(lib, "0-14-032872-6"), 200)
	if got.Status != "found" || got.Title != "Fantastic Mr Fox" || got.Authors[0] != "Roald Dahl" ||
		got.Description != "Three farmers, one fox." || got.ISBN != "9780140328721" || got.CoverToken == "" ||
		got.CoverURL != "/media-tmp/"+got.CoverToken {
		t.Fatalf("%+v", got)
	}
	preview := l.req("GET", got.CoverURL, lib, "")
	if preview.Code != 200 || preview.Header().Get("Content-Type") != "image/jpeg" ||
		preview.Header().Get("X-Content-Type-Options") != "nosniff" || preview.Body.Len() == 0 {
		t.Errorf("preview: %d %v", preview.Code, preview.Header())
	}
	if rec := l.req("GET", got.CoverURL, other, ""); rec.Code != 404 {
		t.Errorf("another librarian saw the cover: %d", rec.Code)
	}
	if rec := l.req("GET", "/media-tmp/../library.db", lib, ""); rec.Code == 200 {
		t.Errorf("a path worked: %d", rec.Code)
	}
	// Nothing saved: no book, no stored image.
	var books int
	_ = l.st.DB().QueryRow(`SELECT count(*) FROM books`).Scan(&books)
	if ents, _ := os.ReadDir(filepath.Join(l.dir, "images")); books != 0 || len(ents) != 0 {
		t.Errorf("the lookup saved %d books and %d images", books, len(ents))
	}
}

// SCAN-2 "Not found or provider down".
func TestSCAN2_NotFoundOrProviderDownThroughTheAPI(t *testing.T) {
	l := newLookupSite(t)
	l.addUser("lib", goodPassword, perm.Librarian)
	lib := l.signIn("lib", goodPassword)

	if got := decodeAs[lookupBody](t, l.lookup(lib, "9780306406157"), 200); got.Status != "not_found" {
		t.Errorf("unknown ISBN: %+v", got)
	}
	l.fake.setMode("error")
	rec := l.lookup(lib, "9780140328721")
	if rec.Code != 503 || errMessage(t, rec) != "Lookup unavailable, enter the details." {
		t.Errorf("provider down: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "500") || strings.Contains(rec.Body.String(), l.fake.srv.URL) {
		t.Errorf("the failure's detail reached the user: %s", rec.Body)
	}
}

func TestLookupOfABadISBNAsksNobody(t *testing.T) {
	l := newLookupSite(t)
	l.addUser("lib", goodPassword, perm.Librarian)
	lib := l.signIn("lib", goodPassword)
	for _, isbn := range []string{"", "12345", "9780140328722", "not an isbn"} {
		if rec := l.lookup(lib, isbn); rec.Code != 422 {
			t.Errorf("%q: %d %s", isbn, rec.Code, rec.Body)
		}
	}
	if rec := l.req("POST", "/api/catalog/lookup", lib, `{"isbn":"9780140328721","extra":1}`); rec.Code != 400 {
		t.Errorf("unknown field: %d", rec.Code)
	}
	if reqs := l.fake.Requests(); len(reqs) != 0 {
		t.Errorf("the provider was asked: %v", reqs)
	}
}

// SCAN-2: an ISBN already in the catalog gives that book's id, and the provider isn't asked.
func TestSCAN2_LookupOfABookAlreadyInTheCatalog(t *testing.T) {
	l := newLookupSite(t)
	l.addUser("lib", goodPassword, perm.Librarian)
	lib := l.signIn("lib", goodPassword)
	b := addBook(t, l.cat, "Fantastic Mr Fox", "9780140328721", 1)
	got := decodeAs[lookupBody](t, l.lookup(lib, "0140328726"), 200)
	if got.Status != "exists" || got.BookID != b.ID || got.Title != "" || got.CoverToken != "" {
		t.Errorf("%+v", got)
	}
	if reqs := l.fake.Requests(); len(reqs) != 0 {
		t.Errorf("the provider was asked: %v", reqs)
	}
}

// DEPLOY-T5 "Adding a book offline": a provider that never answers is given up
// on at the 10-second deadline, so the form is offered for manual entry.
func TestDEPLOY_T5_AddingABookOfflineThroughTheAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the real 10-second deadline")
	}
	l := newLookupSite(t)
	l.addUser("lib", goodPassword, perm.Librarian)
	lib := l.signIn("lib", goodPassword)
	l.fake.setMode("slow")
	start := time.Now()
	rec := l.lookup(lib, "9780140328721")
	d := time.Since(start)
	if rec.Code != 503 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if d < 9*time.Second || d > 11*time.Second {
		t.Errorf("gave up after %v, want about 10 s", d)
	}
}

func TestLookupBodyHasNoSurprises(t *testing.T) {
	var b lookupBody
	if err := json.Unmarshal([]byte(`{"status":"not_found"}`), &b); err != nil || b.Status != "not_found" {
		t.Fatal(err)
	}
}

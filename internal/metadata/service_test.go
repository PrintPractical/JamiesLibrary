package metadata_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/media"
	"jamieslibrary/internal/metadata"
	"jamieslibrary/internal/testutil"
)

const (
	foxISBN13 = "9780140328721"
	foxISBN10 = "0140328726"
	user      = int64(7)
)

type rig struct {
	svc            *metadata.Service
	fake           *testutil.FakeOL
	imagesDir, tmp string
}

func newRig(t *testing.T, mutate func(*metadata.Options)) *rig {
	t.Helper()
	fake := testutil.FakeOpenLibrary(t)
	return newRigAt(t, fake.URL, fake, mutate)
}

func newRigAt(t *testing.T, base string, fake *testutil.FakeOL, mutate func(*metadata.Options)) *rig {
	t.Helper()
	dir := t.TempDir()
	images, err := media.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := images.Sibling(filepath.Join(dir, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	o := metadata.Options{Images: images, Tmp: tmp, Version: "1.2.3", BaseURL: base,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if mutate != nil {
		mutate(&o)
	}
	svc, err := metadata.New(o)
	if err != nil {
		t.Fatal(err)
	}
	return &rig{svc: svc, fake: fake, imagesDir: filepath.Join(dir, "images"), tmp: filepath.Join(dir, "tmp")}
}

func files(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

// SCAN-2 "Found": the title, authors, description and cover come back to prefill
// the form, and nothing is saved by the lookup itself.
func TestSCAN2_Found(t *testing.T) {
	r := newRig(t, nil)
	b, err := r.svc.Lookup(context.Background(), user, foxISBN13)
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "Fantastic Mr Fox" || len(b.Authors) != 1 || b.Authors[0] != "Roald Dahl" ||
		b.Description != "Three farmers, one fox." || b.ISBN13 != foxISBN13 || b.CoverToken == "" {
		t.Fatalf("book = %+v", b)
	}
	if !media.ValidName(b.CoverToken) || strings.ContainsAny(b.CoverToken, "/\\") {
		t.Errorf("the token %q is not a bare generated name, and must never be a path", b.CoverToken)
	}
	// Nothing saved: the cover waits in tmp, the library's images are untouched.
	if got := files(t, r.imagesDir); len(got) != 0 {
		t.Errorf("images/ holds %v after a lookup", got)
	}
	if got := files(t, r.tmp); len(got) != 1 || got[0] != b.CoverToken {
		t.Errorf("tmp/ holds %v, want just the token", got)
	}
	f, err := r.svc.OpenCover(b.CoverToken, user)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, format, err := image.Decode(f); err != nil || format != "jpeg" {
		t.Errorf("preview is not a stored JPEG: %v %q", err, format)
	}
	reqs := strings.Join(r.fake.Requests(), "\n")
	if !strings.Contains(reqs, "default=false") {
		t.Errorf("the cover wasn't asked for with default=false:\n%s", reqs)
	}
}

// CAT-T1: when the book is saved the cover moves into the library's own images,
// and what it was fetched as is no longer needed.
func TestCAT_T1_TakeCoverMovesItIntoImages(t *testing.T) {
	r := newRig(t, nil)
	b, err := r.svc.Lookup(context.Background(), user, foxISBN13)
	if err != nil {
		t.Fatal(err)
	}
	name, err := r.svc.TakeCover(b.CoverToken, user)
	if err != nil || name != b.CoverToken {
		t.Fatalf("TakeCover = %q, %v", name, err)
	}
	if got := files(t, r.imagesDir); len(got) != 1 || got[0] != name {
		t.Errorf("images/ holds %v", got)
	}
	if got := files(t, r.tmp); len(got) != 0 {
		t.Errorf("tmp/ still holds %v", got)
	}
	if _, err := r.svc.TakeCover(b.CoverToken, user); !errors.Is(err, media.ErrNotFound) {
		t.Errorf("a spent token worked again: %v", err)
	}
}

func TestCoverTokenBelongsToWhoFetchedIt(t *testing.T) {
	r := newRig(t, nil)
	b, _ := r.svc.Lookup(context.Background(), user, foxISBN13)
	if _, err := r.svc.OpenCover(b.CoverToken, user+1); !errors.Is(err, media.ErrNotFound) {
		t.Errorf("someone else could preview it: %v", err)
	}
	if _, err := r.svc.TakeCover(b.CoverToken, user+1); !errors.Is(err, media.ErrNotFound) {
		t.Errorf("someone else could take it: %v", err)
	}
	for _, tok := range []string{"", "../../library.db", "x.jpg", strings.Repeat("a", 32) + ".jpg"} {
		if _, err := r.svc.OpenCover(tok, user); !errors.Is(err, media.ErrNotFound) {
			t.Errorf("token %q: %v", tok, err)
		}
		if _, err := r.svc.TakeCover(tok, user); !errors.Is(err, media.ErrNotFound) {
			t.Errorf("token %q: %v", tok, err)
		}
	}
	if len(files(t, r.imagesDir)) != 0 {
		t.Error("a refused token still put something in images/")
	}
}

func TestCoverTokenExpires(t *testing.T) {
	now := time.Now()
	r := newRig(t, func(o *metadata.Options) { o.Now = func() time.Time { return now } })
	b, _ := r.svc.Lookup(context.Background(), user, foxISBN13)
	now = now.Add(61 * time.Minute)
	if _, err := r.svc.TakeCover(b.CoverToken, user); !errors.Is(err, media.ErrNotFound) {
		t.Errorf("an hour-old token worked: %v", err)
	}
}

func TestWaitingCoversAreBounded(t *testing.T) {
	r := newRig(t, nil)
	for i := 0; i < 230; i++ {
		if _, err := r.svc.Lookup(context.Background(), user, foxISBN13); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(files(t, r.tmp)); n > 200 {
		t.Errorf("%d covers wait in tmp/, want at most 200", n)
	}
}

// SCAN-2 "Not found or provider down": the provider doesn't know the book.
func TestSCAN2_NotFoundOrProviderDown_NotFound(t *testing.T) {
	r := newRig(t, nil)
	_, err := r.svc.Lookup(context.Background(), user, "9780306406157") // valid, not a fixture
	if !errors.Is(err, metadata.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if len(files(t, r.tmp)) != 0 {
		t.Error("a cover was kept for a book that wasn't found")
	}
}

// SCAN-2 "Not found or provider down": a provider that fails in any way is
// unavailable, never "not found" and never a crash.
func TestSCAN2_NotFoundOrProviderDown_Unavailable(t *testing.T) {
	for name, mode := range map[string]testutil.Mode{
		"403 over quota": testutil.ModeForbidden, "429": testutil.ModeTooManyRequests,
		"500": testutil.ModeServerError, "garbage": testutil.ModeGarbage, "connection dropped": testutil.ModeGone,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, nil)
			r.fake.SetMode(mode)
			if _, err := r.svc.Lookup(context.Background(), user, foxISBN13); !errors.Is(err, metadata.ErrUnavailable) {
				t.Errorf("err = %v, want ErrUnavailable", err)
			}
		})
	}
	t.Run("provider gone", func(t *testing.T) {
		r := newRig(t, nil)
		r.fake.Close()
		if _, err := r.svc.Lookup(context.Background(), user, foxISBN13); !errors.Is(err, metadata.ErrUnavailable) {
			t.Errorf("err = %v", err)
		}
	})
}

// DEPLOY-T5 "Adding a book offline": a provider that never answers is given up
// on at the deadline. The deadline is the real 10 s; here a short one is set.
func TestDEPLOY_T5_AddingABookOffline(t *testing.T) {
	r := newRig(t, func(o *metadata.Options) { o.Timeout = 300 * time.Millisecond })
	r.fake.SetMode(testutil.ModeSlow)
	r.fake.SetSlowDelay(time.Minute)
	start := time.Now()
	_, err := r.svc.Lookup(context.Background(), user, foxISBN13)
	if !errors.Is(err, metadata.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("gave up after %v", d)
	}
}

func TestTheDefaultDeadlineIsTenSeconds(t *testing.T) {
	if metadata.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v", metadata.Timeout)
	}
}

// One deadline covers the whole lookup, the cover too: a provider that answers
// the data at once and the cover never is given up on in time. The book is
// found, without its cover.
func TestOneDeadlineCoversTheCoverToo(t *testing.T) {
	o := newOdd(t, func(o *odd, w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/books"):
			fmt.Fprint(w, `{"ISBN:`+foxISBN13+`":{"title":"T","authors":[{"name":"A"}],"cover":{"large":"x"}}}`)
		case strings.HasPrefix(r.URL.Path, "/b/isbn/"):
			select {
			case <-r.Context().Done():
			case <-time.After(time.Minute):
			}
		default:
			http.NotFound(w, r)
		}
	})
	r := newRigAt(t, o.srv.URL, nil, func(o *metadata.Options) { o.Timeout = 400 * time.Millisecond })
	start := time.Now()
	b, err := r.svc.Lookup(context.Background(), user, foxISBN13)
	if err != nil || b.Title != "T" || b.CoverToken != "" || time.Since(start) > 2*time.Second {
		t.Errorf("%+v, %v after %v", b, err, time.Since(start))
	}
}

func TestBadISBNAsksNobody(t *testing.T) {
	r := newRig(t, nil)
	for _, isbn := range []string{"", "abc", "9780140328722", "0140328727", "123", "9780140328721x", "97801403287211", "../etc"} {
		_, err := r.svc.Lookup(context.Background(), user, isbn)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.Invalid || ae.Field != "isbn" {
			t.Errorf("isbn %q: err = %v", isbn, err)
		}
	}
	if reqs := r.fake.Requests(); len(reqs) != 0 {
		t.Errorf("requests were made: %v", reqs)
	}
}

func TestISBN10AndHyphenatedFormsAskForTheSameBook(t *testing.T) {
	var want []string
	for i, form := range []string{foxISBN13, foxISBN10, "978-0-14-032872-1", "0-14-032872-6", " 0140328726 "} {
		r := newRig(t, nil)
		b, err := r.svc.Lookup(context.Background(), user, form)
		if err != nil || b.Title != "Fantastic Mr Fox" || b.ISBN13 != foxISBN13 {
			t.Fatalf("%q: %+v, %v", form, b, err)
		}
		got := r.fake.Requests()
		if i == 0 {
			want = got
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%q asked %v, the ISBN-13 asked %v", form, got, want)
		}
	}
}

func TestNoCoverMeansNone(t *testing.T) {
	r := newRig(t, nil)
	b, err := r.svc.Lookup(context.Background(), user, "9780064400558") // Charlotte's Web: no cover
	if err != nil || b.Title != "Charlotte's Web" || b.CoverToken != "" {
		t.Fatalf("%+v, %v", b, err)
	}
	if len(files(t, r.tmp)) != 0 {
		t.Error("something was kept")
	}
}

func TestOneByOneCoverMeansNone(t *testing.T) {
	r := newRig(t, nil)
	r.fake.SetMode(testutil.ModeTinyCover)
	b, err := r.svc.Lookup(context.Background(), user, foxISBN13)
	if err != nil || b.Title == "" || b.CoverToken != "" {
		t.Fatalf("%+v, %v", b, err)
	}
	if len(files(t, r.tmp)) != 0 {
		t.Error("the placeholder was kept")
	}
}

// A provider the program doesn't know: a custom server, so each misbehaviour
// can be shown.
type odd struct {
	srv  *httptest.Server
	hits atomic.Int32
	ua   atomic.Value
}

func newOdd(t *testing.T, h func(o *odd, w http.ResponseWriter, r *http.Request)) *odd {
	o := &odd{}
	o.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.hits.Add(1)
		o.ua.Store(r.Header.Get("User-Agent"))
		h(o, w, r)
	}))
	t.Cleanup(o.srv.Close)
	return o
}

func TestARedirectToAnotherHostIsRefused(t *testing.T) {
	target := newOdd(t, func(o *odd, w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) })
	from := newOdd(t, func(o *odd, w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.srv.URL+r.URL.RequestURI(), http.StatusFound)
	})
	r := newRigAt(t, from.srv.URL, nil, nil)
	if _, err := r.svc.Lookup(context.Background(), user, foxISBN13); !errors.Is(err, metadata.ErrUnavailable) {
		t.Errorf("err = %v", err)
	}
	if target.hits.Load() != 0 {
		t.Error("the other host was asked")
	}
}

func TestEndlessRedirectsStopAtFive(t *testing.T) {
	o := newOdd(t, func(o *odd, w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, fmt.Sprintf("/loop%d", o.hits.Load()), http.StatusFound)
	})
	r := newRigAt(t, o.srv.URL, nil, nil)
	if _, err := r.svc.Lookup(context.Background(), user, foxISBN13); !errors.Is(err, metadata.ErrUnavailable) {
		t.Errorf("err = %v", err)
	}
	if n := o.hits.Load(); n > 6 {
		t.Errorf("%d requests, want at most 6 (the first and 5 hops)", n)
	}
}

func TestAnOversizedAnswerIsRefusedNotTruncated(t *testing.T) {
	o := newOdd(t, func(o *odd, w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ISBN:`+foxISBN13+`":{"title":"`)
		w.Write(bytes.Repeat([]byte("a"), 1<<20))
		fmt.Fprint(w, `"}}`)
	})
	r := newRigAt(t, o.srv.URL, nil, nil)
	if _, err := r.svc.Lookup(context.Background(), user, foxISBN13); !errors.Is(err, metadata.ErrUnavailable) {
		t.Errorf("err = %v", err)
	}
}

func TestAnOversizedCoverMeansNone(t *testing.T) {
	o := newOdd(t, func(o *odd, w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/books"):
			fmt.Fprint(w, `{"ISBN:`+foxISBN13+`":{"title":"T","authors":[{"name":"A"}],"cover":{"large":"x"}}}`)
		case strings.HasPrefix(r.URL.Path, "/b/isbn/"):
			w.Write(testutil.CoverJPEG("x"))
			w.Write(bytes.Repeat([]byte{0}, 5<<20))
		default:
			http.NotFound(w, r)
		}
	})
	r := newRigAt(t, o.srv.URL, nil, nil)
	b, err := r.svc.Lookup(context.Background(), user, foxISBN13)
	if err != nil || b.Title != "T" || b.CoverToken != "" {
		t.Fatalf("%+v, %v", b, err)
	}
	if len(files(t, r.tmp)) != 0 {
		t.Error("an oversized cover was kept")
	}
}

func TestACoverThatIsNotAPictureMeansNone(t *testing.T) {
	o := newOdd(t, func(o *odd, w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/books"):
			fmt.Fprint(w, `{"ISBN:`+foxISBN13+`":{"title":"T","authors":[{"name":"A"}],"cover":{"large":"x"}}}`)
		case strings.HasPrefix(r.URL.Path, "/b/isbn/"):
			fmt.Fprint(w, `<svg onload="alert(1)"/>`)
		default:
			http.NotFound(w, r)
		}
	})
	r := newRigAt(t, o.srv.URL, nil, nil)
	b, err := r.svc.Lookup(context.Background(), user, foxISBN13)
	if err != nil || b.CoverToken != "" || len(files(t, r.tmp)) != 0 {
		t.Fatalf("%+v, %v", b, err)
	}
}

func TestProviderTextIsBoundedAndCleaned(t *testing.T) {
	var authors []string
	for i := 0; i < 30; i++ {
		authors = append(authors, fmt.Sprintf(`{"name":"Author %d"}`, i))
	}
	o := newOdd(t, func(o *odd, w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/books"):
			fmt.Fprintf(w, `{"ISBN:%s":{"title":"%s\u0000‮!","authors":[%s,{"name":"  "}]}}`,
				foxISBN13, strings.Repeat("t", 700), strings.Join(authors, ","))
		case strings.HasPrefix(r.URL.Path, "/isbn/"):
			fmt.Fprintf(w, `{"description":"%s"}`, strings.Repeat("d", 20000))
		default:
			http.NotFound(w, r)
		}
	})
	r := newRigAt(t, o.srv.URL, nil, nil)
	b, err := r.svc.Lookup(context.Background(), user, foxISBN13)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Title) != 500 || len(b.Authors) != 20 || len(b.Description) != 10000 {
		t.Errorf("title %d, authors %d, description %d", len(b.Title), len(b.Authors), len(b.Description))
	}
}

func TestDescriptionFallsBackToTheWork(t *testing.T) {
	o := newOdd(t, func(o *odd, w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/books":
			fmt.Fprint(w, `{"ISBN:`+foxISBN13+`":{"title":"T","authors":[{"name":"A"}]}}`)
		case "/isbn/" + foxISBN13 + ".json":
			fmt.Fprint(w, `{"works":[{"key":"/works/OL1W"}]}`)
		case "/works/OL1W.json":
			fmt.Fprint(w, `{"description":{"type":"/type/text","value":"From the work."}}`)
		default:
			http.NotFound(w, r)
		}
	})
	r := newRigAt(t, o.srv.URL, nil, nil)
	b, err := r.svc.Lookup(context.Background(), user, foxISBN13)
	if err != nil || b.Description != "From the work." {
		t.Fatalf("%+v, %v", b, err)
	}
}

func TestADescriptionThatFailsStillFindsTheBook(t *testing.T) {
	o := newOdd(t, func(o *odd, w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/books") {
			fmt.Fprint(w, `{"ISBN:`+foxISBN13+`":{"title":"T","authors":[{"name":"A"}]}}`)
			return
		}
		http.Error(w, "boom", 500)
	})
	r := newRigAt(t, o.srv.URL, nil, nil)
	b, err := r.svc.Lookup(context.Background(), user, foxISBN13)
	if err != nil || b.Title != "T" || b.Description != "" {
		t.Fatalf("%+v, %v", b, err)
	}
}

func TestTheRequestNamesTheProgramAndItsVersion(t *testing.T) {
	o := newOdd(t, func(o *odd, w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) })
	r := newRigAt(t, o.srv.URL, nil, nil)
	_, _ = r.svc.Lookup(context.Background(), user, foxISBN13)
	ua, _ := o.ua.Load().(string)
	if !strings.HasPrefix(ua, "JamiesLibrary/1.2.3 ") {
		t.Errorf("User-Agent = %q", ua)
	}
}

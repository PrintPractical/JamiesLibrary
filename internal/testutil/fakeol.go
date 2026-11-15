package testutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Mode is how the fake Open Library answers.
type Mode int

const (
	// ModeOK answers from the fixtures.
	ModeOK Mode = iota
	// ModeSlow takes longer than the server's 10-second deadline before answering.
	ModeSlow
	// ModeForbidden answers 403, as Open Library does over its cover quota.
	ModeForbidden
	// ModeTooManyRequests answers 429.
	ModeTooManyRequests
	// ModeServerError answers 500.
	ModeServerError
	// ModeGarbage answers 200 with a body that is neither JSON nor an image.
	ModeGarbage
	// ModeTinyCover answers covers with a 1x1 pixel image, as Open Library does
	// for a missing cover without default=false. Everything else is as ModeOK.
	ModeTinyCover
	// ModeGone drops every connection without answering, as a provider that is down does.
	ModeGone
)

// Book is one fixture: what the fake knows about an ISBN.
type Book struct {
	ISBN13      string
	Title       string
	Authors     []string
	Description string
	Publisher   string
	Published   string
	Pages       int
	NoCover     bool // the provider has no cover for it
}

// DefaultBooks are the fixtures every FakeOpenLibrary starts with.
func DefaultBooks() []Book {
	return []Book{
		{ISBN13: "9780140328721", Title: "Fantastic Mr Fox", Authors: []string{"Roald Dahl"}, Description: "Three farmers, one fox.", Publisher: "Puffin", Published: "1988", Pages: 96},
		{ISBN13: "9780261103573", Title: "The Fellowship of the Ring", Authors: []string{"J. R. R. Tolkien"}, Description: "The first part of a long journey.", Publisher: "HarperCollins", Published: "1999", Pages: 432},
		{ISBN13: "9780064400558", Title: "Charlotte's Web", Authors: []string{"E. B. White", "Garth Williams"}, Publisher: "HarperTrophy", Published: "2001", Pages: 184, NoCover: true},
	}
}

// FakeOL is a stand-in for Open Library on a local port. Point the server at
// URL (Options.OpenLibrary does it) and choose how it answers with SetMode.
type FakeOL struct {
	// URL is the base URL: it answers the paths of openlibrary.org and of
	// covers.openlibrary.org on the one host.
	URL string
	// SlowDelay is how long ModeSlow holds a request: past the server's 10 s deadline.
	SlowDelay time.Duration

	srv     *httptest.Server
	closing chan struct{}
	once    sync.Once
	mu      sync.Mutex
	mode    Mode
	books   map[string]Book
	reqs    []string
}

// FakeOpenLibrary starts the fake with DefaultBooks. It stops when the test ends.
//
// Paths it answers, as Open Library does:
//
//	GET /api/books?bibkeys=ISBN:N&format=json&jscmd=data   book data, {} if unknown
//	GET /isbn/N.json                                       the edition, 404 if unknown
//	GET /authors/KEY.json                                  an author's name
//	GET /b/isbn/N-{S,M,L}.jpg[?default=false]              the cover; 404 if none and default=false,
//	                                                       else a 1x1 pixel image
func FakeOpenLibrary(t testing.TB) *FakeOL {
	t.Helper()
	f := &FakeOL{SlowDelay: 15 * time.Second, closing: make(chan struct{}), books: map[string]Book{}}
	for _, b := range DefaultBooks() {
		f.books[b.ISBN13] = b
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/books", f.handle(f.apiBooks))
	mux.HandleFunc("GET /isbn/{file}", f.handle(f.edition))
	mux.HandleFunc("GET /authors/{file}", f.handle(f.author))
	mux.HandleFunc("GET /b/isbn/{file}", f.handle(f.cover))
	f.srv = httptest.NewServer(mux)
	f.URL = f.srv.URL
	t.Cleanup(f.Close)
	return f
}

// Close stops the fake, as a provider that is gone. It is safe to call twice.
func (f *FakeOL) Close() {
	f.once.Do(func() {
		close(f.closing)
		f.srv.CloseClientConnections()
		f.srv.Close()
	})
}

// SetMode chooses how the fake answers from now on.
func (f *FakeOL) SetMode(m Mode) {
	f.mu.Lock()
	f.mode = m
	f.mu.Unlock()
}

// AddBook adds or replaces a fixture.
func (f *FakeOL) AddBook(b Book) {
	f.mu.Lock()
	f.books[b.ISBN13] = b
	f.mu.Unlock()
}

// Requests is every request received so far, as "METHOD /path?query", oldest first.
func (f *FakeOL) Requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reqs...)
}

func (f *FakeOL) book(isbn string) (Book, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.books[isbn]
	return b, ok
}

func (f *FakeOL) handle(next func(http.ResponseWriter, *http.Request, Mode)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs = append(f.reqs, r.Method+" "+r.URL.RequestURI())
		mode := f.mode
		f.mu.Unlock()
		switch mode {
		case ModeSlow:
			select {
			case <-time.After(f.SlowDelay):
			case <-r.Context().Done():
				return
			case <-f.closing:
				return
			}
		case ModeForbidden:
			http.Error(w, "over quota", http.StatusForbidden)
			return
		case ModeTooManyRequests:
			w.Header().Set("Retry-After", "60")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		case ModeServerError:
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		case ModeGarbage:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"title\": <<not json \x00\xff"))
			return
		case ModeGone:
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
				}
			}
			return
		}
		next(w, r, mode)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func authorKey(i int, isbn string) string { return fmt.Sprintf("OL%sA%d", isbn[len(isbn)-4:], i) }

func (f *FakeOL) coverURL(isbn, size string) string {
	return f.URL + "/b/isbn/" + isbn + "-" + size + ".jpg"
}

func (f *FakeOL) apiBooks(w http.ResponseWriter, r *http.Request, _ Mode) {
	out := map[string]any{}
	for _, key := range strings.Split(r.URL.Query().Get("bibkeys"), ",") {
		isbn := strings.TrimPrefix(key, "ISBN:")
		b, ok := f.book(isbn)
		if !ok {
			continue
		}
		var authors, publishers []map[string]string
		for _, a := range b.Authors {
			authors = append(authors, map[string]string{"name": a})
		}
		if b.Publisher != "" {
			publishers = append(publishers, map[string]string{"name": b.Publisher})
		}
		entry := map[string]any{
			"title": b.Title, "authors": authors, "publishers": publishers,
			"publish_date": b.Published, "number_of_pages": b.Pages,
			"identifiers": map[string][]string{"isbn_13": {b.ISBN13}},
		}
		if !b.NoCover {
			entry["cover"] = map[string]string{"small": f.coverURL(isbn, "S"), "medium": f.coverURL(isbn, "M"), "large": f.coverURL(isbn, "L")}
		}
		out[key] = entry
	}
	writeJSON(w, out)
}

func (f *FakeOL) edition(w http.ResponseWriter, r *http.Request, _ Mode) {
	isbn, ok := strings.CutSuffix(r.PathValue("file"), ".json")
	b, found := f.book(isbn)
	if !ok || !found {
		http.NotFound(w, r)
		return
	}
	var authors []map[string]string
	for i := range b.Authors {
		authors = append(authors, map[string]string{"key": "/authors/" + authorKey(i, isbn)})
	}
	e := map[string]any{"title": b.Title, "authors": authors, "isbn_13": []string{b.ISBN13}, "publish_date": b.Published, "number_of_pages": b.Pages}
	if b.Description != "" {
		e["description"] = map[string]string{"type": "/type/text", "value": b.Description}
	}
	if !b.NoCover {
		e["covers"] = []int{1}
	}
	writeJSON(w, e)
}

func (f *FakeOL) author(w http.ResponseWriter, r *http.Request, _ Mode) {
	key, ok := strings.CutSuffix(r.PathValue("file"), ".json")
	f.mu.Lock()
	defer f.mu.Unlock()
	if ok {
		for isbn, b := range f.books {
			for i, name := range b.Authors {
				if authorKey(i, isbn) == key {
					writeJSON(w, map[string]string{"name": name, "key": "/authors/" + key})
					return
				}
			}
		}
	}
	http.NotFound(w, r)
}

func (f *FakeOL) cover(w http.ResponseWriter, r *http.Request, mode Mode) {
	name := r.PathValue("file")
	isbn, _, _ := strings.Cut(name, "-")
	b, found := f.book(isbn)
	if !strings.HasSuffix(name, ".jpg") {
		http.NotFound(w, r)
		return
	}
	missing := !found || b.NoCover
	if missing && mode != ModeTinyCover && r.URL.Query().Get("default") == "false" {
		http.NotFound(w, r)
		return
	}
	if missing || mode == ModeTinyCover {
		w.Header().Set("Content-Type", "image/gif")
		_, _ = w.Write(TinyGIF())
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	_, _ = w.Write(CoverJPEG(isbn))
}

// TinyGIF is the 1x1 pixel image Open Library sends for a missing cover.
func TinyGIF() []byte {
	var b bytes.Buffer
	img := image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.White})
	_ = gif.Encode(&b, img, nil)
	return b.Bytes()
}

// CoverJPEG is a 200x300 picture of a book cover, the same bytes for the same ISBN.
func CoverJPEG(isbn string) []byte {
	var seed int
	for _, c := range isbn {
		seed = seed*31 + int(c)
	}
	img := image.NewRGBA(image.Rect(0, 0, 200, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 200; x++ {
			img.Set(x, y, color.RGBA{R: uint8(seed), G: uint8(60 + y/2), B: uint8(seed>>8 + x/2), A: 255})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 80})
	return b.Bytes()
}

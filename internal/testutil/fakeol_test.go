package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const fox = "9780140328721"

func fetch(t *testing.T, url string) (int, []byte, error) {
	t.Helper()
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, err
}

func bookURL(f *FakeOL, isbn string) string {
	return f.URL + "/api/books?bibkeys=ISBN:" + isbn + "&format=json&jscmd=data"
}

func coverURL(f *FakeOL, isbn, query string) string {
	return f.URL + "/b/isbn/" + isbn + "-L.jpg" + query
}

func TestFakeOpenLibraryAnswersFromFixtures(t *testing.T) {
	f := FakeOpenLibrary(t)
	code, body, err := fetch(t, bookURL(f, fox))
	if err != nil || code != 200 {
		t.Fatalf("%d %v", code, err)
	}
	var data map[string]struct {
		Title   string
		Authors []struct{ Name string }
		Cover   struct{ Large string }
	}
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatal(err)
	}
	e := data["ISBN:"+fox]
	if e.Title != "Fantastic Mr Fox" || len(e.Authors) != 1 || e.Authors[0].Name != "Roald Dahl" || e.Cover.Large != coverURL(f, fox, "")[:len(e.Cover.Large)] {
		t.Fatalf("entry %+v", e)
	}

	code, body, _ = fetch(t, f.URL+"/isbn/"+fox+".json")
	if code != 200 || !strings.Contains(string(body), "Three farmers") {
		t.Errorf("edition: %d %s", code, body)
	}
	if code, _, _ = fetch(t, f.URL+"/isbn/9781234567897.json"); code != 404 {
		t.Errorf("unknown edition: %d", code)
	}
	if _, body, _ = fetch(t, bookURL(f, "9781234567897")); strings.TrimSpace(string(body)) != "{}" {
		t.Errorf("unknown book data: %s", body)
	}

	code, body, _ = fetch(t, coverURL(f, fox, "?default=false"))
	img, format, err := image.Decode(bytes.NewReader(body))
	if code != 200 || err != nil || format != "jpeg" || img.Bounds().Dx() < 100 {
		t.Errorf("cover: %d %v %s", code, err, format)
	}
	if got := f.Requests(); len(got) < 5 || !strings.HasPrefix(got[0], "GET /api/books") {
		t.Errorf("requests: %v", got)
	}
}

func TestFakeCoverForABookWithoutOne(t *testing.T) {
	f := FakeOpenLibrary(t)
	const web = "9780064400558"
	if code, _, _ := fetch(t, coverURL(f, web, "?default=false")); code != 404 {
		t.Errorf("default=false: %d, want 404", code)
	}
	_, body, _ := fetch(t, coverURL(f, web, ""))
	if img, _, err := image.Decode(bytes.NewReader(body)); err != nil || img.Bounds().Dx() != 1 {
		t.Errorf("without default=false: want the 1x1 placeholder, got %v", err)
	}
}

func TestFakeModes(t *testing.T) {
	type want struct {
		status int
		check  func(t *testing.T, body []byte)
	}
	isJSON := func(t *testing.T, b []byte) {
		if !json.Valid(b) {
			t.Errorf("not JSON: %q", b)
		}
	}
	for _, tc := range []struct {
		name string
		mode Mode
		url  func(*FakeOL) string
		want want
	}{
		{"forbidden", ModeForbidden, func(f *FakeOL) string { return bookURL(f, fox) }, want{403, nil}},
		{"too many requests", ModeTooManyRequests, func(f *FakeOL) string { return coverURL(f, fox, "") }, want{429, nil}},
		{"server error", ModeServerError, func(f *FakeOL) string { return bookURL(f, fox) }, want{500, nil}},
		{"garbage json", ModeGarbage, func(f *FakeOL) string { return bookURL(f, fox) }, want{200, func(t *testing.T, b []byte) {
			if json.Valid(b) {
				t.Errorf("valid JSON, want garbage: %q", b)
			}
		}}},
		{"ok", ModeOK, func(f *FakeOL) string { return bookURL(f, fox) }, want{200, isJSON}},
		{"tiny cover", ModeTinyCover, func(f *FakeOL) string { return coverURL(f, fox, "?default=false") }, want{200, func(t *testing.T, b []byte) {
			if img, _, err := image.Decode(bytes.NewReader(b)); err != nil || img.Bounds().Dx() != 1 || img.Bounds().Dy() != 1 {
				t.Errorf("want a 1x1 image: %v", err)
			}
		}}},
		{"tiny cover leaves metadata alone", ModeTinyCover, func(f *FakeOL) string { return bookURL(f, fox) }, want{200, isJSON}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := FakeOpenLibrary(t)
			f.SetMode(tc.mode)
			code, body, err := fetch(t, tc.url(f))
			if err != nil || code != tc.want.status {
				t.Fatalf("status %d, err %v; want %d", code, err, tc.want.status)
			}
			if tc.want.check != nil {
				tc.want.check(t, body)
			}
		})
	}
}

func TestFakeRateLimitSaysWhenToRetry(t *testing.T) {
	f := FakeOpenLibrary(t)
	f.SetMode(ModeTooManyRequests)
	resp, err := http.Get(bookURL(f, fox))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}
}

func TestFakeSlowOutlastsTheServersDeadline(t *testing.T) {
	f := FakeOpenLibrary(t)
	if f.SlowDelay() <= 10*time.Second {
		t.Fatalf("SlowDelay %v is not beyond 10 s", f.SlowDelay())
	}
	f.SetMode(ModeSlow)
	f.SetSlowDelay(5 * time.Second) // short enough to test; the default is what the real deadline needs
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", bookURL(f, fox), nil)
	start := time.Now()
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("answered at once in slow mode")
	}
	if time.Since(start) > 2*time.Second {
		t.Error("the client's own timeout was not honoured")
	}
	f.SetSlowDelay(100 * time.Millisecond)
	if code, _, err := fetch(t, bookURL(f, fox)); err != nil || code != 200 {
		t.Errorf("slow mode answers in the end: %d %v", code, err)
	}
}

func TestFakeCloseWithAHeldRequestDoesNotHang(t *testing.T) {
	f := FakeOpenLibrary(t)
	f.SetMode(ModeSlow)
	done := make(chan struct{})
	go func() {
		defer close(done)
		c := &http.Client{Timeout: 20 * time.Second}
		if resp, err := c.Get(bookURL(f, fox)); err == nil {
			resp.Body.Close()
		}
	}()
	for len(f.Requests()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	closed := make(chan struct{})
	go func() { f.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close hangs on a held request")
	}
	<-done
}

func TestFakeGoneDropsTheConnectionAndCloseRefusesIt(t *testing.T) {
	f := FakeOpenLibrary(t)
	f.SetMode(ModeGone)
	if _, _, err := fetch(t, bookURL(f, fox)); err == nil {
		t.Error("gone mode answered")
	}
	f.SetMode(ModeOK)
	if code, _, err := fetch(t, bookURL(f, fox)); err != nil || code != 200 {
		t.Fatalf("back to OK: %d %v", code, err)
	}
	f.Close()
	if _, _, err := fetch(t, bookURL(f, fox)); err == nil {
		t.Error("answered after Close")
	}
	f.Close()
}

func TestFakeAddBook(t *testing.T) {
	f := FakeOpenLibrary(t)
	f.AddBook(Book{ISBN13: "9781234567897", Title: "Mine", Authors: []string{"Me"}})
	_, body, _ := fetch(t, bookURL(f, "9781234567897"))
	if !strings.Contains(string(body), `"Mine"`) {
		t.Errorf("%s", body)
	}
}

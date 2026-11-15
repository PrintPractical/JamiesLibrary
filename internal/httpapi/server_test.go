package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"jamieslibrary/internal/apperr"
)

func testHandler(t *testing.T, buf *bytes.Buffer) http.Handler {
	t.Helper()
	var out = buf
	if out == nil {
		out = &bytes.Buffer{}
	}
	static := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html>app</html>")
	})
	return NewHandler(Options{Version: "1.2.3", Logger: slog.New(slog.NewTextHandler(out, nil)), Static: static})
}

func do(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type errResp struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Field     string `json:"field"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func TestHealth(t *testing.T) {
	rec := do(testHandler(t, nil), "GET", "/api/health", "")
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("status %d, type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "ok" || got["version"] != "1.2.3" {
		t.Fatalf("got %v", got)
	}
}

func TestUnknownAPIPathIsJSON404NotTheApp(t *testing.T) {
	for _, p := range []string{"/api/nope", "/api/", "/api/health/extra", "/media/covers/x.jpg"} {
		rec := do(testHandler(t, nil), "GET", p, "")
		if rec.Code != 404 {
			t.Errorf("%s: status %d", p, rec.Code)
		}
		var e errResp
		if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.Error.Code != "not_found" {
			t.Errorf("%s: body %q", p, rec.Body.String())
		}
	}
}

func TestOtherPathsGoToTheApp(t *testing.T) {
	rec := do(testHandler(t, nil), "GET", "/books/7", "")
	if rec.Code != 200 || rec.Body.String() != "<html>app</html>" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
}

func TestErrorBodyForEachCode(t *testing.T) {
	codes := map[apperr.Code]int{
		apperr.BadRequest: 400, apperr.Unauthorized: 401, apperr.Forbidden: 403, apperr.NotFound: 404,
		apperr.Conflict: 409, apperr.TooLarge: 413, apperr.Invalid: 422, apperr.Throttled: 429,
		apperr.Unavailable: 503,
	}
	for code, status := range codes {
		r := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		WriteError(rec, r, fmt.Errorf("wrapped: %w", apperr.OnField(code, "title", "Fix the title.")))
		var e errResp
		if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		if rec.Code != status || e.Error.Code != string(code) || e.Error.Message != "Fix the title." || e.Error.Field != "title" || e.Error.RequestID != "" {
			t.Errorf("%s: status %d body %s", code, rec.Code, rec.Body.String())
		}
	}
}

func TestFieldOmittedWhenEmpty(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest("GET", "/", nil), apperr.New(apperr.Conflict, "On loan."))
	if strings.Contains(rec.Body.String(), "field") {
		t.Fatalf("field present: %s", rec.Body.String())
	}
}

func TestUnexpectedErrorHidesInternalsAndCarriesRequestID(t *testing.T) {
	var logs bytes.Buffer
	h := withRequest(slog.New(slog.NewTextHandler(&logs, nil)), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, errors.New("secret database detail"))
	}))
	rec := do(h, "GET", "/x", "")
	var e errResp
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	id := rec.Header().Get("X-Request-ID")
	if rec.Code != 500 || e.Error.Code != "internal" || id == "" || e.Error.RequestID != id {
		t.Fatalf("status %d id %q body %s", rec.Code, id, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatal("internal text leaked to client")
	}
	if !strings.Contains(logs.String(), "secret database detail") || !strings.Contains(logs.String(), id) {
		t.Fatalf("error not logged with id: %s", logs.String())
	}
}

func TestPanicBecomes500(t *testing.T) {
	h := withRequest(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := do(h, "GET", "/x", "")
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestRequestIDsDifferAndLogLineHasNoBodyOrQuery(t *testing.T) {
	var logs bytes.Buffer
	h := testHandler(t, &logs)
	a := do(h, "POST", "/api/health?token=sekret", `{"password":"hunter2"}`)
	b := do(h, "GET", "/api/health", "")
	if a.Header().Get("X-Request-ID") == "" || a.Header().Get("X-Request-ID") == b.Header().Get("X-Request-ID") {
		t.Fatal("request IDs missing or repeated")
	}
	out := logs.String()
	if strings.Contains(out, "hunter2") || strings.Contains(out, "sekret") {
		t.Fatalf("log leaked request content: %s", out)
	}
	if !strings.Contains(out, "path=/api/health") || !strings.Contains(out, "status=") {
		t.Fatalf("log line incomplete: %s", out)
	}
}

type payload struct {
	Name string `json:"name"`
}

func decode(body string) (payload, error) {
	var p payload
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	return p, DecodeJSON(w, r, &p)
}

func TestDecodeJSON(t *testing.T) {
	if p, err := decode(`{"name":"x"}`); err != nil || p.Name != "x" {
		t.Fatalf("good body: %v %v", p, err)
	}
	bad := map[string]apperr.Code{
		``:                          apperr.BadRequest,
		`{"name":`:                  apperr.BadRequest,
		`nope`:                      apperr.BadRequest,
		`{"name":"x","extra":1}`:    apperr.BadRequest,
		`{"name":5}`:                apperr.BadRequest,
		`{"name":"x"} {"name":"y"}`: apperr.BadRequest,
		`{"name":"x"} junk`:         apperr.BadRequest,
		`{"name":"` + strings.Repeat("a", MaxJSONBody) + `"}`: apperr.TooLarge,
	}
	for body, code := range bad {
		_, err := decode(body)
		e, ok := apperr.As(err)
		if !ok || e.Code != code {
			n := len(body)
			if n > 30 {
				n = 30
			}
			t.Errorf("body %q: got %v, want code %s", body[:n], err, code)
		}
	}
}

func TestServeShutsDownGracefully(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ln, testHandler(t, nil), slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	}()
	resp, err := http.Get("http://" + ln.Addr().String() + "/api/health")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("health: %v %v", resp, err)
	}
	resp.Body.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}

func TestBodyLimitIsExactly64KiB(t *testing.T) {
	if MaxJSONBody != 64<<10 {
		t.Fatalf("MaxJSONBody = %d", MaxJSONBody)
	}
	wrap := `{"name":""}`
	under := `{"name":"` + strings.Repeat("a", MaxJSONBody-len(wrap)) + `"}` // exactly the limit
	if len(under) != MaxJSONBody {
		t.Fatalf("test body is %d bytes", len(under))
	}
	if _, err := decode(under); err != nil {
		t.Fatalf("body at the limit refused: %v", err)
	}
	if _, err := decode(under + " "); err == nil {
		// trailing space is one byte over the limit
		t.Fatal("body one byte over the limit accepted")
	} else if e, ok := apperr.As(err); !ok || e.Code != apperr.TooLarge {
		t.Fatalf("got %v, want too_large", err)
	}
	if _, err := decode(`{"name":"` + strings.Repeat("a", 200<<10) + `"}`); err == nil {
		t.Fatal("200 KiB body accepted")
	}
}

func TestOversizedHeadersRefusedByRealListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Serve(ctx, ln, testHandler(t, nil), slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	get := func(size int) int {
		req, _ := http.NewRequest("GET", "http://"+ln.Addr().String()+"/api/health", nil)
		req.Header.Set("X-Pad", strings.Repeat("a", size))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := get(32 << 10); got != 200 {
		t.Fatalf("32 KiB of headers: status %d", got)
	}
	if got := get(100 << 10); got != 431 {
		t.Fatalf("70 KiB of headers: status %d, want 431", got)
	}
}

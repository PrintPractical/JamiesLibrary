package httpapi

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/media"
	"jamieslibrary/internal/perm"
)

func smallJPEG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func (s *site) storeCover(t *testing.T) string {
	t.Helper()
	name, err := s.img.Store(bytes.NewReader(smallJPEG(t)), media.CoverLimit, media.Cover)
	if err != nil {
		t.Fatal(err)
	}
	return name
}

// ACCESS-1 / ACCESS-4: a cover is for signed-in users with catalog.browse, or for anyone while the public catalog is on.
func TestCoverAccess(t *testing.T) {
	s := newSite(t)
	name := s.storeCover(t)
	s.addUser("ann", goodPassword, perm.Patron)
	cookie := s.signIn("ann", goodPassword)
	path := "/media/" + name

	rec := s.req("GET", path, "", "")
	if rec.Code != 401 {
		t.Errorf("anonymous, public catalog off = %d, want 401", rec.Code)
	}
	if rec = s.req("GET", path, cookie, ""); rec.Code != 200 {
		t.Fatalf("patron = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("Content-Type %q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff header %q", got)
	}
	if _, err := jpeg.Decode(rec.Body); err != nil {
		t.Errorf("body is not the image: %v", err)
	}

	if err := s.set.SetPublicCatalog(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if rec = s.req("GET", path, "", ""); rec.Code != 200 {
		t.Errorf("anonymous, public catalog on = %d, want 200", rec.Code)
	}
	if err := s.set.SetPublicCatalog(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if rec = s.req("GET", path, "", ""); rec.Code != 401 {
		t.Errorf("anonymous after turning it off = %d, want 401", rec.Code)
	}
}

// A signed-in caller without catalog.browse is refused (403).
func TestCoverNeedsCatalogBrowse(t *testing.T) {
	s := newSite(t)
	name := s.storeCover(t)
	u := s.addUser("ann", goodPassword, perm.Patron)
	cookie := s.signIn("ann", goodPassword)
	if _, err := s.st.DB().Exec(`DELETE FROM user_permissions WHERE user_id = ? AND permission = ?`, u.ID, string(perm.CatalogBrowse)); err != nil {
		t.Fatal(err)
	}
	if rec := s.req("GET", "/media/"+name, cookie, ""); rec.Code != 403 {
		t.Errorf("without catalog.browse = %d, want 403", rec.Code)
	}
}

// ADMIN-1: the logo is shown to anyone (the sign-in page needs it), a PNG as a PNG.
func TestLogoIsPublic(t *testing.T) {
	s := newSite(t)
	img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	img.Set(1, 1, color.NRGBA{255, 0, 0, 255})
	var b bytes.Buffer
	png.Encode(&b, img)
	logo, err := s.img.Store(&b, media.LogoLimit, media.Logo)
	if err != nil {
		t.Fatal(err)
	}
	cover := s.storeCover(t)
	if err := s.set.SetLogo(context.Background(), logo); err != nil {
		t.Fatal(err)
	}
	rec := s.req("GET", "/media/"+logo, "", "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("anonymous logo = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec = s.req("GET", "/media/"+cover, "", ""); rec.Code != 401 {
		t.Errorf("a cover beside the logo = %d, want 401", rec.Code)
	}
}

func TestMediaPathsThatAreNotGeneratedNamesAre404(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	cookie := s.signIn("ann", goodPassword)
	good := strings.Repeat("ab", 16)
	for _, p := range []string{
		"/media/..%2Flibrary.db", "/media/%2e%2e%2f%2e%2e%2fetc%2fpasswd",
		"/media/..%5Clibrary.db", "/media/library.db", "/media/" + good + ".svg",
		"/media/", "/media/a/b.jpg", "/media/" + good + ".jpg%00.png",
	} {
		for _, c := range []string{"", cookie} {
			if rec := s.req("GET", p, c, ""); rec.Code != 404 {
				t.Errorf("GET %s (cookie %v) = %d, want 404", p, c != "", rec.Code)
			}
		}
	}
}

// The mux cleans "/media/../x" into a redirect to "/x"; it never reaches the images.
func TestMediaDotDotPathNeverServesAFile(t *testing.T) {
	s := newSite(t)
	rec := s.req("GET", "/media/../library.db", "", "")
	if rec.Code != 307 || rec.Header().Get("Location") != "/library.db" {
		t.Errorf("= %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

// A well-formed name with no file is 404 for a caller allowed to look, and does not tell anyone else it is missing.
func TestMissingImage(t *testing.T) {
	s := newSite(t)
	s.addUser("ann", goodPassword, perm.Patron)
	cookie := s.signIn("ann", goodPassword)
	p := "/media/" + strings.Repeat("ab", 16) + ".jpg"
	if rec := s.req("GET", p, cookie, ""); rec.Code != 404 {
		t.Errorf("signed in = %d, want 404", rec.Code)
	}
	if rec := s.req("GET", p, "", ""); rec.Code != 401 {
		t.Errorf("anonymous = %d, want 401", rec.Code)
	}
}

func TestMediaRouteIsDeclared(t *testing.T) {
	s := newSite(t)
	rt, err := buildRouter(Options{Settings: s.set, Media: s.img})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rt.Routes() {
		if r.Pattern == "GET /media/{name}" {
			if r.Access.String() != "decided by the handler" {
				t.Errorf("access %q", r.Access)
			}
			return
		}
	}
	t.Error("GET /media/{name} is not registered")
}

func uploadReq(t *testing.T, field, file string, content []byte) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile(field, file)
	fw.Write(content)
	mw.Close()
	r := httptest.NewRequest("POST", "/x", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return httptest.NewRecorder(), r
}

func TestReadFilePart(t *testing.T) {
	content := bytes.Repeat([]byte{7}, 1000)
	w, r := uploadReq(t, "cover", "c.jpg", content)
	got, err := ReadFilePart(w, r, "cover", 1000)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("exact limit: %d bytes, %v", len(got), err)
	}
	// One byte over is refused.
	w, r = uploadReq(t, "cover", "c.jpg", append(content, 7))
	_, err = ReadFilePart(w, r, "cover", 1000)
	if e, ok := apperr.As(err); !ok || e.Code != apperr.TooLarge || e.Field != "cover" {
		t.Errorf("one over: %v", err)
	}
	// A body far beyond limit + overhead is cut off, not read whole.
	w, r = uploadReq(t, "cover", "c.jpg", bytes.Repeat([]byte{7}, 200<<10))
	_, err = ReadFilePart(w, r, "cover", 1000)
	if e, ok := apperr.As(err); !ok || e.Code != apperr.TooLarge {
		t.Errorf("huge body: %v", err)
	}
	// No such part, and not multipart.
	w, r = uploadReq(t, "other", "c.jpg", content)
	if _, err = ReadFilePart(w, r, "cover", 1000); err == nil {
		t.Error("missing part accepted")
	}
	r = httptest.NewRequest("POST", "/x", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	if _, err = ReadFilePart(httptest.NewRecorder(), r, "cover", 1000); err == nil {
		t.Error("JSON body accepted")
	}
}

package httpapi

import (
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/media"
	"jamieslibrary/internal/perm"
)

// uploadOverhead is what a multipart body may add to the file itself (sec §9).
const uploadOverhead = 64 << 10

// ReadFilePart reads the file part called field from a multipart upload of at
// most limit bytes: the body is capped at limit plus the multipart overhead
// before it is parsed, and the part at limit. The caller passes the bytes to
// media.Images.Store with the same limit.
func ReadFilePart(w http.ResponseWriter, r *http.Request, field string, limit int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, limit+uploadOverhead)
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, apperr.OnField(apperr.BadRequest, field, "Send the image as a form upload.")
	}
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, apperr.OnField(apperr.Invalid, field, "Choose an image file to upload.")
		}
		if err != nil {
			return nil, uploadErr(field, err)
		}
		if p.FormName() != field || p.FileName() == "" {
			continue
		}
		return readPart(p, field, limit)
	}
}

func readPart(p *multipart.Part, field string, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(p, limit+1))
	if err != nil {
		return nil, uploadErr(field, err)
	}
	if int64(len(data)) > limit {
		return nil, tooLarge(field, limit)
	}
	return data, nil
}

func uploadErr(field string, err error) error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return tooLarge(field, mbe.Limit-uploadOverhead)
	}
	return apperr.OnField(apperr.BadRequest, field, "The upload was cut off or malformed. Try again.")
}

func tooLarge(field string, limit int64) error {
	return apperr.OnField(apperr.TooLarge, field, "That image is larger than "+strconv.FormatInt(limit>>20, 10)+" MB. Choose a smaller one.")
}

// registerMedia serves GET /media/{name}: the logo to anyone, every other image
// to a signed-in caller who may browse the catalog, or to anyone while the
// public catalog is on (sec §7). The route is Mixed because the answer depends
// on the image, which the handler looks up.
func registerMedia(rt *Router, o Options) {
	rt.HandleFunc("GET /media/{name}", Mixed(), func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !media.ValidName(name) {
			notFound(w, r)
			return
		}
		if name != o.Settings.Get().LogoFile {
			if err := rt.mediaAccess(r); err != nil {
				WriteError(w, r, err)
				return
			}
		}
		f, err := o.Media.Open(name)
		if errors.Is(err, media.ErrNotFound) {
			notFound(w, r)
			return
		}
		if err != nil {
			WriteError(w, r, err)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			WriteError(w, r, err)
			return
		}
		h := w.Header()
		h.Set("Content-Type", media.ContentType(name))
		h.Set("X-Content-Type-Options", "nosniff")
		// A name is random and never reused, so the bytes behind it never change.
		h.Set("Cache-Control", "private, max-age=3600")
		http.ServeContent(w, r, name, st.ModTime(), f)
	})
}

// mediaAccess is the rule for an image that isn't the logo.
func (r *Router) mediaAccess(req *http.Request) error {
	if r.publicOn() {
		return nil
	}
	c := auth.Caller(req.Context())
	switch {
	case c == nil:
		return apperr.New(apperr.Unauthorized, "Sign in to continue.")
	case c.MustChangePassword:
		return apperr.New(apperr.Forbidden, "Choose a new password before you do anything else.")
	case !c.Has(perm.CatalogBrowse):
		return apperr.New(apperr.Forbidden, "You don't have permission to do that.")
	}
	return nil
}

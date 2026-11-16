package httpapi

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"

	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/catalog"
	"jamieslibrary/internal/media"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/reviews"
)

// registerCatalogRoutes registers search, book pages, books and locations
// (CAT-1, CAT-2, CAT-4, CAT-5, CAT-8). The rules live in catalog.
func registerCatalogRoutes(rt *Router, o Options) {
	c := &catalogRoutes{cat: o.Catalog, images: o.Media, meta: o.Metadata, reviews: o.Reviews}
	rt.HandleFunc("GET /api/books", Require(perm.CatalogBrowse), c.search)
	rt.HandleFunc("GET /api/books/{id}", Require(perm.CatalogBrowse), c.book)
	rt.HandleFunc("GET /api/locations", Require(perm.CatalogBrowse), c.locations)

	rt.HandleFunc("POST /api/books", Require(perm.CatalogManage).Multipart(), c.createBook)
	rt.HandleFunc("PUT /api/books/{id}", Require(perm.CatalogManage).Multipart(), c.updateBook)
	rt.HandleFunc("DELETE /api/books/{id}", Require(perm.CatalogManage), c.removeBook)
	rt.HandleFunc("POST /api/locations", Require(perm.CatalogManage), c.createLocation)
	rt.HandleFunc("PUT /api/locations/{id}", Require(perm.CatalogManage), c.renameLocation)
	rt.HandleFunc("DELETE /api/locations/{id}", Require(perm.CatalogManage), c.deleteLocation)
}

type catalogRoutes struct {
	cat     *catalog.Service
	images  *media.Images
	reviews *reviews.Service // nil: book pages carry no ratings
	meta    interface {
		TakeCover(token string, actor int64) (string, error)
	}
}

// bookSummary is a book in a list of results.
type bookSummary struct {
	ID        int64    `json:"id"`
	Title     string   `json:"title"`
	Authors   []string `json:"authors"`
	ISBN      string   `json:"isbn"`
	CoverURL  string   `json:"cover_url"`
	CopyCount int      `json:"copy_count"`
	Available int      `json:"available"`
}

// bookCopyBody is one copy on a book's page: where it is and whether it can be
// borrowed, or when it is due back. It never says who has it.
type bookCopyBody struct {
	ID        int64  `json:"id"`
	Number    int    `json:"number"`
	Condition string `json:"condition"`
	Location  string `json:"location"`
	Available bool   `json:"available"`
	DueDate   string `json:"due_date,omitempty"`
}

// bookDetail is a book's page.
type bookDetail struct {
	bookSummary
	Description string         `json:"description"`
	Copies      []bookCopyBody `json:"copies"`
	// Ratings and reviews (CAT-6): the page of a signed-in caller only.
	RatingAverage float64      `json:"rating_average"`
	RatingCount   int          `json:"rating_count"`
	MyReview      *reviewBody  `json:"my_review"`
	Reviews       []reviewBody `json:"reviews"`
	ReviewsTotal  int          `json:"reviews_total"`
}

type searchBody struct {
	Books    []bookSummary `json:"books"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}

type locationBody struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Copies int    `json:"copies"`
}

func coverURL(name string) string {
	if name == "" {
		return ""
	}
	return "/media/" + name
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func (c *catalogRoutes) summary(r *http.Request, b catalog.Book) (bookSummary, error) {
	copies, avail, err := c.cat.Counts(r.Context(), b.ID)
	if err != nil {
		return bookSummary{}, err
	}
	return bookSummary{ID: b.ID, Title: b.Title, Authors: b.Authors, ISBN: b.ISBN13, CoverURL: coverURL(b.Cover),
		CopyCount: copies, Available: avail}, nil
}

func (c *catalogRoutes) detail(r *http.Request, b catalog.Book) (bookDetail, error) {
	ctx := r.Context()
	sum, err := c.summary(r, b)
	if err != nil {
		return bookDetail{}, err
	}
	copies, err := c.cat.CopiesOf(ctx, b.ID)
	if err != nil {
		return bookDetail{}, err
	}
	dues, err := c.cat.DueDates(ctx, b.ID)
	if err != nil {
		return bookDetail{}, err
	}
	d := bookDetail{bookSummary: sum, Description: b.Description, Copies: make([]bookCopyBody, 0, len(copies))}
	for _, cp := range copies {
		d.Copies = append(d.Copies, bookCopyBody{ID: cp.ID, Number: cp.Number, Condition: cp.Condition,
			Location: cp.LocationName, Available: !cp.OnLoan, DueDate: dues[cp.ID]})
	}
	if c.reviews != nil {
		if err := fillReviews(r, c.reviews, &d); err != nil {
			return bookDetail{}, err
		}
	}
	return d, nil
}

// intQuery reads an optional whole-number query parameter.
func intQuery(r *http.Request, name string) (int64, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, apperr.OnField(apperr.Invalid, name, "That is not a valid "+name+".")
	}
	return n, nil
}

func (c *catalogRoutes) search(w http.ResponseWriter, r *http.Request) {
	loc, err := intQuery(r, "location")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	page, err := intQuery(r, "page")
	if err != nil || page > 1<<31 {
		WriteError(w, r, apperr.OnField(apperr.Invalid, "page", "That is not a valid page."))
		return
	}
	res, err := c.cat.Search(r.Context(), r.URL.Query().Get("q"), loc, int(page))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	out := searchBody{Books: make([]bookSummary, 0, len(res.Books)), Total: res.Total, Page: res.Page, PageSize: res.PageSize}
	for _, b := range res.Books {
		s, err := c.summary(r, b)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		out.Books = append(out.Books, s)
	}
	WriteJSON(w, http.StatusOK, out)
}

func (c *catalogRoutes) book(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		notFound(w, r)
		return
	}
	b, err := c.cat.GetBook(r.Context(), id)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	d, err := c.detail(r, b)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, d)
}

func (c *catalogRoutes) locations(w http.ResponseWriter, r *http.Request) {
	ls, err := c.cat.Locations(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	out := make([]locationBody, 0, len(ls))
	for _, l := range ls {
		out = append(out, locationBody{ID: l.ID, Name: l.Name, Copies: l.Copies})
	}
	WriteJSON(w, http.StatusOK, out)
}

// bookForm is what a book is saved from: JSON, or a multipart form with the
// same fields and a "cover" file.
type bookForm struct {
	Title       string   `json:"title"`
	Authors     []string `json:"authors"`
	ISBN        string   `json:"isbn"`
	Description string   `json:"description"`
	CoverToken  string   `json:"cover_token"`
	cover       []byte   // an uploaded cover's bytes
}

// maxFormField bounds each text field of a multipart book form, like a JSON body.
const (
	maxFormField = MaxJSONBody
	maxFormParts = 100
)

func readBookForm(w http.ResponseWriter, r *http.Request) (bookForm, error) {
	var f bookForm
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "multipart/form-data" {
		return f, DecodeJSON(w, r, &f)
	}
	r.Body = http.MaxBytesReader(w, r.Body, media.CoverLimit+uploadOverhead)
	mr, err := r.MultipartReader()
	if err != nil {
		return f, apperr.New(apperr.BadRequest, "The form could not be read.")
	}
	for n := 0; ; n++ {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return f, nil
		}
		if err != nil {
			return f, uploadErr("cover", err)
		}
		if n >= maxFormParts {
			return f, apperr.New(apperr.BadRequest, "The form has too many parts.")
		}
		if err := f.part(p); err != nil {
			return f, err
		}
	}
}

func (f *bookForm) part(p *multipart.Part) error {
	if p.FormName() == "cover" && p.FileName() != "" {
		if f.cover != nil {
			return apperr.OnField(apperr.BadRequest, "cover", "Send one cover image.")
		}
		data, err := readPart(p, "cover", media.CoverLimit)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			return apperr.OnField(apperr.Invalid, "cover", "That image file is empty. Choose a different file.")
		}
		f.cover = data
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(p, maxFormField+1))
	if err != nil {
		return uploadErr(p.FormName(), err)
	}
	if len(data) > maxFormField {
		return apperr.OnField(apperr.TooLarge, p.FormName(), "That field is too long.")
	}
	switch p.FormName() {
	case "title":
		f.Title = string(data)
	case "authors":
		f.Authors = append(f.Authors, string(data))
	case "isbn":
		f.ISBN = string(data)
	case "description":
		f.Description = string(data)
	case "cover_token":
		f.CoverToken = string(data)
	default:
		return apperr.New(apperr.BadRequest, "The form has a field that isn't allowed.")
	}
	return nil
}

// resolveCover turns the form's cover (an upload or a looked-up token) into a
// stored image's name, "" when there is none. A name that was made is the
// caller's to delete if the book is not saved.
func (c *catalogRoutes) resolveCover(r *http.Request, f bookForm) (string, error) {
	switch {
	case f.cover != nil && f.CoverToken != "":
		return "", apperr.OnField(apperr.Invalid, "cover", "Send an uploaded cover or a fetched one, not both.")
	case f.cover != nil:
		return c.images.Store(r.Context(), bytes.NewReader(f.cover), media.CoverLimit, media.Cover)
	case f.CoverToken != "":
		if c.meta == nil {
			return "", apperr.OnField(apperr.Invalid, "cover_token", "There is no fetched cover under that token.")
		}
		name, err := c.meta.TakeCover(f.CoverToken, auth.Caller(r.Context()).UserID)
		if errors.Is(err, media.ErrNotFound) {
			return "", apperr.OnField(apperr.Invalid, "cover_token", "The fetched cover has expired. Look the book up again or upload a cover.")
		}
		return name, err
	}
	return "", nil
}

// saveBook checks the text, makes the cover, then runs save with the input. A
// cover made for a book that was not saved is deleted, so a refusal leaves nothing behind.
func (c *catalogRoutes) saveBook(w http.ResponseWriter, r *http.Request, save func(catalog.BookInput) (catalog.Book, error)) {
	f, err := readBookForm(w, r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	in := catalog.BookInput{Title: f.Title, Authors: f.Authors, ISBN: f.ISBN, Description: f.Description}
	if err := in.Validate(); err != nil {
		WriteError(w, r, err)
		return
	}
	if in.Cover, err = c.resolveCover(r, f); err != nil {
		WriteError(w, r, err)
		return
	}
	b, err := save(in)
	if err != nil {
		if in.Cover != "" {
			_ = c.images.Delete(in.Cover)
		}
		var dup *catalog.DuplicateISBN
		if errors.As(err, &dup) {
			e, _ := apperr.As(err)
			WriteJSON(w, http.StatusConflict, struct {
				Error  errorDetail `json:"error"`
				BookID int64       `json:"book_id"`
			}{errorDetail{Code: e.Code, Message: e.Message, Field: e.Field}, dup.BookID})
			return
		}
		WriteError(w, r, err)
		return
	}
	d, err := c.detail(r, b)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	WriteJSON(w, status, d)
}

func (c *catalogRoutes) createBook(w http.ResponseWriter, r *http.Request) {
	actor := auth.Caller(r.Context()).UserID
	c.saveBook(w, r, func(in catalog.BookInput) (catalog.Book, error) {
		return c.cat.CreateBook(r.Context(), actor, in)
	})
}

func (c *catalogRoutes) updateBook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		notFound(w, r)
		return
	}
	actor := auth.Caller(r.Context()).UserID
	old, err := c.cat.GetBook(r.Context(), id)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	c.saveBook(w, r, func(in catalog.BookInput) (catalog.Book, error) {
		b, err := c.cat.UpdateBook(r.Context(), actor, id, in)
		if err == nil && in.Cover != "" && old.Cover != "" {
			_ = c.images.Delete(old.Cover) // the replaced image is no longer used
		}
		return b, err
	})
}

func (c *catalogRoutes) removeBook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		notFound(w, r)
		return
	}
	if err := c.cat.RemoveBook(r.Context(), auth.Caller(r.Context()).UserID, id); err != nil {
		WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type nameBody struct {
	Name string `json:"name"`
}

func (c *catalogRoutes) createLocation(w http.ResponseWriter, r *http.Request) {
	var in nameBody
	if err := DecodeJSON(w, r, &in); err != nil {
		WriteError(w, r, err)
		return
	}
	l, err := c.cat.CreateLocation(r.Context(), auth.Caller(r.Context()).UserID, in.Name)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, locationBody{ID: l.ID, Name: l.Name})
}

func (c *catalogRoutes) renameLocation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		notFound(w, r)
		return
	}
	var in nameBody
	if err := DecodeJSON(w, r, &in); err != nil {
		WriteError(w, r, err)
		return
	}
	l, err := c.cat.RenameLocation(r.Context(), auth.Caller(r.Context()).UserID, id, in.Name)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, locationBody{ID: l.ID, Name: l.Name})
}

// deleteLocation deletes an empty location, or with ?move_to=ID moves its
// copies there first.
func (c *catalogRoutes) deleteLocation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		notFound(w, r)
		return
	}
	to, err := intQuery(r, "move_to")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	actor := auth.Caller(r.Context()).UserID
	if r.URL.Query().Has("move_to") {
		if to == 0 {
			WriteError(w, r, apperr.OnField(apperr.Invalid, "move_to", "Choose the location to move the copies to."))
			return
		}
		err = c.cat.MoveAndDeleteLocation(r.Context(), actor, id, to)
	} else {
		err = c.cat.DeleteLocation(r.Context(), actor, id)
	}
	if err != nil {
		WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

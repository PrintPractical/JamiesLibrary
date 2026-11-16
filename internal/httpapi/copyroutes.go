package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"jamieslibrary/internal/activity"
	"jamieslibrary/internal/apperr"
	"jamieslibrary/internal/auth"
	"jamieslibrary/internal/barcode"
	"jamieslibrary/internal/catalog"
	"jamieslibrary/internal/perm"
	"jamieslibrary/internal/settings"
	"jamieslibrary/internal/store"
)

// registerCopyRoutes registers copies, their labels and the copy-ID mode
// (CAT-3, CAT-5, CAT-7, CAT-9). The rules live in catalog and the drawing in
// barcode.
func registerCopyRoutes(rt *Router, o Options) {
	c := &copyRoutes{cat: o.Catalog}
	rt.HandleFunc("POST /api/books/{id}/copies", Require(perm.CatalogManage), c.add)
	rt.HandleFunc("PUT /api/copies/{id}", Require(perm.CatalogManage), c.update)
	rt.HandleFunc("DELETE /api/copies/{id}", Require(perm.CatalogManage), c.remove)
	rt.HandleFunc("POST /api/catalog/labels", Require(perm.CatalogManage), c.labels)
	rt.HandleFunc("PUT /api/admin/copy-id-mode", Require(perm.SettingsManage), c.setMode)
}

type copyRoutes struct {
	cat *catalog.Service
}

// staffCopyBody is a copy as staff see it: with its barcode, when it has one.
type staffCopyBody struct {
	ID         int64  `json:"id"`
	BookID     int64  `json:"book_id"`
	Number     int    `json:"number"`
	Condition  string `json:"condition"`
	LocationID int64  `json:"location_id"`
	Location   string `json:"location"`
	Barcode    string `json:"barcode"`
	Available  bool   `json:"available"`
}

func staffCopyOf(c catalog.Copy) staffCopyBody {
	return staffCopyBody{ID: c.ID, BookID: c.BookID, Number: c.Number, Condition: c.Condition, LocationID: c.LocationID,
		Location: c.LocationName, Barcode: c.Code, Available: !c.OnLoan}
}

// copyForm is what a copy is saved from. A location is named by ID or, to make
// one in the same request, by new_location.
type copyForm struct {
	LocationID  int64  `json:"location_id"`
	NewLocation string `json:"new_location"`
	Condition   string `json:"condition"`
	Sticker     string `json:"sticker"`
}

func (f copyForm) input() catalog.CopyInput {
	return catalog.CopyInput{LocationID: f.LocationID, NewLocation: f.NewLocation, Condition: f.Condition, Sticker: f.Sticker}
}

func (c *copyRoutes) add(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		notFound(w, r)
		return
	}
	var f copyForm
	if err := DecodeJSON(w, r, &f); err != nil {
		WriteError(w, r, err)
		return
	}
	cp, err := c.cat.AddCopy(r.Context(), auth.Caller(r.Context()).UserID, id, f.input())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, staffCopyOf(cp))
}

func (c *copyRoutes) update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		notFound(w, r)
		return
	}
	var f copyForm
	if err := DecodeJSON(w, r, &f); err != nil {
		WriteError(w, r, err)
		return
	}
	cp, err := c.cat.UpdateCopy(r.Context(), auth.Caller(r.Context()).UserID, id, f.input())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, staffCopyOf(cp))
}

func (c *copyRoutes) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		notFound(w, r)
		return
	}
	if err := c.cat.RemoveCopy(r.Context(), auth.Caller(r.Context()).UserID, id); err != nil {
		WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type labelsForm struct {
	CopyIDs []int64 `json:"copy_ids"`
	Layout  string  `json:"layout"`
}

// labels prints the selected copies' labels as a PDF, for the library's
// current copy-ID mode (CAT-7).
func (c *copyRoutes) labels(w http.ResponseWriter, r *http.Request) {
	var f labelsForm
	if err := DecodeJSON(w, r, &f); err != nil {
		WriteError(w, r, err)
		return
	}
	layout, ok := barcode.LayoutByName(f.Layout)
	if !ok {
		WriteError(w, r, apperr.OnField(apperr.Invalid, "layout", "Choose a label layout: avery5160, averyl7160 or single."))
		return
	}
	switch n := len(f.CopyIDs); {
	case n == 0:
		WriteError(w, r, apperr.OnField(apperr.Invalid, "copy_ids", "Choose at least one copy to print labels for."))
		return
	case n > barcode.MaxLabels:
		WriteError(w, r, apperr.OnField(apperr.Invalid, "copy_ids", fmt.Sprintf("At most %d labels can be printed at once.", barcode.MaxLabels)))
		return
	}
	copies, mode, err := c.cat.LabelCopies(r.Context(), f.CopyIDs)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	labels := make([]barcode.Label, 0, len(copies))
	for _, cp := range copies {
		lb, err := labelOf(cp, mode)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		labels = append(labels, lb)
	}
	pdf, err := barcode.Labels(layout, labels)
	if err != nil {
		if errors.Is(err, barcode.ErrInvalid) {
			WriteError(w, r, apperr.OnField(apperr.Invalid, "copy_ids", strings.TrimPrefix(err.Error(), barcode.ErrInvalid.Error()+": ")))
			return
		}
		WriteError(w, r, err)
		return
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, pdf); err != nil {
		WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="labels.pdf"`)
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	_, _ = w.Write(buf.Bytes())
}

// labelOf is what a copy's label shows. In own-barcode mode that is its
// barcode and its book's title. In by-ISBN mode it is the book's ISBN barcode,
// title, copy number and location; a book without an ISBN has no ISBN barcode
// to show, so it uses the copy's own.
func labelOf(cp catalog.LabelCopy, mode string) (barcode.Label, error) {
	if mode == settings.CopyIDOwn {
		if cp.Code == "" {
			return barcode.Label{}, noBarcode(cp)
		}
		return barcode.Label{Code: cp.Code, CodeKind: barcode.Code128, Lines: []string{cp.Title}}, nil
	}
	lines := []string{cp.Title, fmt.Sprintf("Copy %d", cp.Number), cp.LocationName}
	if cp.ISBN != "" {
		return barcode.Label{Code: cp.ISBN, CodeKind: barcode.EAN13, Lines: lines}, nil
	}
	if cp.Code == "" {
		return barcode.Label{}, noBarcode(cp)
	}
	return barcode.Label{Code: cp.Code, CodeKind: barcode.Code128, Lines: lines}, nil
}

func noBarcode(cp catalog.LabelCopy) error {
	return apperr.OnField(apperr.Conflict, "copy_ids", fmt.Sprintf("Copy %d of “%s” has no barcode to print. Give it a sticker or switch to own-barcode mode.", cp.Number, cp.Title))
}

// setMode switches how copies are identified (CAT-9) and writes the activity
// entry in the same transaction.
func (c *copyRoutes) setMode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode string `json:"mode"`
	}
	if err := DecodeJSON(w, r, &in); err != nil {
		WriteError(w, r, err)
		return
	}
	actor := auth.Caller(r.Context()).UserID
	audit := func(ctx context.Context, tx store.Tx, details map[string]any) error {
		return activity.Record(ctx, tx, activity.ByUser(actor), activity.SettingsChanged, "settings", details)
	}
	if err := c.cat.SetCopyIDMode(r.Context(), actor, in.Mode, audit); err != nil {
		if e, ok := apperr.As(err); ok && e.Field == "copy_id_mode" {
			err = apperr.OnField(e.Code, "mode", e.Message)
		}
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, struct {
		Mode string `json:"mode"`
	}{in.Mode})
}

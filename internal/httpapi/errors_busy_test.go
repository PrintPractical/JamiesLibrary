package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"

	"jamieslibrary/internal/store"
)

func TestBusyDatabaseIsA503TryAgain(t *testing.T) {
	for _, err := range []error{
		fmt.Errorf("waiting to write: %w", store.ErrBusy),
		errors.New("starting a transaction: database is locked (5) (SQLITE_BUSY)"),
	} {
		w := httptest.NewRecorder()
		WriteError(w, httptest.NewRequest("POST", "/api/books", nil), err)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%v: status %d, want 503", err, w.Code)
		}
	}
}

func TestFullDiskIsA503WithAnActionableMessage(t *testing.T) {
	for _, err := range []error{
		errors.New("committing: database or disk is full (13)"),
		fmt.Errorf("saving the cover: %w", &os.PathError{Op: "write", Path: "/data/images/x", Err: syscall.ENOSPC}),
	} {
		w := httptest.NewRecorder()
		WriteError(w, httptest.NewRequest("POST", "/api/books", nil), err)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%v: status %d, want 503", err, w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "run out of disk space") || strings.Contains(body, "/data") || strings.Contains(body, "SQLite") || strings.Contains(body, "Something went wrong") {
			t.Errorf("%v: body %s", err, body)
		}
	}
}

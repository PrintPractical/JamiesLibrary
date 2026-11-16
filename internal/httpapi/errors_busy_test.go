package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

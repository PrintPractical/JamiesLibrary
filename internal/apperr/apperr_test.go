package apperr

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestStatusMapping(t *testing.T) {
	want := map[Code]int{
		BadRequest: 400, Unauthorized: 401, Forbidden: 403, NotFound: 404, Conflict: 409,
		TooLarge: 413, Invalid: 422, Throttled: 429, Internal: 500, Unavailable: 503,
		Code("something_new"): http.StatusInternalServerError,
	}
	for c, s := range want {
		if got := Status(c); got != s {
			t.Errorf("Status(%q) = %d, want %d", c, got, s)
		}
	}
}

func TestAsFindsWrappedError(t *testing.T) {
	err := fmt.Errorf("checkout: %w", OnField(Conflict, "copy", "This copy is on loan."))
	e, ok := As(err)
	if !ok || e.Code != Conflict || e.Field != "copy" || e.Message != "This copy is on loan." {
		t.Fatalf("got %+v, %v", e, ok)
	}
	if _, ok := As(errors.New("plain")); ok {
		t.Fatal("plain error must not match")
	}
}

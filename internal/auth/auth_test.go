package auth

import (
	"context"
	"testing"

	"jamieslibrary/internal/perm"
)

func TestNoCallerByDefault(t *testing.T) {
	if Caller(context.Background()) != nil {
		t.Fatal("a fresh context has a caller")
	}
	var c *CallerInfo
	if c.Has(perm.CatalogBrowse) {
		t.Fatal("nil caller has a permission")
	}
}

func TestCallerRoundTrip(t *testing.T) {
	in := &CallerInfo{UserID: 7, Permissions: perm.NewSet(perm.CatalogBrowse), MustChangePassword: true}
	got := Caller(WithCaller(context.Background(), in))
	if got != in || !got.Has(perm.CatalogBrowse) || got.Has(perm.UsersManage) {
		t.Fatalf("got %+v", got)
	}
}

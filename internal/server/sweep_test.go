package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jamieslibrary/internal/media"
	"jamieslibrary/internal/store"
)

func TestSweepImagesKeepsBookCoversAndLogo(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, dir, store.Options{NoLock: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	images, err := media.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	name := func(c string) string { return strings.Repeat(c, 32) + ".jpg" }
	cover, logo, stray := name("a"), name("b"), name("c")
	for _, q := range []string{
		`INSERT INTO books (title, cover, search_text, created_at) VALUES ('B', '` + cover + `', 'b', 'x')`,
		`UPDATE settings SET logo_file = '` + logo + `'`,
	} {
		if _, err := st.DB().ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{cover, logo, stray, ".tmp-1"} {
		if err := os.WriteFile(filepath.Join(dir, "images", n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := sweepImages(ctx, st, images, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	for n, want := range map[string]bool{cover: true, logo: true, stray: false, ".tmp-1": false} {
		if _, err := os.Stat(filepath.Join(dir, "images", n)); (err == nil) != want {
			t.Errorf("%s present = %v, want %v", n, err == nil, want)
		}
	}
}

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionPrintsBuildVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, func(string) string { return "" }, &out, &errOut); code != 0 || out.String() != "dev\n" {
		t.Fatalf("code %d out %q err %q", code, out.String(), errOut.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"frobnicate"}, func(string) string { return "" }, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "frobnicate") {
		t.Fatalf("code %d err %q", code, errOut.String())
	}
}

func TestBadConfigExitsNonZeroNamingTheValue(t *testing.T) {
	var out, errOut bytes.Buffer
	env := func(k string) string {
		if k == "JL_HTTP_PORT" {
			return "eighty"
		}
		return ""
	}
	if code := run([]string{"serve"}, env, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "JL_HTTP_PORT") {
		t.Fatalf("code %d err %q", code, errOut.String())
	}
}

func TestServeRefusesAnUnusableDataDirectoryNamingIt(t *testing.T) {
	file := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := run([]string{"serve", "--data", file}, func(string) string { return "" }, &out, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), file) {
		t.Fatalf("code %d err %q", code, errOut.String())
	}
}

package main

import (
	"bytes"
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

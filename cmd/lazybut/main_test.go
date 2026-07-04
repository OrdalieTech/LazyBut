package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestParseSize(t *testing.T) {
	width, height, err := parseSize("120x40")
	if err != nil {
		t.Fatal(err)
	}
	if width != 120 || height != 40 {
		t.Fatalf("size = %dx%d", width, height)
	}
}

func TestParseSizeRejectsInvalidInput(t *testing.T) {
	for _, value := range []string{"120", "x40", "10x2"} {
		if _, _, err := parseSize(value); err == nil {
			t.Fatalf("expected %q to fail", value)
		}
	}
}

func TestResolveUpdateTagPassesThroughExplicitTags(t *testing.T) {
	tag, err := resolveUpdateTag("v0.1.21")
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.1.21" {
		t.Fatalf("tag = %q", tag)
	}
	if _, err := resolveUpdateTag(""); err == nil {
		t.Fatal("expected empty ref to fail")
	}
}

// End-to-end check of the update mechanics: serve a release tarball from a
// local server, install it over an existing binary, verify content and mode.
func TestDownloadAndInstallReplacesBinary(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := []byte("#!/bin/sh\necho new-lazybut\n")
	if err := tw.WriteHeader(&tar.Header{Name: "lazybut", Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(buf.Bytes())
	}))
	defer server.Close()

	dir := t.TempDir()
	target := filepath.Join(dir, "lazybut")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := downloadAndInstall(server.URL, target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("installed binary content = %q", got)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("installed binary mode = %v, want 0755", info.Mode().Perm())
	}
}

func TestDownloadAndInstallRejectsMissingAsset(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	target := filepath.Join(t.TempDir(), "lazybut")
	if err := downloadAndInstall(server.URL, target); err == nil {
		t.Fatal("expected 404 download to fail")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("failed update must not leave a binary behind")
	}
}

package main

import "testing"

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

func TestSelfUpdateCommandUsesInstallDirAndRef(t *testing.T) {
	cmd, installDir, err := selfUpdateCommand("v0.1.8", "/tmp/bin")
	if err != nil {
		t.Fatal(err)
	}
	wantCmd := []string{"go", "install", "github.com/OrdalieTech/LazyBut/cmd/lazybut@v0.1.8"}
	if len(cmd) != len(wantCmd) {
		t.Fatalf("cmd length = %d, want %d", len(cmd), len(wantCmd))
	}
	for i := range cmd {
		if cmd[i] != wantCmd[i] {
			t.Fatalf("cmd[%d] = %q, want %q", i, cmd[i], wantCmd[i])
		}
	}
	if installDir != "/tmp/bin" {
		t.Fatalf("installDir = %q", installDir)
	}
}

func TestDefaultUpdateRefUsesLatestRelease(t *testing.T) {
	if defaultUpdateRef != "latest" {
		t.Fatalf("defaultUpdateRef = %q, want latest", defaultUpdateRef)
	}
}

func TestSelfUpdateCommandRejectsEmptyRef(t *testing.T) {
	if _, _, err := selfUpdateCommand(" ", "/tmp/bin"); err == nil {
		t.Fatal("expected empty ref to fail")
	}
}

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

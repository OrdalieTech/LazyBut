package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/OrdalieTech/LazyBut/internal/gitbutler"
	"github.com/OrdalieTech/LazyBut/internal/tui"
)

func main() {
	dir := flag.String("C", ".", "run as if lazybut started in this directory")
	bin := flag.String("but-bin", "but", "GitButler CLI binary")
	snapshot := flag.String("snapshot", "", "render one non-interactive frame, formatted as WIDTHxHEIGHT")
	overlay := flag.String("snapshot-overlay", "", "overlay to render in snapshot mode: help, confirm, prompt, palette, branch")
	flag.Parse()

	client := gitbutler.NewClient(*dir, gitbutler.ExecRunner{Bin: *bin})
	if *snapshot != "" {
		width, height, err := parseSize(*snapshot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "lazybut: %v\n", err)
			os.Exit(1)
		}
		view := tui.SnapshotMode(client, width, height, *overlay)
		fmt.Print(view)
		return
	}
	if err := tui.Run(client); err != nil {
		fmt.Fprintf(os.Stderr, "lazybut: %v\n", err)
		os.Exit(1)
	}
}

func parseSize(value string) (int, int, error) {
	parts := strings.Split(value, "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("snapshot size must be WIDTHxHEIGHT")
	}
	width, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	height, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	if width < 20 || height < 8 {
		return 0, 0, fmt.Errorf("snapshot size is too small")
	}
	return width, height, nil
}

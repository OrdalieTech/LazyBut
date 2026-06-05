package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/OrdalieTech/LazyBut/internal/gitbutler"
	"github.com/OrdalieTech/LazyBut/internal/tui"
)

const modulePath = "github.com/OrdalieTech/LazyBut/cmd/lazybut"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "update" {
		if err := runSelfUpdate(os.Args[2:]); err != nil {
			if err == flag.ErrHelp {
				return
			}
			fmt.Fprintf(os.Stderr, "lazybut: %v\n", err)
			os.Exit(1)
		}
		return
	}

	dir := flag.String("C", ".", "run as if lazybut started in this directory")
	bin := flag.String("but-bin", "but", "GitButler CLI binary")
	noAutoRefresh := flag.Bool("no-auto-refresh", false, "disable background GitButler status refresh")
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
	if err := tui.Run(client, !*noAutoRefresh); err != nil {
		fmt.Fprintf(os.Stderr, "lazybut: %v\n", err)
		os.Exit(1)
	}
}

func runSelfUpdate(args []string) error {
	flags := flag.NewFlagSet("update", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	ref := flags.String("ref", "main", "module ref to install, such as main, latest, or v0.1.8")
	installDir := flags.String("install-dir", "", "directory to install lazybut into")
	dryRun := flags.Bool("dry-run", false, "print the go install command without running it")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected update argument %q", flags.Arg(0))
	}
	cmd, targetDir, err := selfUpdateCommand(*ref, *installDir)
	if err != nil {
		return err
	}
	fmt.Printf("Updating lazybut from %s...\n", modulePath+"@"+*ref)
	fmt.Printf("Installing to %s\n", targetDir)
	if *dryRun {
		fmt.Println(strings.Join(append([]string{"GOBIN=" + targetDir}, cmd...), " "))
		return nil
	}
	command := exec.Command(cmd[0], cmd[1:]...)
	command.Env = append(os.Environ(), "GOBIN="+targetDir)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}
	fmt.Println("lazybut updated")
	return nil
}

func selfUpdateCommand(ref, installDir string) ([]string, string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, "", fmt.Errorf("update ref cannot be empty")
	}
	if installDir == "" {
		executable, err := os.Executable()
		if err != nil {
			return nil, "", fmt.Errorf("locate current executable: %w", err)
		}
		resolved, err := filepath.EvalSymlinks(executable)
		if err == nil {
			executable = resolved
		}
		installDir = filepath.Dir(executable)
	}
	return []string{"go", "install", modulePath + "@" + ref}, installDir, nil
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

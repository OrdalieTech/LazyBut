package main

import (
	"archive/tar"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/OrdalieTech/LazyBut/internal/gitbutler"
	"github.com/OrdalieTech/LazyBut/internal/tui"
)

const modulePath = "github.com/OrdalieTech/LazyBut/cmd/lazybut"
const repoSlug = "OrdalieTech/LazyBut"
const defaultUpdateRef = "latest"

// version is stamped by the release workflow via -ldflags "-X main.version=vX.Y.Z".
// Empty for source builds, which fall back to module/VCS build info.
var version string

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println("lazybut " + versionString())
		return
	}
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

// runSelfUpdate downloads the release binary for this platform — the same
// assets install.sh uses — and swaps it in place. No Go toolchain needed,
// so binaries installed via install.sh can update themselves.
func runSelfUpdate(args []string) error {
	flags := flag.NewFlagSet("update", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	ref := flags.String("ref", defaultUpdateRef, "release tag to install, such as latest or v0.1.21")
	installDir := flags.String("install-dir", "", "directory to install lazybut into (default: alongside the current binary)")
	dryRun := flags.Bool("dry-run", false, "print what would be downloaded without installing")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected update argument %q", flags.Arg(0))
	}
	tag, err := resolveUpdateTag(strings.TrimSpace(*ref))
	if err != nil {
		return err
	}
	targetDir := *installDir
	if targetDir == "" {
		if targetDir, err = currentInstallDir(); err != nil {
			return err
		}
	}
	target := filepath.Join(targetDir, "lazybut")
	if tag == versionString() {
		fmt.Printf("lazybut %s is already the newest release\n", tag)
		return nil
	}
	assetURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repoSlug, tag, releaseAsset())
	fmt.Printf("Updating lazybut %s -> %s\n", versionString(), tag)
	if *dryRun {
		fmt.Printf("Would download %s\nWould install to %s\n", assetURL, target)
		return nil
	}
	if err := downloadAndInstall(assetURL, target); err != nil {
		return err
	}
	fmt.Printf("lazybut %s installed to %s\n", tag, target)
	return nil
}

func releaseAsset() string {
	return fmt.Sprintf("lazybut_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
}

// resolveUpdateTag turns "latest" into the concrete release tag by following
// GitHub's releases/latest redirect; explicit tags pass through unchanged.
func resolveUpdateTag(ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("update ref cannot be empty")
	}
	if ref != defaultUpdateRef {
		return ref, nil
	}
	resp, err := httpClient().Get("https://github.com/" + repoSlug + "/releases/latest")
	if err != nil {
		return "", fmt.Errorf("resolve latest release: %w", err)
	}
	defer resp.Body.Close()
	final := resp.Request.URL.Path
	idx := strings.LastIndex(final, "/tag/")
	if resp.StatusCode != http.StatusOK || idx < 0 {
		return "", fmt.Errorf("resolve latest release: unexpected response %s (%s)", resp.Status, final)
	}
	return final[idx+len("/tag/"):], nil
}

func currentInstallDir() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate current executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	return filepath.Dir(executable), nil
}

// downloadAndInstall streams the release tar.gz, extracts the lazybut binary
// into a temp file beside target, and renames it into place — atomic on the
// same filesystem, and replacing a running binary is fine on unix.
func downloadAndInstall(url, target string) error {
	resp, err := httpClient().Get(url)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s — no release asset for this platform? try `go install %s@latest`", url, resp.Status, modulePath)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("read release archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("release archive has no lazybut binary")
		}
		if err != nil {
			return fmt.Errorf("read release archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != "lazybut" {
			continue
		}
		tmp, err := os.CreateTemp(filepath.Dir(target), ".lazybut-update-*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := io.Copy(tmp, tr); err != nil {
			tmp.Close()
			return fmt.Errorf("write update: %w", err)
		}
		if err := tmp.Chmod(0o755); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		return os.Rename(tmp.Name(), target)
	}
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 3 * time.Minute}
}

func versionString() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && setting.Value != "" {
			if len(setting.Value) > 12 {
				return setting.Value[:12]
			}
			return setting.Value
		}
	}
	return "dev"
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

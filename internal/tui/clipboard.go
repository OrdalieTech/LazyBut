package tui

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/charmbracelet/x/term"
)

// copyToClipboard copies text to the system clipboard. It tries, in order:
//  1. OSC 52 escape sequence (works over SSH in supported terminals)
//  2. Native clipboard utilities (pbcopy / wl-copy / xclip / xsel)
//
// OSC 52 is tried first because it works over SSH — the terminal on the local
// machine interprets the sequence and updates its own clipboard.
func copyToClipboard(text string) error {
	// OSC 52: base64 payload inside an escape sequence that the terminal
	// interprets as a clipboard write. Most modern terminals (iTerm2,
	// Alacritty, kitty, Windows Terminal, tmux) support this. Emitted on
	// one write to the terminal, preferring stderr so Bubble Tea cannot
	// interleave a frame flush mid-sequence. If stderr was redirected, write
	// directly to the controlling terminal instead.
	osc52Written := false
	output := os.Stderr
	var tty *os.File
	if !term.IsTerminal(output.Fd()) {
		tty, _ = os.OpenFile("/dev/tty", os.O_WRONLY, 0)
		if tty != nil {
			defer tty.Close()
			output = tty
		} else if term.IsTerminal(os.Stdout.Fd()) {
			output = os.Stdout
		} else {
			output = nil
		}
	}
	if output != nil {
		_, err := output.WriteString("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07")
		osc52Written = err == nil
	}
	// Also try native clipboards as a backup for terminals that ignore OSC 52.
	var cmds [][]string
	switch runtime.GOOS {
	case "darwin":
		cmds = [][]string{{"pbcopy"}}
	case "linux":
		cmds = [][]string{
			{"wl-copy"},
			{"xclip", "-selection", "clipboard"},
			{"xsel", "--clipboard", "--input"},
		}
	default:
		cmds = [][]string{{"pbcopy"}}
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	if osc52Written {
		return nil
	}
	return errors.New("no terminal or native clipboard available")
}

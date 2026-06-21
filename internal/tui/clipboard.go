package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// copyToClipboard copies text to the system clipboard. It tries, in order:
//  1. OSC 52 escape sequence (works over SSH in supported terminals)
//  2. Native clipboard utilities (pbcopy / wl-copy / xclip / xsel)
//
// OSC 52 is tried first because it works over SSH — the terminal on the local
// machine interprets the sequence and updates its own clipboard.
func copyToClipboard(text string) error {
	// OSC 52: write base64-ish payload inside an escape sequence that the
	// terminal interprets as a clipboard write. Most modern terminals
	// (iTerm2, Alacritty, kitty, Windows Terminal, tmux) support this.
	fmt.Printf("\x1b]52;c;%s\x07", base64Encode(text))
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
	return nil // OSC 52 was already emitted; native failure is non-fatal.
}

// base64Encode is a minimal base64 encoder (standard alphabet, no padding) so
// we don't pull in encoding/base64 just for clipboard text.
func base64Encode(s string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	data := []byte(s)
	var b strings.Builder
	for i := 0; i < len(data); i += 3 {
		b0 := data[i]
		b1 := byte(0)
		b2 := byte(0)
		n := 1
		if i+1 < len(data) {
			b1 = data[i+1]
			n = 2
		}
		if i+2 < len(data) {
			b2 = data[i+2]
			n = 3
		}
		b.WriteByte(alphabet[b0>>2])
		b.WriteByte(alphabet[((b0&0x03)<<4)|(b1>>4)])
		if n == 1 {
			b.WriteString("==")
			break
		}
		b.WriteByte(alphabet[((b1&0x0f)<<2)|(b2>>6)])
		if n == 2 {
			b.WriteByte('=')
			break
		}
		b.WriteByte(alphabet[b2&0x3f])
	}
	return b.String()
}

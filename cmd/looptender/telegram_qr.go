package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	qr "github.com/piglig/go-qr/v2"
)

// The caller supplies the validated DeepLink, never the bot token. Rendering is
// local and the text link remains available even if QR encoding fails.
func printTelegramPairingLink(out io.Writer, link string, lifetime time.Duration, showQR bool) {
	fmt.Fprintf(out, "Open this one-tap link in Telegram and press Start (expires in %s):\n  %s\n", lifetime, link)
	if !showQR {
		return
	}
	code, err := telegramTerminalQR(link)
	if err != nil {
		fmt.Fprintln(out, "QR unavailable; use the text link above.")
		return
	}
	fmt.Fprintln(out, "Scan this QR, or use the text link above (same pairing and expiry):")
	fmt.Fprint(out, code)
}

func telegramTerminalQR(link string) (string, error) {
	code, err := qr.Encode(link, qr.WithECC(qr.ECCMedium))
	if err != nil {
		return "", err
	}
	// A four-module white quiet zone surrounds the symbol. Half blocks draw
	// two square modules per character cell, keeping pairing links narrow.
	// Explicit black/white ANSI colors avoid depending on the terminal theme.
	const border = 4
	var out strings.Builder
	for y := -border; y < code.Size()+border; y += 2 {
		out.WriteString("\x1b[30;47m")
		for x := -border; x < code.Size()+border; x++ {
			top, bottom := code.Module(x, y), code.Module(x, y+1)
			switch {
			case top && bottom:
				out.WriteRune('█')
			case top:
				out.WriteRune('▀')
			case bottom:
				out.WriteRune('▄')
			default:
				out.WriteByte(' ')
			}
		}
		out.WriteString("\x1b[0m\n")
	}
	return out.String(), nil
}

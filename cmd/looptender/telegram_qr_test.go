package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/telegram"
	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

// Decode the actual terminal glyphs, not the encoder's matrix. One character
// cell represents two vertically stacked square modules with explicit colors.
func decodeTelegramTerminalQR(t *testing.T, output string) string {
	t.Helper()
	var rows [][]rune
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "\x1b[30;47m") {
			if !strings.HasSuffix(line, "\x1b[0m") {
				t.Fatal("QR row leaves terminal colors active")
			}
			rows = append(rows, []rune(strings.TrimSuffix(strings.TrimPrefix(line, "\x1b[30;47m"), "\x1b[0m")))
		}
	}
	if len(rows) == 0 || len(rows[0]) > 80 {
		t.Fatal("missing QR or pairing QR exceeds 80 columns")
	}
	const scale = 6
	img := image.NewGray(image.Rect(0, 0, len(rows[0])*scale, len(rows)*2*scale))
	for y, row := range rows {
		if len(row) != len(rows[0]) {
			t.Fatal("unequal QR row widths")
		}
		for x, ch := range row {
			if !strings.ContainsRune(" █▀▄", ch) {
				t.Fatalf("unexpected terminal glyph %q", ch)
			}
			for dy := 0; dy < 2*scale; dy++ {
				dark := ch == '█' || (ch == '▀' && dy < scale) || (ch == '▄' && dy >= scale)
				c := color.Gray{Y: 255}
				if dark {
					c.Y = 0
				}
				for dx := 0; dx < scale; dx++ {
					img.SetGray(x*scale+dx, y*2*scale+dy, c)
				}
			}
		}
	}
	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		t.Fatal(err)
	}
	result, err := qrcode.NewQRCodeReader().DecodeWithoutHints(bitmap)
	if err != nil {
		t.Fatalf("independent decoder refused terminal QR: %v", err)
	}
	return result.GetText()
}

func TestTelegramTerminalQRRoundTrip(t *testing.T) {
	for _, username := range []string{"exbot", "example_looptender_bot", strings.Repeat("x", 32)} {
		for _, nonce := range []string{strings.Repeat("a", 16), "Abcd_0123456789-abcdefghij", strings.Repeat("Z", 48)} {
			t.Run(username, func(t *testing.T) {
				link, err := telegram.DeepLink(username, nonce)
				if err != nil {
					t.Fatal(err)
				}
				var out bytes.Buffer
				printTelegramPairingLink(&out, link, time.Minute, true)
				if got := decodeTelegramTerminalQR(t, out.String()); got != link {
					t.Fatalf("decoded %q; want exact pairing link %q", got, link)
				}
				if !strings.Contains(out.String(), "  "+link+"\n") || !strings.Contains(out.String(), "expires in 1m0s") {
					t.Fatal("QR must retain text link and expiry")
				}
			})
		}
	}
}

func TestTelegramTerminalQRPlainFallback(t *testing.T) {
	for _, tc := range []struct {
		name, link string
		qr         bool
	}{
		{"disabled", "https://t.me/example_bot?start=pair_example", false},
		{"encoding_failure", strings.Repeat("x", 10000), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			printTelegramPairingLink(&out, tc.link, time.Minute, tc.qr)
			if !strings.Contains(out.String(), "  "+tc.link+"\n") || strings.ContainsAny(out.String(), "\x1b█▀▄") {
				t.Fatal("fallback must retain plain link without partial QR")
			}
			if tc.qr && !strings.Contains(out.String(), "QR unavailable; use the text link above.") {
				t.Fatal("encoding failure must explain the text fallback")
			}
		})
	}
}

func TestTelegramSetupQRHelpAndInvalidFlag(t *testing.T) {
	var out, errs bytes.Buffer
	e := env{ctx: context.Background(), stdout: &out, stderr: &errs}
	if code := telegramCommandWithInput(e, []string{"setup", "--help"}, nil); code != exitOK || !strings.Contains(errs.String(), "-qr") {
		t.Fatal("setup help must describe opt-in QR")
	}
	errs.Reset()
	if code := telegramCommandWithInput(e, []string{"setup", "--qr=invalid"}, nil); code != exitUsage || out.Len() != 0 {
		t.Fatal("invalid QR flag must fail before token input or setup")
	}
}

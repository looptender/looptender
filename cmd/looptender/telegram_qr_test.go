package main

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/telegram"
	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode/decoder"
)

// Decode the actual terminal glyphs, not the encoder's matrix. One character
// cell represents two vertically stacked square modules with explicit colors.
// The glyphs are turned back into a module matrix, the four-module quiet zone
// is checked, and gozxing's QR decoder (format/version, unmasking, Reed-Solomon)
// decodes it. gozxing's image finder-pattern detector is deliberately not used:
// it fails on about 1% of valid symbols whatever the pixel scale or binarizer
// (OpenCV reads every one of them), which made this test flake. On failure the
// whole output, including the pairing link, is printed so it can be replayed.
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
	w, h := len(rows[0]), len(rows)*2
	full, err := gozxing.NewBitMatrix(w, h)
	if err != nil {
		t.Fatal(err)
	}
	for y, row := range rows {
		if len(row) != w {
			t.Fatal("unequal QR row widths")
		}
		for x, ch := range row {
			if !strings.ContainsRune(" █▀▄", ch) {
				t.Fatalf("unexpected terminal glyph %q", ch)
			}
			if ch == '█' || ch == '▀' {
				full.Set(x, 2*y)
			}
			if ch == '█' || ch == '▄' {
				full.Set(x, 2*y+1)
			}
		}
	}
	left, top, right, bottom := w, h, -1, -1
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if full.Get(x, y) {
				left, right = min(left, x), max(right, x)
				top, bottom = min(top, y), max(bottom, y)
			}
		}
	}
	n := right - left + 1
	if right < 0 || bottom-top+1 != n {
		t.Fatalf("QR symbol is not square\n%s", output)
	}
	if left < 4 || top < 4 || w-1-right < 4 || h-1-bottom < 4 {
		t.Fatalf("QR quiet zone is under four modules\n%s", output)
	}
	sym, err := gozxing.NewBitMatrix(n, n)
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if full.Get(left+x, top+y) {
				sym.Set(x, y)
			}
		}
	}
	result, err := decoder.NewDecoder().Decode(sym, nil)
	if err != nil {
		t.Fatalf("independent decoder refused terminal QR: %v\n%s", err, output)
	}
	return result.GetText()
}

// Links captured from random-nonce runs whose symbols gozxing's image detector
// could not locate (NotFoundException) although the matrix is valid.
var telegramQRDetectorMissLinks = []string{
	"https://t.me/example_looptender_bot?start=pair_3aLERtGIaNPs--rxvYGHjOup",
	"https://t.me/example_looptender_bot?start=pair_kWTURn_CCoaFGd2ibXsz_yqh",
	"https://t.me/example_looptender_bot?start=pair_maHTE-nsXcrOXuzJdBXG7I3M",
	"https://t.me/example_looptender_bot?start=pair_kCc-KvVgoDrENoOhfUTIREdH",
}

func TestTelegramTerminalQRFixedAndRandomLinks(t *testing.T) {
	links := slices.Clone(telegramQRDetectorMissLinks)
	for i := 0; i < 300; i++ {
		nonce, err := telegram.NewPairingNonce()
		if err != nil {
			t.Fatal(err)
		}
		link, err := telegram.DeepLink("example_looptender_bot", nonce)
		if err != nil {
			t.Fatal(err)
		}
		links = append(links, link)
	}
	for _, link := range links {
		var out bytes.Buffer
		printTelegramPairingLink(&out, link, time.Minute, true)
		if got := decodeTelegramTerminalQR(t, out.String()); got != link {
			t.Fatalf("decoded %q; want pairing link %q", got, link)
		}
	}
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

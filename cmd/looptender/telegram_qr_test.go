package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/telegram"
	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	"github.com/makiuchi-d/gozxing/qrcode/decoder"
)

// terminalQRModules turns terminal glyphs back into a module grid. One
// character cell represents two vertically stacked square modules with
// explicit colors.
func terminalQRModules(output string) ([][]bool, error) {
	var rows [][]rune
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "\x1b[30;47m") {
			if !strings.HasSuffix(line, "\x1b[0m") {
				return nil, errors.New("QR row leaves terminal colors active")
			}
			rows = append(rows, []rune(strings.TrimSuffix(strings.TrimPrefix(line, "\x1b[30;47m"), "\x1b[0m")))
		}
	}
	if len(rows) == 0 || len(rows[0]) > 80 {
		return nil, errors.New("missing QR or pairing QR exceeds 80 columns")
	}
	grid := make([][]bool, 2*len(rows))
	for i := range grid {
		grid[i] = make([]bool, len(rows[0]))
	}
	for y, row := range rows {
		if len(row) != len(rows[0]) {
			return nil, errors.New("unequal QR row widths")
		}
		for x, ch := range row {
			if !strings.ContainsRune(" █▀▄", ch) {
				return nil, fmt.Errorf("unexpected terminal glyph %q", ch)
			}
			grid[2*y][x] = ch == '█' || ch == '▀'
			grid[2*y+1][x] = ch == '█' || ch == '▄'
		}
	}
	return grid, nil
}

// terminalQRRender is the inverse of terminalQRModules, for building
// deliberately damaged symbols in negative tests.
func terminalQRRender(grid [][]bool) string {
	var b strings.Builder
	for y := 0; y < len(grid); y += 2 {
		b.WriteString("\x1b[30;47m")
		for x := range grid[y] {
			top, bottom := grid[y][x], y+1 < len(grid) && grid[y+1][x]
			switch {
			case top && bottom:
				b.WriteRune('█')
			case top:
				b.WriteRune('▀')
			case bottom:
				b.WriteRune('▄')
			default:
				b.WriteByte(' ')
			}
		}
		b.WriteString("\x1b[0m\n")
	}
	return b.String()
}

// decodeTelegramTerminalQRMatrix decodes the actual terminal glyphs, not the
// encoder's matrix. It checks the four-module quiet zone, a square symbol, the
// three finder patterns and the timing rows (which gozxing's matrix decoder
// does not look at), then decodes with gozxing's QR decoder (format/version,
// unmasking, Reed-Solomon). gozxing's image finder-pattern detector is not
// used here: it fails on about 1% of valid symbols whatever the pixel scale or
// binarizer (OpenCV reads every one of them), which made this test flake.
func decodeTelegramTerminalQRMatrix(output string) (string, error) {
	grid, err := terminalQRModules(output)
	if err != nil {
		return "", err
	}
	h, w := len(grid), len(grid[0])
	left, top, right, bottom := w, h, -1, -1
	for y := range grid {
		for x := range grid[y] {
			if grid[y][x] {
				left, right = min(left, x), max(right, x)
				top, bottom = min(top, y), max(bottom, y)
			}
		}
	}
	n := right - left + 1
	if right < 0 || bottom-top+1 != n {
		return "", errors.New("QR symbol is not square")
	}
	if left < 4 || top < 4 || w-1-right < 4 || h-1-bottom < 4 {
		return "", errors.New("QR quiet zone is under four modules")
	}
	at := func(x, y int) bool { return grid[top+y][left+x] }
	for _, o := range [][2]int{{0, 0}, {n - 7, 0}, {0, n - 7}} {
		for dy := 0; dy < 7; dy++ {
			for dx := 0; dx < 7; dx++ {
				ring := max(abs(dx-3), abs(dy-3))
				if at(o[0]+dx, o[1]+dy) != (ring != 2) {
					return "", errors.New("QR finder pattern is damaged")
				}
			}
		}
	}
	for i := 8; i < n-8; i++ {
		if at(i, 6) != (i%2 == 0) || at(6, i) != (i%2 == 0) {
			return "", errors.New("QR timing pattern is damaged")
		}
	}
	sym, err := gozxing.NewBitMatrix(n, n)
	if err != nil {
		return "", err
	}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if at(x, y) {
				sym.Set(x, y)
			}
		}
	}
	result, err := decoder.NewDecoder().Decode(sym, nil)
	if err != nil {
		return "", fmt.Errorf("independent decoder refused terminal QR: %w", err)
	}
	return result.GetText(), nil
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// decodeTelegramTerminalQRImage rasterises the glyphs and decodes them with
// gozxing's image path (finder detection). That detector misses some valid
// symbols, so callers use it only for links verified to decode with it.
func decodeTelegramTerminalQRImage(output string) (string, error) {
	grid, err := terminalQRModules(output)
	if err != nil {
		return "", err
	}
	const scale = 6
	img := image.NewGray(image.Rect(0, 0, len(grid[0])*scale, len(grid)*scale))
	for y := range grid {
		for x, dark := range grid[y] {
			c := color.Gray{Y: 255}
			if dark {
				c.Y = 0
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetGray(x*scale+dx, y*scale+dy, c)
				}
			}
		}
	}
	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", err
	}
	result, err := qrcode.NewQRCodeReader().DecodeWithoutHints(bitmap)
	if err != nil {
		return "", fmt.Errorf("image decoder refused terminal QR: %w", err)
	}
	return result.GetText(), nil
}

// On failure the whole output, including the pairing link, is printed so a
// random-nonce failure can be replayed.
func decodeTelegramTerminalQR(t *testing.T, output string) string {
	t.Helper()
	got, err := decodeTelegramTerminalQRMatrix(output)
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	return got
}

func TestTelegramTerminalQRRejectsBadSymbols(t *testing.T) {
	link, err := telegram.DeepLink("example_looptender_bot", strings.Repeat("a", 24))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	printTelegramPairingLink(&out, link, time.Minute, true)
	if got, err := decodeTelegramTerminalQRMatrix(out.String()); err != nil || got != link {
		t.Fatalf("control symbol must decode: %q %v", got, err)
	}
	base, err := terminalQRModules(out.String())
	if err != nil {
		t.Fatal(err)
	}
	// The symbol starts at module (4,4); find its size from the dark bounds.
	n := 0
	for x := range base[4] {
		if base[4][x] {
			n = x - 3
		}
	}
	mutate := func(f func(g [][]bool) [][]bool) string {
		g := make([][]bool, len(base))
		for y := range base {
			g[y] = slices.Clone(base[y])
		}
		return terminalQRRender(f(g))
	}
	for name, bad := range map[string]string{
		// Far more data modules than level M can repair.
		"flipped data modules": mutate(func(g [][]bool) [][]bool {
			for y := 12; y < 22; y++ {
				for x := 9; x < n-8; x++ {
					g[4+y][4+x] = !g[4+y][4+x]
				}
			}
			return g
		}),
		"damaged finder": mutate(func(g [][]bool) [][]bool {
			for y := 2; y < 5; y++ {
				for x := 2; x < 5; x++ {
					g[4+y][4+x] = false
				}
			}
			return g
		}),
		"damaged timing row": mutate(func(g [][]bool) [][]bool { g[4+6][4+10] = !g[4+6][4+10]; return g }),
		"quiet zone of three": mutate(func(g [][]bool) [][]bool {
			for y := range g {
				g[y] = g[y][1:]
			}
			return g
		}),
		"dark module in quiet zone": mutate(func(g [][]bool) [][]bool { g[1][1] = true; return g }),
		"truncated rows":            mutate(func(g [][]bool) [][]bool { return g[:len(g)-6] }),
	} {
		got, err := decodeTelegramTerminalQRMatrix(bad)
		if err == nil {
			t.Errorf("%s: decoded %q instead of failing", name, got)
		}
		t.Logf("%s: %v", name, err)
	}
	if _, err := decodeTelegramTerminalQRMatrix(strings.Join(strings.Split(out.String(), "\n")[:6], "\n")); err == nil {
		t.Error("QR cut to a few terminal rows must fail")
	}
	if _, err := decodeTelegramTerminalQRMatrix("no QR here\n"); err == nil {
		t.Error("output without a QR must fail")
	}
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
				// Fixed inputs verified to decode through the image path, so a
				// rendering regression that breaks real scanning is still caught.
				if got, err := decodeTelegramTerminalQRImage(out.String()); err != nil || got != link {
					t.Fatalf("image decode of %q: got %q, err %v", link, got, err)
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

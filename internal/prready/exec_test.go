package prready

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPRReadyExecHelper(t *testing.T) {
	switch os.Getenv("PR_READY_TEST_HELPER") {
	case "overflow":
		fmt.Print(strings.Repeat("x", MaxBytes+1))
		os.Exit(0)
	case "wait":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "stderr":
		fmt.Fprint(os.Stderr, "private diagnostic")
		fmt.Print(`{"ok":true}`)
		os.Exit(1)
	}
}
func TestExecGHBoundsAndCancellation(t *testing.T) {
	for _, mode := range []string{"overflow", "wait", "stderr"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("PR_READY_TEST_HELPER", mode)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if mode == "wait" {
				cancel()
			}
			b, err := ExecGH(os.Args[0])(ctx, "-test.run=^TestPRReadyExecHelper$")
			if err == nil || strings.Contains(string(b), "private diagnostic") {
				t.Fatalf("%s bytes=%d err=%v", mode, len(b), err)
			}
			if mode == "overflow" && len(b) != 0 {
				t.Fatal("overflow exposed partial response")
			}
		})
	}
}

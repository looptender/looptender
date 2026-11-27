package prready

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
)

// ExecGH bounds stdout and discards stderr so API failures cannot expose tokens
// or private response text. Inspect supplies only fixed read-only operations.
func ExecGH(path string) Query {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.WaitDelay = time.Second
		cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_PAGER=cat")
		var b boundedBuffer
		cmd.Stdout = &b
		cmd.Stderr = io.Discard
		err := cmd.Run()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if b.exceeded {
			return nil, errors.New("output limit")
		}
		return b.buffer.Bytes(), err
	}
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	exceeded bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > MaxBytes {
		b.exceeded = true
		return 0, errors.New("output limit")
	}
	return b.buffer.Write(p)
}

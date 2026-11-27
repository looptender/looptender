//go:build !windows

package main

import (
	"fmt"
	"os"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/state"
)

func accountUsageIdentity(_ string, info os.FileInfo) (string, error) {
	if info == nil {
		return "", accounts.ErrChanged
	}
	id, ok := state.IdentityOf(info)
	if !ok {
		return "", accounts.ErrChanged
	}
	return fmt.Sprintf("%d:%d", id.Dev, id.Ino), nil
}

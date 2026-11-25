//go:build !unix

package login

import "os"

func defaultOwnUID() int {
	return 0
}

func sameOwner(_ os.FileInfo, _ int) bool {
	return true
}

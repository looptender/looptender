//go:build linux

package sessionpipe

import (
	"os"
	"strconv"
	"strings"
)

// procStart names when process pid started (field 22 of /proc/<pid>/stat,
// in clock ticks since boot), so a reused process ID is not taken for the
// wake that was recorded; "" when it cannot be read.
func procStart(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	// The command name, field 2, is in parentheses and may hold spaces.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return ""
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return ""
	}
	return "linux:" + f[19]
}

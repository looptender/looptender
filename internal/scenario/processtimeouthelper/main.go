// A fictional process adapter command. It never launches another process.
package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "warm" {
		return
	}
	if len(os.Args) != 4 {
		os.Exit(2)
	}
	dir, op, address := os.Args[1], os.Args[2], os.Args[3]
	f, err := os.OpenFile(filepath.Join(dir, "calls"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		panic(err)
	}
	fmt.Fprintln(f, op)
	f.Close()
	if op == "resume" {
		return
	}
	mode, err := os.ReadFile(filepath.Join(dir, "mode"))
	if err != nil {
		panic(err)
	}
	switch string(mode) {
	case "block":
		conn, err := net.DialTimeout("tcp", address, 5*time.Second)
		if err != nil {
			panic(err)
		}
		defer conn.Close()
		// Parent cleanup closes the connection on every failure path. This deadline
		// also bounds the helper if the test process itself disappears.
		conn.SetReadDeadline(time.Now().Add(20 * time.Second))
		var b [1]byte
		conn.Read(b[:])
		if err := os.WriteFile(filepath.Join(dir, "escaped"), []byte("status returned without adapter termination"), 0600); err != nil {
			panic(err)
		}
		os.Exit(3)
	case "idle":
		fmt.Println(`{"state":"idle"}`)
	case "limit":
		fmt.Println(`{"state":"error","errorClass":"rate_limit"}`)
	default:
		os.Exit(4)
	}
}

// A fictional process command: receiving input never claims completed work.
package main

import (
	"fmt"
	"io"
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
	// Bound even stdin receipt if the caller never closes its input pipe.
	watchdog := time.AfterFunc(25*time.Second, func() { os.Exit(6) })
	defer watchdog.Stop()
	dir, op, address := os.Args[1], os.Args[2], os.Args[3]
	if op == "status" {
		fmt.Println(`{"state":"error","errorClass":"unknown"}`)
		return
	}
	if op != "resume" {
		os.Exit(3)
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil || len(input) == 0 || len(input) == 1<<20 {
		os.Exit(4)
	}
	f, err := os.OpenFile(filepath.Join(dir, "receipts"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		panic(err)
	}
	// Only record byte count, not input contents. This means received, not done.
	fmt.Fprintln(f, len(input))
	if err = f.Close(); err != nil {
		panic(err)
	}
	mode, err := os.ReadFile(filepath.Join(dir, "mode"))
	if err != nil {
		panic(err)
	}
	if string(mode) == "return" {
		return
	}
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	var b [1]byte
	conn.Read(b[:])
	if err = os.WriteFile(filepath.Join(dir, "escaped"), []byte("resume returned without adapter termination"), 0600); err != nil {
		panic(err)
	}
	os.Exit(5)
}

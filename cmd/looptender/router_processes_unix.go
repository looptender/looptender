//go:build unix

package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/looptender/looptender/internal/router/channel"
	"github.com/looptender/looptender/internal/router/core"
	"github.com/looptender/looptender/internal/router/front"
	"github.com/looptender/looptender/internal/router/loopcert"
)

// The account router's two processes, started by hand only
// (docs/design/router.md, "The parts"). Nothing starts them, no service
// runs them, and nothing points an agent at them. They read no login: the
// router process here knows no subscription, so every decision it gives is
// "your own login", and the front knows no client, so every request it is
// sent goes on unchanged. They exist to prove the two processes and the
// channel between them.

// routerFrontCertName is where a front started with --tls writes the
// certificate a client would be told to trust. The key is never written.
const routerFrontCertName = "front-cert.pem"

// routerRunCommand is __router-run: the deciding process.
func routerRunCommand(e env, args []string) int {
	fs := flag.NewFlagSet("__router-run", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	dir := fs.String("dir", "", "the router's private directory")
	if fs.Parse(args) != nil || fs.NArg() != 0 || *dir == "" {
		e.errf("usage: __router-run --dir DIR")
		return exitUsage
	}
	srv, err := channel.Listen(*dir)
	if err != nil {
		e.errf("router: %v", err)
		return exitFailed
	}
	fmt.Fprintln(e.stdout, "router: answering in "+*dir)
	go func() {
		<-e.ctx.Done()
		srv.Close()
	}()
	if err := srv.Serve(core.New(core.Config{})); err != nil {
		e.errf("router: stopped: %v", err)
		return exitFailed
	}
	fmt.Fprintln(e.stdout, "router: stopped")
	return exitOK
}

// routerFrontCommand is __router-front: the forwarding process.
func routerFrontCommand(e env, args []string) int {
	fs := flag.NewFlagSet("__router-front", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	dir := fs.String("dir", "", "the router's private directory")
	port := fs.Int("port", 0, "loopback port to listen on; 0 picks a free one")
	upstream := fs.String("upstream", "https://api.anthropic.com", "the one address requests are forwarded to")
	useTLS := fs.Bool("tls", false, "serve TLS with a new loopback certificate, written to the directory")
	drain := fs.Duration("drain", 20*time.Second, "how long open requests may finish at a stop")
	if fs.Parse(args) != nil || fs.NArg() != 0 || *dir == "" {
		e.errf("usage: __router-front --dir DIR [--port N] [--upstream URL] [--tls]")
		return exitUsage
	}
	u, err := url.Parse(*upstream)
	if err != nil {
		e.errf("front: the upstream address cannot be read")
		return exitUsage
	}
	ln, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		// A port that is taken is never traded for another one.
		e.errf("front: port %d on loopback cannot be listened on", *port)
		return exitFailed
	}
	f, err := front.New(front.Config{Upstream: u, Port: ln.Addr().(*net.TCPAddr).Port}, channel.NewClient(*dir))
	if err != nil {
		ln.Close()
		e.errf("front: %v", err)
		return exitFailed
	}
	serve := func() error { return f.Serve(ln) }
	scheme := "http"
	if *useTLS {
		cert, certPEM, err := loopcert.New(time.Now())
		if err == nil {
			err = os.WriteFile(filepath.Join(*dir, routerFrontCertName), certPEM, 0o600)
		}
		if err != nil {
			ln.Close()
			e.errf("front: the loopback certificate could not be made or written")
			return exitFailed
		}
		serve, scheme = func() error { return f.ServeTLS(ln, cert) }, "https"
	}
	fmt.Fprintf(e.stdout, "front: listening on %s://%s\n", scheme, ln.Addr())
	done := make(chan error, 1)
	go func() { done <- serve() }()
	select {
	case err := <-done:
		e.errf("front: stopped: %v", err)
		return exitFailed
	case <-e.ctx.Done():
	}
	ctx, cancel := context.WithTimeout(context.Background(), *drain)
	open, cut := f.Stop(ctx)
	cancel()
	c := f.Counts()
	fmt.Fprintf(e.stdout, "front: stopped; waited for %d open requests, cut %d; decisions missed %d, not asked %d; handshakes %d, failed %d\n", open-cut, cut, c.Missed, c.Skipped, c.Handshakes, c.HandshakesFailed)
	return exitOK
}

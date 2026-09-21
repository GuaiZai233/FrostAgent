package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"FrostAgent/internal/sandbox/gateway"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:3874", "Sandbox Gateway listen address")
	token := flag.String("token", "", "X-Auth-Token secret for gateway authentication")
	workDir := flag.String("workdir", "", "Working directory for sandbox session isolation (default: OS temp dir)")
	flag.Parse()

	if *token == "" {
		*token = os.Getenv("SANDBOX_AUTH_TOKEN")
		if *token == "" {
			*token = os.Getenv("FA_SANDBOX_AUTH_TOKEN")
		}
	}

	if *workDir == "" {
		*workDir = filepath.Join(os.TempDir(), "frostagent_sandbox_gateway")
	}

	srv := gateway.NewServer(gateway.Config{
		AuthToken: *token,
		WorkDir:   *workDir,
	})

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", *addr, err)
	}
	defer ln.Close()

	fmt.Printf("[Sandbox Gateway] Listening on http://%s\n", ln.Addr().String())
	fmt.Printf("[Sandbox Gateway] WorkDir: %s\n", *workDir)
	if *token != "" {
		fmt.Println("[Sandbox Gateway] Authentication: enabled (X-Auth-Token required)")
	} else {
		fmt.Println("[Sandbox Gateway] Authentication: disabled (anonymous access allowed)")
	}
	fmt.Println("[Sandbox Gateway] Supported Profiles: go-builder, action-runtime, minimal")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\n[Sandbox Gateway] Shutting down...")
		_ = srv.Close()
		os.Exit(0)
	}()

	httpServer := &http.Server{Handler: srv.Handler()}
	if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[Sandbox Gateway] Server error: %v", err)
	}
}

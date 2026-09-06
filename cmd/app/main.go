package main

import (
	"FrostAgent/internal/frontend"
	"FrostAgent/internal/instance"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/logs"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func run() error {
	global, err := instanceconfig.Open(".env", true)
	if err != nil {
		return err
	}
	manager, err := instance.New("data", global, "eval/dialogue/dialogue.yml")
	if err != nil {
		return err
	}
	defer manager.Close()
	mux := http.NewServeMux()
	mux.Handle("/api/instances", manager)
	mux.Handle("/api/instances/", manager)
	mux.Handle("/instances/", manager)
	mux.Handle("/frostagent.v1.LogService/", manager)
	mux.Handle("/api/log-images/", manager)
	mux.Handle("/", frontend.Handler())
	listen := global.Get("LISTEN_ADDR")
	if listen == "" {
		listen = ":8080"
	}
	wsListen := global.Get("WS_LISTEN_ADDR")
	if wsListen == "" {
		wsListen = "0.0.0.0:1234"
	}
	wsMux := http.NewServeMux()
	wsMux.Handle("/instances/", instanceWebSocketHandler(manager))
	servers := []*http.Server{{Addr: listen, Handler: corsMiddleware(mux), ReadHeaderTimeout: 10 * time.Second}}
	if wsListen != listen {
		servers = append(servers, &http.Server{Addr: wsListen, Handler: wsMux, ReadHeaderTimeout: 10 * time.Second})
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errs := make(chan error, len(servers))
	for _, server := range servers {
		go func() { logs.General.Info(logs.HTTP, "listening on "+server.Addr); errs <- server.ListenAndServe() }()
	}
	select {
	case <-ctx.Done():
	case err = <-errs:
	}
	manager.Close()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, server := range servers {
		_ = server.Shutdown(shutdown)
	}
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Keep the adapter listener separate from the HTTP management surface.
func instanceWebSocketHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/instances/"), "/")
		if len(parts) != 3 || parts[1] != "ws" || (parts[2] != "onebot" && parts[2] != "astrbot") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// corsMiddleware 作为标准 http.Handler 包装器
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}

		next.ServeHTTP(w, r)
	})
}

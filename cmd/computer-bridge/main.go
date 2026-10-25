package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/computeruse/bridge"
	"github.com/GuaiZai233/FrostAgent/internal/computeruse/machine"
)

type config struct {
	InstanceID    string `json:"instance_id"`
	Listen        string `json:"listen"`
	Token         string `json:"token"`
	Certificate   string `json:"certificate"`
	Key           string `json:"key"`
	StateDir      string `json:"state_dir"`
	AuditDir      string `json:"audit_dir"`
	ProjectRoot   string `json:"project_root"`
	Git           string `json:"git"`
	MaxConcurrent int    `json:"max_concurrent"`
	RetentionDays int    `json:"audit_retention_days"`
}

func main() {
	path := flag.String("config", "", "VM-local protected JSON configuration")
	flag.Parse()
	if err := run(*path); err != nil {
		fmt.Fprintln(os.Stderr, "computer-use bridge stopped:", err)
		os.Exit(1)
	}
}
func run(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.New("cannot read bridge configuration")
	}
	var cfg config
	if json.Unmarshal(data, &cfg) != nil {
		return errors.New("invalid bridge configuration")
	}
	host, _, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return errors.New("private numeric listen address required")
	}
	ip := net.ParseIP(host)
	if ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
		return errors.New("bridge may only listen on a private or loopback IP")
	}
	desktop, err := machine.NewDesktop()
	if err != nil {
		return err
	}
	if cfg.MaxConcurrent == 0 {
		cfg.MaxConcurrent = 4
	}
	service, err := bridge.OpenWithFactory(bridge.Config{InstanceID: cfg.InstanceID, StateDir: cfg.StateDir, AuditDir: cfg.AuditDir, AuditRetentionDays: cfg.RetentionDays, MaxConcurrent: cfg.MaxConcurrent}, func() (bridge.Executor, error) {
		projects, err := machine.OpenProjects(cfg.ProjectRoot, cfg.StateDir, cfg.Git)
		if err != nil {
			return nil, errors.New("cannot open project registry")
		}
		return machine.NewExecutor(projects, desktop), nil
	})
	if err != nil {
		return err
	}
	defer service.Shutdown(context.Background())
	handler, err := bridge.NewHandler(service, cfg.Token)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	err = server.ListenAndServeTLS(cfg.Certificate, cfg.Key)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return errors.New("bridge TLS listener failed")
}

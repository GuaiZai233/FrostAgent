package frostctl

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRemoteUsesEnvironmentProxy(t *testing.T) {
	// ProxyFromEnvironment caches the process environment on first use.
	if os.Getenv("FROSTCTL_PROXY_TEST_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRemoteUsesEnvironmentProxy$", "-test.count=1")
		cmd.Env = append(os.Environ(), "FROSTCTL_PROXY_TEST_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("proxy subprocess: %v\n%s", err, output)
		}
		return
	}
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-secret" {
			t.Error("backend did not receive the saved credential")
		}
		w.Write([]byte(`{"success":true}`))
	}))
	defer backend.Close()
	connected := make(chan bool, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "example.com:443" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected proxy request: %s %s", r.Method, r.Host)
		}
		upstream, err := net.DialTimeout("tcp", backend.Listener.Addr().String(), time.Second)
		if err != nil {
			t.Error(err)
			return
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			t.Error(err)
			return
		}
		defer connection.Close()
		defer upstream.Close()
		io.WriteString(connection, "HTTP/1.1 200 Connection Established\r\n\r\n")
		connected <- true
		go io.Copy(upstream, connection)
		io.Copy(connection, upstream)
	}))
	defer proxy.Close()
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	t.Setenv("FROSTCTL_CONFIG_DIR", t.TempDir())
	_, err := withRemotes(context.Background(), func(s *remoteStore) (any, error) {
		s.Entries["lab"] = Remote{Name: "lab", URL: "https://example.com", TokenConfigured: true, Credential: "synthetic", Store: "file"}
		data, _ := json.Marshal(credential{Origin: "https://example.com:443", Token: "synthetic-secret"})
		if err := privateWrite(filepath.Join(s.dir, "synthetic.secret"), data); err != nil {
			return nil, err
		}
		return nil, s.save()
	})
	if err != nil {
		t.Fatal(err)
	}
	inv, _ := Parse([]string{"version", "--server", "--remote", "lab"})
	c, err := newClient(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	transport := c.http.Transport.(*http.Transport)
	pool := x509.NewCertPool()
	pool.AddCert(backend.Certificate())
	transport.TLSClientConfig.RootCAs = pool
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.call(ctx, "GET", "/api/version", nil, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-connected:
	default:
		t.Fatal("HTTPS request bypassed the environment proxy")
	}
	localInv, _ := Parse([]string{"version", "--server"})
	local, err := newClient(ctx, localInv)
	if err != nil {
		t.Fatal(err)
	}
	defer local.close()
	if local.http.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("local IPC must not use an environment proxy")
	}
}

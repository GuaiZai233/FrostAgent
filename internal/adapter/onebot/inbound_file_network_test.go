package onebot

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInboundFileDialRejectsNonPublicAddresses(t *testing.T) {
	blocked := []string{
		"0.0.0.0", "0.1.2.3", "10.0.0.1", "100.64.0.0", "100.64.0.1", "100.100.100.200", "100.127.255.255",
		"127.0.0.1", "169.254.169.254", "172.16.0.1", "192.0.0.8", "192.0.0.9", "192.0.2.1", "192.88.99.1", "192.168.0.1",
		"198.18.0.0", "198.18.0.1", "198.19.255.255", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255",
		"::", "::1", "::192.168.0.1", "::ffff:100.100.100.200", "::ffff:198.18.0.1", "::ffff:10.0.0.1",
		"64:ff9b::6464:64c8", "64:ff9b::808:808", "64:ff9b:1::1", "100::1", "100:0:0:1::1",
		"2001::1", "2001:1ff::1", "2001:2::1", "2001:db8::1", "2002:6464:64c8::1", "2002:808:808::1",
		"3ffe::1", "3fff::1", "3fff:fff::1", "4000::1", "5f00::1", "fc00::1", "fd00:ec2::254", "fe80::1", "fec0::1", "ff02::1",
		"2001:4860:4860::8888%fixture",
	}
	tests := []struct {
		name string
		ips  []netip.Addr
	}{
		{"invalid-address", []netip.Addr{{}}},
		{"public-first", []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("100.100.100.200")}},
		{"restricted-first", []netip.Addr{netip.MustParseAddr("100.100.100.200"), netip.MustParseAddr("8.8.8.8")}},
		{"mixed-families", []netip.Addr{netip.MustParseAddr("2606:4700:4700::1111"), netip.MustParseAddr("198.18.0.1")}},
	}
	for _, raw := range blocked {
		tests = append(tests, struct {
			name string
			ips  []netip.Addr
		}{raw, []netip.Addr{netip.MustParseAddr(raw)}})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lookups, dials := 0, 0
			client := newInboundFileHTTPClient()
			defer client.CloseIdleConnections()
			transport := client.Transport.(*http.Transport)
			transport.DialContext = newInboundFileDialContext(
				func(_ context.Context, network, host string) ([]netip.Addr, error) {
					lookups++
					if network != "ip" || host != "multimedia.nt.qq.com.cn" {
						t.Errorf("unexpected DNS request: %s %s", network, host)
					}
					return test.ips, nil
				},
				func(context.Context, string, string) (net.Conn, error) {
					dials++
					return nil, errors.New("network access forbidden in this test")
				},
			)
			c := &wsConnection{fileHTTPClient: client}
			if _, err := c.downloadInboundFile(context.Background(), fixtureFileURL, 10); err == nil || lookups != 1 || dials != 0 {
				t.Fatalf("restricted DNS answer reached dial: err=%v lookups=%d dials=%d", err, lookups, dials)
			}
		})
	}
}

func TestInboundFileDialPinsPublicAddresses(t *testing.T) {
	for _, raw := range []string{
		"8.8.8.8", "1.1.1.1", "100.63.255.255", "100.128.0.0", "198.17.255.255", "198.20.0.0", "::ffff:8.8.8.8",
		"2001:200::1", "2001:4860:4860::8888", "240e::1", "2606:4700:4700::1111", "3fff:1000::1",
	} {
		t.Run(raw, func(t *testing.T) {
			lookups, dials := 0, 0
			local, remote := net.Pipe()
			defer local.Close()
			defer remote.Close()
			dial := newInboundFileDialContext(
				func(_ context.Context, network, host string) ([]netip.Addr, error) {
					lookups++
					if network != "ip" || host != "multimedia.nt.qq.com.cn" {
						t.Errorf("unexpected DNS request: %s %s", network, host)
					}
					return []netip.Addr{netip.MustParseAddr(raw)}, nil
				},
				func(_ context.Context, network, address string) (net.Conn, error) {
					dials++
					if network != "tcp" || address != net.JoinHostPort(netip.MustParseAddr(raw).Unmap().String(), "443") {
						t.Errorf("dial did not pin the validated address: %s %s", network, address)
					}
					return local, nil
				},
			)
			conn, err := dial(context.Background(), "tcp", "multimedia.nt.qq.com.cn:443")
			if err != nil || conn != local || lookups != 1 || dials != 1 {
				t.Fatalf("public endpoint rejected: err=%v lookups=%d dials=%d", err, lookups, dials)
			}
		})
	}
	t.Run("fallback uses the original DNS result", func(t *testing.T) {
		local, remote := net.Pipe()
		defer local.Close()
		defer remote.Close()
		lookups := 0
		var targets []string
		dial := newInboundFileDialContext(func(context.Context, string, string) ([]netip.Addr, error) {
			lookups++
			if lookups > 1 {
				return []netip.Addr{netip.MustParseAddr("100.100.100.200")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("2606:4700:4700::1111")}, nil
		}, func(_ context.Context, _ string, target string) (net.Conn, error) {
			targets = append(targets, target)
			if len(targets) == 1 {
				return nil, errors.New("first address unavailable")
			}
			return local, nil
		})
		conn, err := dial(context.Background(), "tcp", "multimedia.nt.qq.com.cn:443")
		if err != nil || conn != local || lookups != 1 || !reflect.DeepEqual(targets, []string{"8.8.8.8:443", "[2606:4700:4700::1111]:443"}) {
			t.Fatalf("fallback resolved again or lost IP pinning: err=%v lookups=%d targets=%v", err, lookups, targets)
		}
	})
}

func TestInboundFileRedirectRejectsRestrictedDNSAnswer(t *testing.T) {
	client := newInboundFileHTTPClient()
	defer client.CloseIdleConnections()
	var hosts, targets []string
	served := make(chan error, 1)
	client.Transport.(*http.Transport).DialContext = newInboundFileDialContext(
		func(_ context.Context, _ string, host string) ([]netip.Addr, error) {
			hosts = append(hosts, host)
			if host == "multimedia.nt.qq.com.cn" {
				return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("100.100.100.200")}, nil
		},
		func(_ context.Context, _ string, target string) (net.Conn, error) {
			targets = append(targets, target)
			local, remote := net.Pipe()
			go func() {
				defer remote.Close()
				_ = remote.SetDeadline(time.Now().Add(3 * time.Second))
				req, err := http.ReadRequest(bufio.NewReader(remote))
				if err != nil {
					served <- err
					return
				}
				defer req.Body.Close()
				_, err = fmt.Fprint(remote, "HTTP/1.1 302 Found\r\nLocation: http://gchat.qpic.cn/redirect\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
				served <- err
			}()
			return local, nil
		},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c := &wsConnection{fileHTTPClient: client}
	_, err := c.downloadInboundFile(ctx, "http://multimedia.nt.qq.com.cn/start", 10)
	if err == nil || !reflect.DeepEqual(hosts, []string{"multimedia.nt.qq.com.cn", "gchat.qpic.cn"}) || !reflect.DeepEqual(targets, []string{"8.8.8.8:80"}) {
		t.Fatalf("redirect reached restricted address: err=%v hosts=%v targets=%v", err, hosts, targets)
	}
	select {
	case err := <-served:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("redirect fixture did not finish")
	}
}

func TestInboundFileDialRejectsUnresolvedDestination(t *testing.T) {
	for _, mode := range []string{"invalid-address", "empty-answer", "lookup-error"} {
		t.Run(mode, func(t *testing.T) {
			dials := 0
			dial := newInboundFileDialContext(func(context.Context, string, string) ([]netip.Addr, error) {
				if mode == "lookup-error" {
					return nil, errors.New("synthetic DNS failure")
				}
				return nil, nil
			}, func(context.Context, string, string) (net.Conn, error) {
				dials++
				return nil, errors.New("unexpected dial")
			})
			address := "multimedia.nt.qq.com.cn:443"
			if strings.HasPrefix(mode, "invalid-") {
				address = "missing-port"
			}
			if _, err := dial(context.Background(), "tcp", address); err == nil || dials != 0 {
				t.Fatalf("unresolved destination reached dial: err=%v dials=%d", err, dials)
			}
		})
	}
}

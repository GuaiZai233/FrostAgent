//go:build !linux && !windows

package managementipc

import (
	"context"
	"errors"
	"net"
)

const defaultEndpoint = ""

func Listen(string) (net.Listener, error) {
	return nil, errors.New("local management requires Linux or Windows")
}
func Dial(context.Context, string) (net.Conn, error) {
	return nil, errors.New("local management requires Linux or Windows; use --remote")
}

//go:build !windows

package machine

import "errors"

func NewDesktop() (Desktop, error) { return nil, errors.New("interactive desktop requires Windows") }

//go:build !windows

package modelrouter

import "fmt"

func readWindowsCredential(string) (string, bool, error) {
	return "", false, fmt.Errorf("Windows Credential Manager 仅支持 Windows")
}

func writeWindowsCredential(string, string) error {
	return fmt.Errorf("Windows Credential Manager 仅支持 Windows")
}

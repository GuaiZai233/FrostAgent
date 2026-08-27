//go:build windows

package modelrouter

import (
	"errors"

	"github.com/danieljoos/wincred"
)

func readWindowsCredential(endpointID string) (string, bool, error) {
	credential, err := wincred.GetGenericCredential(windowsCredentialTarget(endpointID))
	if errors.Is(err, wincred.ErrElementNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(credential.CredentialBlob), true, nil
}

func writeWindowsCredential(endpointID, apiKey string) error {
	credential := wincred.NewGenericCredential(windowsCredentialTarget(endpointID))
	if apiKey == "" {
		err := credential.Delete()
		if errors.Is(err, wincred.ErrElementNotFound) {
			return nil
		}
		return err
	}
	credential.UserName = "FrostAgent"
	credential.Comment = "FrostAgent model router API key"
	credential.CredentialBlob = []byte(apiKey)
	return credential.Write()
}

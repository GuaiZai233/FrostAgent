package instanceconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAccessFailureIsNotRepairableParseFailure(t *testing.T) {
	dir := t.TempDir()
	inaccessible, err := Open(dir, false)
	if err == nil || inaccessible.AccessError() == nil {
		t.Fatal("directory accepted as config")
	}
	if _, err = inaccessible.Raw(); err == nil {
		t.Fatal("access failure exposed raw editor")
	}
	if err = inaccessible.Replace("BOT_NAME=x\n"); err == nil {
		t.Fatal("access failure allowed overwrite")
	}
	path := filepath.Join(dir, "malformed.env")
	if err = os.WriteFile(path, []byte("BROKEN=\"unterminated"), 0600); err != nil {
		t.Fatal(err)
	}
	malformed, err := Open(path, false)
	if err == nil || malformed.AccessError() != nil {
		t.Fatal("parse error was classified as access error")
	}
	if raw, err := malformed.Raw(); err != nil || raw == "" {
		t.Fatal("malformed config not available for repair")
	}
	if err = malformed.Replace("BOT_NAME=repaired\n"); err != nil {
		t.Fatal(err)
	}
	if malformed.Error() != nil || malformed.Get("BOT_NAME") != "repaired" {
		t.Fatal("repair not applied")
	}
}

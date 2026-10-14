package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"strings"
	"testing"
	"time"
)

func TestSandboxBundlePreservesFilesAndAvoidsTransferNameCollision(t *testing.T) {
	files := map[string][]byte{
		"SKILL.md":                    []byte("---\nname: sample\ndescription: Sample\n---\nRun scripts/task.py"),
		"scripts/task.py":             []byte("print('synthetic')"),
		"assets/image.bin":            {0, 1, 255},
		".frostagent-transfer-0":      []byte("legitimate resource"),
		".frostagent-transfer-1/file": []byte("legitimate directory"),
	}
	pkg, err := ParsePackage(files, "")
	if err != nil {
		t.Fatal(err)
	}
	backend := &recordingSandbox{}
	if _, _, err := Run(context.Background(), backend, "synthetic-session", pkg, "python3 scripts/task.py", 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if len(backend.calls) != 4 {
		t.Fatalf("unexpected request count: %d", len(backend.calls))
	}
	transfer := backend.calls[1].Command
	if !strings.HasSuffix(transfer, "'.frostagent-transfer-2'") {
		t.Fatal("transfer file conflicts with bundled resources")
	}
	parts := strings.Split(transfer, "'")
	if len(parts) < 4 {
		t.Fatal("invalid transfer command")
	}
	data, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != len(files) {
		t.Fatal("missing sandbox resources")
	}
	for _, member := range reader.File {
		stream, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(stream)
		stream.Close()
		if err != nil || !bytes.Equal(data, files[member.Name]) {
			t.Fatalf("resource changed: %s %v", member.Name, err)
		}
	}
}

func TestPackageRejectsFileDirectoryConflict(t *testing.T) {
	_, err := ParsePackage(map[string][]byte{
		"SKILL.md": []byte("---\nname: sample\ndescription: Sample\n---\nInstructions"),
		"scripts":  []byte("file"), "scripts/task.py": []byte("script"),
	}, "")
	if err == nil {
		t.Fatal("file/directory conflict accepted")
	}
}

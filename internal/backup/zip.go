package backup

import (
	"FrostAgent/internal/storage"
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BuildInstanceZIP returns a self-contained backup without creating a server
// side archive. Sticker bytes are separate members, never embedded in JSON.
func BuildInstanceZIP(db *storage.DB, instanceID, imageDir string) ([]byte, error) {
	settings, err := ExportSettings(db, instanceID)
	if err != nil {
		return nil, err
	}
	memories, err := ExportMemories(db, instanceID)
	if err != nil {
		return nil, err
	}
	summaries, err := ExportSummaries(db, instanceID)
	if err != nil {
		return nil, err
	}
	stickers, err := ExportStickers(db, instanceID, imageDir)
	if err != nil {
		return nil, err
	}

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	manifest := Manifest{FormatVersion: FormatVersion, Kind: "instance", ExportedAt: time.Now().UTC(), SecretNotice: SecretNotice}
	for _, file := range []struct {
		name string
		data any
	}{
		{"manifest.json", manifest},
		{"setting.json", settings},
		{"memory.json", memories},
		{"group_summaries.json", summaries},
		{"sticker/metadata.json", stickers},
	} {
		if err := addJSON(writer, file.name, file.data); err != nil {
			writer.Close()
			return nil, err
		}
	}
	seen := make(map[string]bool, len(stickers.Entries))
	for _, entry := range stickers.Entries {
		name := entry.FileName
		if name == "" || name == "." || name == ".." || filepath.Base(name) != name ||
			strings.ContainsAny(name, `/\`) {
			writer.Close()
			return nil, fmt.Errorf("invalid sticker filename %q", name)
		}
		if seen[strings.ToLower(name)] {
			writer.Close()
			return nil, fmt.Errorf("duplicate sticker filename %q", name)
		}
		seen[strings.ToLower(name)] = true
		path := filepath.Join(imageDir, name)
		info, err := os.Lstat(path)
		if err != nil {
			writer.Close()
			return nil, fmt.Errorf("read sticker %q: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			writer.Close()
			return nil, fmt.Errorf("sticker %q is not a regular file", name)
		}
		file, err := os.Open(path)
		if err != nil {
			writer.Close()
			return nil, err
		}
		member, err := writer.Create("sticker/files/" + name)
		if err == nil {
			_, err = io.Copy(member, file)
		}
		closeErr := file.Close()
		if err != nil {
			writer.Close()
			return nil, err
		}
		if closeErr != nil {
			writer.Close()
			return nil, closeErr
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func addJSON(writer *zip.Writer, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	member, err := writer.Create(name)
	if err != nil {
		return err
	}
	_, err = member.Write(append(data, '\n'))
	return err
}

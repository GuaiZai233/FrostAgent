package backup

import (
	"FrostAgent/internal/sticker"
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
	budget := zipBudget{limit: MaxInstanceZIPBytes}
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
		if err := addJSON(writer, &budget, file.name, file.data); err != nil {
			writer.Close()
			return nil, err
		}
	}
	if err := addStickerFiles(writer, &budget, stickers.Entries, imageDir); err != nil {
		writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if buffer.Len() > MaxInstanceZIPBytes {
		return nil, fmt.Errorf("instance ZIP exceeds size limit")
	}
	return buffer.Bytes(), nil
}

func BuildStickerZIP(db *storage.DB, instanceID, imageDir string) ([]byte, error) {
	stickers, err := ExportStickers(db, instanceID, imageDir)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	budget := zipBudget{limit: MaxInstanceZIPBytes}
	manifest := Manifest{FormatVersion: FormatVersion, Kind: "stickers", ExportedAt: time.Now().UTC(), SecretNotice: SecretNotice}
	if err := addJSON(writer, &budget, "manifest.json", manifest); err != nil {
		writer.Close()
		return nil, err
	}
	if err := addJSON(writer, &budget, "sticker/metadata.json", stickers); err != nil {
		writer.Close()
		return nil, err
	}
	if err := addStickerFiles(writer, &budget, stickers.Entries, imageDir); err != nil {
		writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if buffer.Len() > MaxInstanceZIPBytes {
		return nil, fmt.Errorf("sticker ZIP exceeds size limit")
	}
	return buffer.Bytes(), nil
}

type zipBudget struct {
	limit    uint64
	expanded uint64
}

func (b *zipBudget) reserve(size uint64) error {
	if size > b.limit-b.expanded {
		return fmt.Errorf("ZIP expands beyond size limit")
	}
	b.expanded += size
	return nil
}

func addStickerFiles(writer *zip.Writer, budget *zipBudget, entries []sticker.Entry, imageDir string) error {
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		name := entry.FileName
		if name == "" || name == "." || name == ".." || filepath.Base(name) != name ||
			strings.ContainsAny(name, `/\`) {
			return fmt.Errorf("invalid sticker filename %q", name)
		}
		if seen[strings.ToLower(name)] {
			return fmt.Errorf("duplicate sticker filename %q", name)
		}
		seen[strings.ToLower(name)] = true
		path := filepath.Join(imageDir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("read sticker %q: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("sticker %q is not a regular file", name)
		}
		if info.Size() < 0 || uint64(info.Size()) > budget.limit-budget.expanded {
			return fmt.Errorf("sticker %q exceeds ZIP size limit", name)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		member, err := writer.Create("sticker/files/" + name)
		if err == nil {
			var copied int64
			copied, err = io.Copy(member, io.LimitReader(file, int64(budget.limit-budget.expanded)+1))
			if err == nil {
				err = budget.reserve(uint64(copied))
			}
		}
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func addJSON(writer *zip.Writer, budget *zipBudget, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := budget.reserve(uint64(len(data) + 1)); err != nil {
		return err
	}
	member, err := writer.Create(name)
	if err != nil {
		return err
	}
	_, err = member.Write(append(data, '\n'))
	return err
}

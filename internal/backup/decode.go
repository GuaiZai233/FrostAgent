package backup

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const MaxInstanceZIPBytes = 512 << 20

type DecodedInstance struct {
	Manifest  Manifest
	Settings  Settings
	Memories  Memories
	Summaries Summaries
	Stickers  Stickers
	Images    map[string][]byte
}

// DecodeInstanceZIP accepts only the documented archive members and checks
// every sticker byte stream before any instance is created.
func DecodeInstanceZIP(archive []byte) (DecodedInstance, error) {
	var decoded DecodedInstance
	if len(archive) > MaxInstanceZIPBytes {
		return decoded, fmt.Errorf("instance ZIP exceeds size limit")
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return decoded, err
	}
	files := make(map[string][]byte, len(reader.File))
	var total uint64
	for _, member := range reader.File {
		name := member.Name
		if _, duplicate := files[name]; duplicate || member.FileInfo().IsDir() {
			return decoded, fmt.Errorf("duplicate or invalid ZIP member %q", name)
		}
		if name != "manifest.json" && name != "setting.json" && name != "memory.json" &&
			name != "group_summaries.json" && name != "sticker/metadata.json" {
			if !strings.HasPrefix(name, "sticker/files/") || !validStickerFilename(strings.TrimPrefix(name, "sticker/files/")) {
				return decoded, fmt.Errorf("unexpected ZIP member %q", name)
			}
		}
		if member.UncompressedSize64 > MaxInstanceZIPBytes-total {
			return decoded, fmt.Errorf("instance ZIP expands beyond size limit")
		}
		remaining := uint64(MaxInstanceZIPBytes) - total
		stream, err := member.Open()
		if err != nil {
			return decoded, err
		}
		data, err := io.ReadAll(io.LimitReader(stream, int64(remaining)+1))
		closeErr := stream.Close()
		if err != nil {
			return decoded, err
		}
		if closeErr != nil {
			return decoded, closeErr
		}
		if uint64(len(data)) > remaining {
			return decoded, fmt.Errorf("instance ZIP expands beyond size limit")
		}
		total += uint64(len(data))
		files[name] = data
	}
	for _, name := range []string{"manifest.json", "setting.json", "memory.json", "group_summaries.json", "sticker/metadata.json"} {
		if files[name] == nil {
			return decoded, fmt.Errorf("required ZIP member %q is missing", name)
		}
	}
	for _, item := range []struct {
		name  string
		value any
	}{
		{"manifest.json", &decoded.Manifest}, {"setting.json", &decoded.Settings},
		{"memory.json", &decoded.Memories}, {"group_summaries.json", &decoded.Summaries},
		{"sticker/metadata.json", &decoded.Stickers},
	} {
		if err := json.Unmarshal(files[item.name], item.value); err != nil {
			return decoded, fmt.Errorf("decode %s: %w", item.name, err)
		}
	}
	if decoded.Manifest.Kind != "instance" {
		return decoded, fmt.Errorf("ZIP is not an instance backup")
	}
	for _, version := range []int{decoded.Manifest.FormatVersion, decoded.Settings.FormatVersion,
		decoded.Memories.FormatVersion, decoded.Summaries.FormatVersion, decoded.Stickers.FormatVersion} {
		if err := ValidateFormat(version); err != nil {
			return decoded, err
		}
	}
	decoded.Images = make(map[string][]byte, len(decoded.Stickers.Entries))
	for name, data := range files {
		if strings.HasPrefix(name, "sticker/files/") {
			decoded.Images[strings.TrimPrefix(name, "sticker/files/")] = data
		}
	}
	seen := make(map[string]bool)
	for _, entry := range decoded.Stickers.Entries {
		if !validStickerFilename(entry.FileName) || seen[strings.ToLower(entry.FileName)] {
			return decoded, fmt.Errorf("invalid sticker filename %q", entry.FileName)
		}
		seen[strings.ToLower(entry.FileName)] = true
		if _, ok := decoded.Images[entry.FileName]; !ok {
			return decoded, fmt.Errorf("sticker %q has no image", entry.ID)
		}
	}
	if len(decoded.Images) != len(decoded.Stickers.Entries) {
		return decoded, fmt.Errorf("ZIP contains unreferenced sticker images")
	}
	return decoded, nil
}

package content

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"path"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

// MediaKind classifies non-image media that can be sent to an audio/video model.
type MediaKind string

const (
	MediaNone  MediaKind = ""
	MediaAudio MediaKind = "audio"
	MediaVideo MediaKind = "video"
)

var (
	audioFileExtensions = map[string]bool{
		".mp3": true, ".wav": true, ".m4a": true, ".aac": true, ".flac": true, ".ogg": true,
		".oga": true, ".opus": true, ".amr": true, ".silk": true, ".slk": true, ".aiff": true, ".aif": true, ".wma": true,
	}
	videoFileExtensions = map[string]bool{
		".mp4": true, ".m4v": true, ".mov": true, ".webm": true, ".mkv": true, ".avi": true, ".3gp": true,
	}
)

// SegmentMediaKind reports whether a OneBot segment carries audio or video:
// voice messages, video messages, and uploaded files with a media extension.
func SegmentMediaKind(seg MessageSegment) MediaKind {
	switch seg.Type {
	case "record", "audio", "voice":
		return MediaAudio
	case "video":
		return MediaVideo
	case "file":
		ext := strings.ToLower(path.Ext(dataString(seg.Data, "name", "file")))
		if audioFileExtensions[ext] {
			return MediaAudio
		}
		if videoFileExtensions[ext] {
			return MediaVideo
		}
	}
	return MediaNone
}

// ContainsMedia reports whether any segment carries audio or video.
func ContainsMedia(segments []MessageSegment) bool {
	for _, seg := range segments {
		if SegmentMediaKind(seg) != MediaNone {
			return true
		}
	}
	return false
}

// SegmentsText joins the plain text segments, mirroring what the image
// transcription request receives alongside the media.
func SegmentsText(segments []MessageSegment) string {
	var texts []string
	for _, seg := range segments {
		if seg.Type == "text" {
			if text, ok := seg.Data["text"].(string); ok {
				texts = append(texts, text)
			}
		}
	}
	return strings.Join(texts, "")
}

// SegmentInlineMedia returns bytes carried inline by the segment itself.
func SegmentInlineMedia(seg MessageSegment) ([]byte, bool, error) {
	source := dataString(seg.Data, "base64")
	if source != "" && !strings.HasPrefix(source, "base64://") && !strings.HasPrefix(source, "data:") {
		source = "base64://" + source
	}
	if source == "" {
		for _, key := range []string{"file", "url"} {
			value := dataString(seg.Data, key)
			if strings.HasPrefix(value, "base64://") || strings.HasPrefix(value, "data:") {
				source = value
				break
			}
		}
	}
	if source == "" {
		return nil, false, nil
	}
	data, err := DecodeInlineMedia(source)
	return data, true, err
}

// DecodeInlineMedia decodes a base64:// source, a Base64 data URL or bare Base64.
func DecodeInlineMedia(source string) ([]byte, error) {
	source = strings.TrimSpace(source)
	switch {
	case strings.HasPrefix(source, "base64://"):
		source = strings.TrimPrefix(source, "base64://")
	case strings.HasPrefix(source, "data:"):
		header, encoded, ok := strings.Cut(source, ",")
		if !ok || !strings.Contains(strings.ToLower(header), ";base64") {
			return nil, fmt.Errorf("媒体 data URI 不是 Base64")
		}
		source = encoded
	}
	data, err := base64.StdEncoding.DecodeString(source)
	if err != nil {
		return nil, fmt.Errorf("媒体 Base64 无效")
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("媒体内容为空")
	}
	return data, nil
}

// DetectAudioFormat sniffs an audio container and returns its input_audio
// format name. QQ's native SILK/AMR voice encodings are rejected because
// general-purpose models cannot decode them.
func DetectAudioFormat(data []byte) (string, error) {
	switch {
	case bytes.HasPrefix(data, []byte("#!SILK")) || bytes.HasPrefix(data, []byte("\x02#!SILK")):
		return "", fmt.Errorf("SILK 语音需要 OneBot 实现转码（get_record out_format）后才能读取")
	case bytes.HasPrefix(data, []byte("#!AMR")):
		return "", fmt.Errorf("AMR 语音需要 OneBot 实现转码（get_record out_format）后才能读取")
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE")):
		return "wav", nil
	case bytes.HasPrefix(data, []byte("ID3")):
		return "mp3", nil
	case len(data) >= 2 && data[0] == 0xFF && data[1]&0xF6 == 0xF0:
		return "aac", nil
	case len(data) >= 2 && data[0] == 0xFF && data[1]&0xE0 == 0xE0:
		return "mp3", nil
	case bytes.HasPrefix(data, []byte("OggS")):
		return "ogg", nil
	case bytes.HasPrefix(data, []byte("fLaC")):
		return "flac", nil
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("FORM")) && bytes.Equal(data[8:12], []byte("AIFF")):
		return "aiff", nil
	case len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")):
		return "m4a", nil
	case bytes.HasPrefix(data, []byte{0x1A, 0x45, 0xDF, 0xA3}):
		return "webm", nil
	}
	return "", fmt.Errorf("无法识别的音频格式")
}

// DetectVideoMIMEType sniffs a video container and returns its MIME type.
func DetectVideoMIMEType(data []byte) (string, error) {
	switch {
	case len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")):
		brand := string(data[8:12])
		switch {
		case strings.HasPrefix(brand, "qt"):
			return "video/quicktime", nil
		case strings.HasPrefix(brand, "3g"):
			return "video/3gpp", nil
		}
		return "video/mp4", nil
	case bytes.HasPrefix(data, []byte{0x1A, 0x45, 0xDF, 0xA3}):
		return "video/webm", nil
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("AVI ")):
		return "video/x-msvideo", nil
	}
	return "", fmt.Errorf("无法识别的视频格式")
}

// MediaContentPart converts verified media bytes into a request-local content part.
func MediaContentPart(kind MediaKind, data []byte) (core.ContentPart, error) {
	if len(data) == 0 {
		return core.ContentPart{}, fmt.Errorf("媒体内容为空")
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	switch kind {
	case MediaAudio:
		format, err := DetectAudioFormat(data)
		if err != nil {
			return core.ContentPart{}, err
		}
		return core.ContentPart{Type: string(core.ContentPartTypeAudio), InputAudio: &core.InputAudio{Data: encoded, Format: format}}, nil
	case MediaVideo:
		mimeType, err := DetectVideoMIMEType(data)
		if err != nil {
			return core.ContentPart{}, err
		}
		return core.ContentPart{Type: string(core.ContentPartTypeVideo), VideoURL: &core.VideoURL{URL: "data:" + mimeType + ";base64," + encoded}}, nil
	}
	return core.ContentPart{}, fmt.Errorf("不支持的媒体类型 %q", kind)
}

package content

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestSegmentMediaKind(t *testing.T) {
	for _, tc := range []struct {
		seg  MessageSegment
		want MediaKind
	}{
		{MessageSegment{Type: "record", Data: map[string]any{"file": "voice.amr"}}, MediaAudio},
		{MessageSegment{Type: "video", Data: map[string]any{"file": "clip.mp4"}}, MediaVideo},
		{MessageSegment{Type: "file", Data: map[string]any{"name": "Song.MP3"}}, MediaAudio},
		{MessageSegment{Type: "file", Data: map[string]any{"name": "clip.mov"}}, MediaVideo},
		{MessageSegment{Type: "file", Data: map[string]any{"name": "report.docx"}}, MediaNone},
		{MessageSegment{Type: "image", Data: map[string]any{"file": "a.png"}}, MediaNone},
	} {
		if got := SegmentMediaKind(tc.seg); got != tc.want {
			t.Errorf("%s %v: got %q want %q", tc.seg.Type, tc.seg.Data, got, tc.want)
		}
	}
}

func TestDetectAudioFormat(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"wav", []byte("RIFF\x24\x00\x00\x00WAVEfmt "), "wav"},
		{"mp3 id3", []byte("ID3\x04\x00"), "mp3"},
		{"mp3 frame", []byte{0xFF, 0xFB, 0x90, 0x00}, "mp3"},
		{"aac adts", []byte{0xFF, 0xF1, 0x50, 0x80}, "aac"},
		{"ogg", []byte("OggS\x00\x02"), "ogg"},
		{"flac", []byte("fLaC\x00"), "flac"},
		{"m4a", []byte("\x00\x00\x00\x20ftypM4A "), "m4a"},
	} {
		got, err := DetectAudioFormat(tc.data)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v", tc.name, got, err)
		}
	}
	for _, native := range [][]byte{[]byte("\x02#!SILK_V3"), []byte("#!SILK_V3"), []byte("#!AMR\n")} {
		if _, err := DetectAudioFormat(native); err == nil {
			t.Errorf("QQ native voice %q must be rejected", native)
		}
	}
}

func TestMediaContentPart(t *testing.T) {
	wav := []byte("RIFF\x24\x00\x00\x00WAVEfmt ")
	part, err := MediaContentPart(MediaAudio, wav)
	if err != nil || part.Type != "input_audio" || part.InputAudio == nil || part.InputAudio.Format != "wav" ||
		part.InputAudio.Data != base64.StdEncoding.EncodeToString(wav) {
		t.Fatalf("audio part: %+v, %v", part, err)
	}
	mp4 := []byte("\x00\x00\x00\x18ftypisom")
	part, err = MediaContentPart(MediaVideo, mp4)
	if err != nil || part.Type != "video_url" || part.VideoURL == nil ||
		part.VideoURL.URL != "data:video/mp4;base64,"+base64.StdEncoding.EncodeToString(mp4) {
		t.Fatalf("video part: %+v, %v", part, err)
	}
	if _, err := MediaContentPart(MediaVideo, []byte("not a video")); err == nil {
		t.Fatal("unknown video container must be rejected")
	}
}

func TestSegmentInlineMedia(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("audio"))
	for _, data := range []map[string]any{
		{"file": "base64://" + encoded},
		{"base64": encoded},
		{"url": "data:audio/mpeg;base64," + encoded},
	} {
		got, ok, err := SegmentInlineMedia(MessageSegment{Type: "record", Data: data}, 1024)
		if !ok || err != nil || string(got) != "audio" {
			t.Errorf("%v: got %q ok=%v err=%v", data, got, ok, err)
		}
	}
	if _, ok, _ := SegmentInlineMedia(MessageSegment{Type: "record", Data: map[string]any{"file": "voice.amr", "url": "https://multimedia.nt.qq.com.cn/x"}}, 1024); ok {
		t.Fatal("remote sources are not inline media")
	}
}

func TestInlineMediaRejectsOversizedInputBeforeDecoding(t *testing.T) {
	// Invalid Base64 proves the size check runs first: decoding would fail
	// with a different error.
	oversized := "base64://" + strings.Repeat("!", 64)
	if _, ok, err := SegmentInlineMedia(MessageSegment{Type: "record", Data: map[string]any{"file": oversized}}, 16); !ok || !errors.Is(err, ErrMediaTooLarge) {
		t.Fatalf("oversized inline media: ok=%v err=%v", ok, err)
	}
	exact := base64.StdEncoding.EncodeToString(make([]byte, 16))
	if data, err := DecodeInlineMedia(exact, 16); err != nil || len(data) != 16 {
		t.Fatalf("media at the limit: %d bytes, %v", len(data), err)
	}
	if _, err := DecodeInlineMedia(exact, 15); !errors.Is(err, ErrMediaTooLarge) {
		t.Fatalf("media over the limit: %v", err)
	}
}

package content

import (
	"testing"
)

func TestImageSubType(t *testing.T) {
	tests := []struct {
		name string
		data map[string]any
		want any
	}{
		{
			name: "nil data",
			data: nil,
			want: nil,
		},
		{
			name: "snake_case sub_type",
			data: map[string]any{"sub_type": 1},
			want: 1,
		},
		{
			name: "camelCase subType",
			data: map[string]any{"subType": 1},
			want: 1,
		},
		{
			name: "both present prefers sub_type",
			data: map[string]any{"sub_type": 1, "subType": 2},
			want: 1,
		},
		{
			name: "neither present",
			data: map[string]any{"url": "https://example.com/img.png"},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ImageSubType(tt.data)
			if got != tt.want {
				t.Errorf("ImageSubType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeMessageSegment(t *testing.T) {
	t.Run("normalizes LuckyLillia subType to sub_type", func(t *testing.T) {
		seg := MessageSegment{
			Type: "image",
			Data: map[string]any{
				"subType": 1,
				"url":     "https://example.com/pic.png",
			},
		}
		norm := NormalizeMessageSegment(seg)
		if norm.Data["sub_type"] != 1 {
			t.Errorf("expected sub_type=1, got %v", norm.Data["sub_type"])
		}
		if norm.Data["subType"] != 1 {
			t.Errorf("expected subType=1, got %v", norm.Data["subType"])
		}
	})

	t.Run("normalizes top-level SubType struct field", func(t *testing.T) {
		seg := MessageSegment{
			Type:    "image",
			SubType: "1",
		}
		norm := NormalizeMessageSegment(seg)
		if norm.Data["sub_type"] != "1" {
			t.Errorf("expected sub_type='1', got %v", norm.Data["sub_type"])
		}
		if norm.Data["subType"] != "1" {
			t.Errorf("expected subType='1', got %v", norm.Data["subType"])
		}
	})

	t.Run("normalizes LuckyLillia camelCase emoji metadata", func(t *testing.T) {
		seg := MessageSegment{
			Type: "mface",
			Data: map[string]any{
				"emojiId":        "12345",
				"emojiPackageId": "67890",
			},
		}
		norm := NormalizeMessageSegment(seg)
		if norm.Data["emoji_id"] != "12345" {
			t.Errorf("expected emoji_id='12345', got %v", norm.Data["emoji_id"])
		}
		if norm.Data["emojiId"] != "12345" {
			t.Errorf("expected emojiId='12345', got %v", norm.Data["emojiId"])
		}
		if norm.Data["emoji_package_id"] != "67890" {
			t.Errorf("expected emoji_package_id='67890', got %v", norm.Data["emoji_package_id"])
		}
		if norm.Data["emojiPackageId"] != "67890" {
			t.Errorf("expected emojiPackageId='67890', got %v", norm.Data["emojiPackageId"])
		}
	})

	t.Run("normalizes top-level Url and FileSize", func(t *testing.T) {
		seg := MessageSegment{
			Type:     "file",
			Url:      "https://example.com/test.zip",
			FileSize: 1024,
		}
		norm := NormalizeMessageSegment(seg)
		if norm.Data["url"] != "https://example.com/test.zip" {
			t.Errorf("expected url in Data, got %v", norm.Data["url"])
		}
		if norm.Data["file_size"] != int64(1024) {
			t.Errorf("expected file_size in Data, got %v", norm.Data["file_size"])
		}
	})
}

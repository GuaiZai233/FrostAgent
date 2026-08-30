package content

import "testing"

func TestMarketFaceSegmentSource(t *testing.T) {
	tests := []struct {
		name   string
		seg    MessageSegment
		source string
	}{
		{
			name: "NapCat inbound image",
			seg: MessageSegment{Type: "image", Data: map[string]any{
				"emoji_id": "abcdef",
				"url":      "https://gxh.vip.qq.com/club/item/parcel/item/ab/abcdef/raw300.gif",
			}},
			source: "https://gxh.vip.qq.com/club/item/parcel/item/ab/abcdef/raw300.gif",
		},
		{
			name: "native mface",
			seg: MessageSegment{Type: "mface", Data: map[string]any{
				"emoji_id": "123456",
			}},
			source: "https://gxh.vip.qq.com/club/item/parcel/item/12/123456/raw300.gif",
		},
		{
			name: "native mface base64",
			seg: MessageSegment{Type: "mface", Data: map[string]any{
				"emoji_id": "123456",
				"file":     "base64://bWZhY2U=",
			}},
			source: "base64://bWZhY2U=",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !IsMarketFaceSegment(test.seg) {
				t.Fatal("expected marketplace sticker segment")
			}
			if source := SegmentImageSource(test.seg); source != test.source {
				t.Fatalf("source = %q, want %q", source, test.source)
			}
		})
	}
}

func TestIsContainImageRecognizesMFace(t *testing.T) {
	if !IsContainImage([]MessageSegment{{Type: "mface", Data: map[string]any{"emoji_id": "123456"}}}) {
		t.Fatal("expected mface to be treated as image content")
	}
}

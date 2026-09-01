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

func TestTrustedQQImageURL(t *testing.T) {
	tests := []struct {
		url     string
		allowed bool
	}{
		{url: "https://gchat.qpic.cn/download?file=test", allowed: true},
		{url: "https://multimedia.nt.qq.com.cn/download?file=test", allowed: true},
		{url: "https://gxh.vip.qq.com/club/item/parcel/item/12/123456/raw300.gif", allowed: true},
		{url: "http://gchat.qpic.cn/download", allowed: false},
		{url: "https://gchat.qpic.cn.evil.example/download", allowed: false},
		{url: "https://gchat.qpic.cn@evil.example/download", allowed: false},
		{url: "https://gchat.qpic.cn:8443/download", allowed: false},
		{url: "http://127.0.0.1/private", allowed: false},
	}

	for _, test := range tests {
		if got := trustedQQImageURL.MatchString(test.url); got != test.allowed {
			t.Errorf("trustedQQImageURL.MatchString(%q) = %v, want %v", test.url, got, test.allowed)
		}
	}
}

func TestImageSourceToBase64RejectsUntrustedURL(t *testing.T) {
	if _, err := imageSourceToBase64("http://127.0.0.1/private"); err == nil {
		t.Fatal("expected untrusted image URL to be rejected")
	}
}

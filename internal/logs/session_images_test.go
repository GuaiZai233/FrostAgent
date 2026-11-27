package logs

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var testPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAHEC7cQAAAABJRU5ErkJggg==")

func TestSessionImageServedThroughLogImageHandler(t *testing.T) {
	store := New("", "test", 10)
	key := SessionImageKey("https://example.invalid/synthetic.png")
	if store.HasSessionImage(key) {
		t.Fatal("image cached before it was put")
	}
	store.PutSessionImage(key, testPNG)
	if !store.HasSessionImage(key) {
		t.Fatal("image not cached")
	}
	rec := httptest.NewRecorder()
	store.ImageHandler(rec, httptest.NewRequest(http.MethodGet, LogImagePathPrefix+key, nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || rec.Body.Len() != len(testPNG) {
		t.Fatalf("unexpected response: %d %q %d", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}
	missing := httptest.NewRecorder()
	store.ImageHandler(missing, httptest.NewRequest(http.MethodGet, LogImagePathPrefix+strings.Repeat("0", 64), nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown key served: %d", missing.Code)
	}
}

func TestSessionImageRejectsNonImagesAndEvictsOldest(t *testing.T) {
	store := New("", "test", 10)
	store.PutSessionImage("text", []byte("not an image"))
	if store.HasSessionImage("text") {
		t.Fatal("non-image data was retained")
	}
	store.PutSessionImage("oversized", append(append([]byte(nil), testPNG...), make([]byte, maxSessionImageEach)...))
	if store.HasSessionImage("oversized") {
		t.Fatal("oversized image was retained")
	}
	for i := 0; i <= maxSessionImages; i++ {
		store.PutSessionImage(SessionImageKey(string(rune('a'+i%26))+strings.Repeat("x", i)), testPNG)
	}
	if store.HasSessionImage(SessionImageKey("a")) {
		t.Fatal("oldest image was not evicted past the entry limit")
	}
}

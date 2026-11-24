package richimage

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/go-fonts/stix/stix2mathregular"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// ErrNoCJKFont means the content needs glyphs that no loaded font provides.
var ErrNoCJKFont = errors.New("没有可用的中日韩字体，请安装思源黑体(Noto Sans CJK)或设置 FROSTAGENT_RICH_IMAGE_FONT")

// FontEnvKey names the optional environment variable holding a font file path.
const FontEnvKey = "FROSTAGENT_RICH_IMAGE_FONT"

// Source Han Sans is published as Noto Sans CJK in most distributions.
func defaultFontCandidates() []string {
	list := []string{
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/opentype/source-han-sans/SourceHanSansSC-Regular.otf",
		"/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/google-noto-cjk/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/google-noto-sans-cjk-fonts/NotoSansCJK-Regular.ttc",
		"/Library/Fonts/SourceHanSansSC-Regular.otf",
	}
	if runtime.GOOS == "windows" {
		list = append(list,
			`C:\Windows\Fonts\SourceHanSansSC-Regular.otf`,
			`C:\Windows\Fonts\NotoSansCJKsc-Regular.otf`,
			`C:\Windows\Fonts\msyh.ttc`,
		)
	}
	return list
}

type fontSet struct {
	text *sfnt.Font
	mono *sfnt.Font
	// Latin faces used by math layout (italic variables, bold vectors).
	goReg, goIt, goBold *sfnt.Font
	// math is STIX Two Math, the fallback for operators and relations that
	// neither the body font nor the Go fonts provide.
	math *sfnt.Font
	// cjk reports whether text carries CJK glyphs.
	cjk bool
}

var (
	fontMu    sync.Mutex
	fontCache = map[string]*fontSet{}
)

// loadFonts resolves the text font. override may be empty.
func loadFonts(override string) (*fontSet, error) {
	fontMu.Lock()
	defer fontMu.Unlock()
	if set, ok := fontCache[override]; ok {
		return set, nil
	}
	var text *sfnt.Font
	cjk := false
	candidates := defaultFontCandidates()
	if override != "" {
		candidates = []string{override}
	}
	var lastErr error
	for _, path := range candidates {
		f, err := parseFontFile(path)
		if err != nil {
			if override != "" {
				lastErr = fmt.Errorf("读取字体 %q 失败: %w", path, err)
			}
			continue
		}
		text, cjk = f, true
		break
	}
	if override != "" && text == nil {
		return nil, lastErr
	}
	if text == nil {
		f, err := sfnt.Parse(goregular.TTF)
		if err != nil {
			return nil, err
		}
		text = f
	}
	mono, err := sfnt.Parse(gomono.TTF)
	if err != nil {
		return nil, err
	}
	set := &fontSet{text: text, mono: mono, cjk: cjk}
	for _, f := range []struct {
		dst  **sfnt.Font
		data []byte
	}{{&set.goReg, goregular.TTF}, {&set.goIt, goitalic.TTF}, {&set.goBold, gobold.TTF}, {&set.math, stix2mathregular.TTF}} {
		parsed, err := sfnt.Parse(f.data)
		if err != nil {
			return nil, err
		}
		*f.dst = parsed
	}
	fontCache[override] = set
	return set, nil
}

func parseFontFile(path string) (*sfnt.Font, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) >= 4 && string(data[:4]) == "ttcf" {
		collection, err := sfnt.ParseCollection(data)
		if err != nil {
			return nil, err
		}
		var first *sfnt.Font
		for i := 0; i < collection.NumFonts(); i++ {
			f, err := collection.Font(i)
			if err != nil {
				continue
			}
			if first == nil {
				first = f
			}
			// Prefer the Simplified Chinese face of Noto Sans CJK.
			if name, err := f.Name(nil, sfnt.NameIDFull); err == nil && strings.Contains(name, " SC") {
				return f, nil
			}
		}
		if first == nil {
			return nil, fmt.Errorf("字体集合为空")
		}
		return first, nil
	}
	return sfnt.Parse(data)
}

// hasGlyph reports whether the font maps r to a real glyph.
func hasGlyph(f *sfnt.Font, r rune) bool {
	var buf sfnt.Buffer
	idx, err := f.GlyphIndex(&buf, r)
	return err == nil && idx != 0
}

type faceKey struct {
	f    *sfnt.Font
	size float64
}

// faces caches opentype faces per size for one render.
type faces struct {
	set   *fontSet
	cache map[faceKey]font.Face
}

func newFaces(set *fontSet) *faces {
	return &faces{set: set, cache: map[faceKey]font.Face{}}
}

func (fs *faces) get(mono bool, size float64) (font.Face, error) {
	if mono {
		return fs.faceOf(fs.set.mono, size)
	}
	return fs.faceOf(fs.set.text, size)
}

func (fs *faces) faceOf(src *sfnt.Font, size float64) (font.Face, error) {
	key := faceKey{src, size}
	if face, ok := fs.cache[key]; ok {
		return face, nil
	}
	face, err := opentype.NewFace(src, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil, err
	}
	fs.cache[key] = face
	return face, nil
}

func (fs *faces) close() {
	for _, face := range fs.cache {
		_ = face.Close()
	}
}

// checkText fails when text needs glyphs the text font lacks.
func (fs *faces) checkText(text string) error {
	for _, r := range text {
		if r < 0x2E80 {
			continue
		}
		if !fs.set.cjk || !hasGlyph(fs.set.text, r) {
			return ErrNoCJKFont
		}
	}
	return nil
}

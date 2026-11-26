package richimage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// ErrNoCJKFont means the content needs glyphs that no loaded font provides.
var ErrNoCJKFont = errors.New("没有可用的中日韩字体：需要联网自动下载霞鹜臻楷，或安装思源黑体(Noto Sans CJK)，或设置 FROSTAGENT_RICH_IMAGE_FONT")

// FontEnvKey names the optional environment variable holding a font file path
// that replaces the body font.
const FontEnvKey = "FROSTAGENT_RICH_IMAGE_FONT"

// systemFontCandidates are tried when the pinned body font is unavailable.
func systemFontCandidates() []string {
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
	// Latin faces used by math layout when STIX is unavailable.
	goReg, goIt, goBold *sfnt.Font
	// math is STIX Two Math; nil when it could not be fetched, in which case
	// formulas fall back to the Go fonts and unsupported symbols are shown as source.
	math *sfnt.Font
	// emoji is Noto Emoji (monochrome outlines); nil when unavailable.
	emoji *sfnt.Font
	// cjk reports whether text carries CJK glyphs.
	cjk bool
	// fetchErr records why a pinned font could not be obtained.
	fetchErr error
}

type fontKey struct{ override, dir string }

var (
	fontMu    sync.Mutex
	fontCache = map[fontKey]*fontSet{}
	// pinnedFonts keeps parsed pinned fonts per directory: the body font alone is ~17 MB.
	pinnedFonts = map[string]*sfnt.Font{}
)

func pinnedFont(ctx context.Context, rf remoteFont, dir string) (*sfnt.Font, error) {
	key := dir + "\x00" + rf.name
	if f, ok := pinnedFonts[key]; ok {
		return f, nil
	}
	fontMu.Unlock() // never hold the lock while waiting on the network
	data, err := ensureFont(ctx, rf, dir)
	fontMu.Lock()
	if err != nil {
		return nil, err
	}
	if f, ok := pinnedFonts[key]; ok {
		return f, nil
	}
	f, err := sfnt.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("解析字体 %s 失败: %w", rf.name, err)
	}
	pinnedFonts[key] = f
	return f, nil
}

// loadFonts resolves the fonts. override replaces the body font; dir is the
// pinned-font cache directory (empty selects the default).
func loadFonts(ctx context.Context, override, dir string) (*fontSet, error) {
	fontMu.Lock()
	defer fontMu.Unlock()
	dir = fontDir(dir)
	key := fontKey{override, dir}
	if set, ok := fontCache[key]; ok {
		return set, nil
	}
	set := &fontSet{}
	var fetchErrs []error
	if override != "" {
		f, err := parseFontFile(override)
		if err != nil {
			return nil, fmt.Errorf("读取字体 %q 失败: %w", override, err)
		}
		set.text, set.cjk = f, true
	} else {
		if f, err := pinnedFont(ctx, lxgwZhenKai, dir); err == nil {
			set.text, set.cjk = f, true
		} else {
			fetchErrs = append(fetchErrs, err)
		}
		for _, path := range systemFontCandidates() {
			if set.text != nil {
				break
			}
			if f, err := parseFontFile(path); err == nil {
				set.text, set.cjk = f, true
			}
		}
	}
	if m, err := pinnedFont(ctx, stixTwoMath, dir); err == nil {
		set.math = m
	} else {
		fetchErrs = append(fetchErrs, err)
	}
	if e, err := pinnedFont(ctx, notoEmoji, dir); err == nil {
		set.emoji = e
	} else {
		fetchErrs = append(fetchErrs, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	set.fetchErr = errors.Join(fetchErrs...)
	for _, f := range []struct {
		dst  **sfnt.Font
		data []byte
	}{{&set.goReg, goregular.TTF}, {&set.goIt, goitalic.TTF}, {&set.goBold, gobold.TTF}, {&set.mono, gomono.TTF}} {
		parsed, err := sfnt.Parse(f.data)
		if err != nil {
			return nil, err
		}
		*f.dst = parsed
	}
	if set.text == nil {
		set.text = set.goReg
	}
	// Only complete results are cached, so a transient download failure is
	// retried (subject to the cooldown) instead of sticking for the process.
	if set.fetchErr == nil {
		fontCache[key] = set
	}
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

// glyphFont returns the first loaded font that has a glyph for r: the body
// font, then STIX Two Math (symbols and arrows), then Noto Emoji.
func (fs *faces) glyphFont(r rune) (*sfnt.Font, bool) {
	if hasGlyph(fs.set.text, r) {
		return fs.set.text, true
	}
	if fs.set.math != nil && hasGlyph(fs.set.math, r) {
		return fs.set.math, true
	}
	if fs.set.emoji != nil && hasGlyph(fs.set.emoji, r) {
		return fs.set.emoji, true
	}
	return nil, false
}

// missingGlyph explains why no loaded font can draw r.
func (fs *faces) missingGlyph(r rune) error {
	if r >= 0x2E80 {
		if fs.set.fetchErr != nil {
			return fmt.Errorf("%w（%v）", ErrNoCJKFont, fs.set.fetchErr)
		}
		return ErrNoCJKFont
	}
	if fs.set.fetchErr != nil {
		return fmt.Errorf("字符 %q 没有可用字形（%v）", r, fs.set.fetchErr)
	}
	return fmt.Errorf("字符 %q 没有可用字形", r)
}

// checkText fails when some rune has no glyph in any loaded font.
func (fs *faces) checkText(text string) error {
	_, err := fs.splitRuns(text, false)
	return err
}

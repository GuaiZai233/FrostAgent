package richimage

import (
	"context"
	"image"
	"testing"
)

// Layout geometry must contain the ink it draws. These use Go-font glyphs only,
// so they run on every machine, including CI without STIX.
func TestScriptBoxCoversInk(t *testing.T) {
	fonts, err := loadFonts(context.Background(), "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fs := newFaces(fonts)
	defer fs.close()
	bigI := func() *mAtom { return &mAtom{text: "I", kind: kOp, font: fontRoman, big: true} }
	atom := func(s string) mseq {
		var out mseq
		for _, r := range s {
			out = append(out, atomFromRune(r))
		}
		return out
	}
	st := mstyle{size: 32, root: 32, display: true}
	cases := map[string]*mScripts{
		"longer upper limit": {base: bigI(), sup: atom("WWWWWWWW"), sub: atom("0")},
		"narrow lower limit": {base: bigI(), sub: atom(".")},
	}
	for name, node := range cases {
		m := &mathLayout{fs: fs, col: lightTheme.fg}
		base := m.lay(bigI(), st)
		box := m.lay(node, st)
		if m.err != nil {
			t.Fatalf("%s: %v", name, m.err)
		}
		if box.w < base.w {
			t.Errorf("%s: box width %.1f is narrower than its base %.1f", name, box.w, base.w)
		}
		const pad = 20
		img := image.NewRGBA(image.Rect(0, 0, int(box.w)+2*pad, int(box.asc+box.desc)+2*pad))
		box.draw(img, pad, box.asc+pad)
		right := 0
		for y := 0; y < img.Bounds().Dy(); y++ {
			for x := 0; x < img.Bounds().Dx(); x++ {
				if img.RGBAAt(x, y).A > 0 && x > right {
					right = x
				}
			}
		}
		if float64(right) > pad+box.w+1 {
			t.Errorf("%s: ink reaches x=%d but the box ends at %.1f", name, right, pad+box.w)
		}
	}
}

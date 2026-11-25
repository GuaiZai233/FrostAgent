package richimage

import (
	"image/color"
	"testing"

	"github.com/alecthomas/chroma/v2"
)

// Classification is font-free, so these run on every machine including CI.
func TestClassifyToken(t *testing.T) {
	cases := map[string]struct {
		typ  chroma.TokenType
		want syntaxClass
	}{
		"keyword":          {chroma.Keyword, synKeyword},
		"declaration":      {chroma.KeywordDeclaration, synKeyword},
		"constant keyword": {chroma.KeywordConstant, synKeyword},
		"string":           {chroma.LiteralString, synString},
		"string char":      {chroma.LiteralStringChar, synString},
		"number":           {chroma.LiteralNumberInteger, synNumber},
		"float":            {chroma.LiteralNumberFloat, synNumber},
		"comment":          {chroma.CommentSingle, synComment},
		"multiline":        {chroma.CommentMultiline, synComment},
		"function name":    {chroma.NameFunction, synFunction},
		"builtin":          {chroma.NameBuiltin, synBuiltin},
		"operator":         {chroma.Operator, synOperator},
		"plain name":       {chroma.NameOther, synPlain},
		"whitespace":       {chroma.TextWhitespace, synPlain},
		"punctuation":      {chroma.Punctuation, synPlain},
	}
	for name, tc := range cases {
		if got := classifyToken(tc.typ); got != tc.want {
			t.Errorf("%s: class %d, want %d", name, got, tc.want)
		}
	}
}

func TestSyntaxLexerSelection(t *testing.T) {
	for _, info := range []string{"python", "py", "Python title=x", "go", "golang", "js", "ts", "json", "bash", "sh", "rust", "yaml", "sql"} {
		if syntaxLexer(info) == nil {
			t.Errorf("%q must be highlighted", info)
		}
	}
	for _, info := range []string{"", "   ", "not-a-real-language"} {
		if syntaxLexer(info) != nil {
			t.Errorf("%q must fall back to plain text", info)
		}
	}
}

// Unknown languages and missing info strings keep the single plain colour.
func TestCodeColorsFallBackToNil(t *testing.T) {
	th := lightTheme
	if colorsOf(th, "", "x = 1") != nil {
		t.Fatal("no language must not colour tokens")
	}
	if colorsOf(th, "not-a-real-language", "MOVE 1 TO X") != nil {
		t.Fatal("unknown language must not colour tokens")
	}
}

// Every rune gets exactly one colour, so the colour slice indexes the code.
func TestCodeColorsCoverEveryRune(t *testing.T) {
	code := "def f(x):\n    return x + 1  # 注释\n"
	colors := colorsOf(lightTheme, "python", code)
	if colors == nil {
		t.Fatal("python must be highlighted")
	}
	if got, want := len(colors), len([]rune(code)); got != want {
		t.Fatalf("%d colours for %d runes", got, want)
	}
	distinct := map[color.RGBA]bool{}
	for _, c := range colors {
		distinct[c] = true
	}
	if len(distinct) < 3 {
		t.Fatalf("expected several token colours, got %d", len(distinct))
	}
}

// A language outside the original six works too, with no files copied in.
func TestRustHighlighted(t *testing.T) {
	code := "fn main() { let x = 1; }\n"
	colors := colorsOf(lightTheme, "rust", code)
	if colors == nil {
		t.Fatal("rust must be highlighted")
	}
	if colors[0] != lightTheme.syntax[synKeyword] {
		t.Error("'fn' must use the keyword colour")
	}
}

func TestGoLexerHighlights(t *testing.T) {
	code := "package main\n\nfunc main() {\n\tfmt.Println(\"hi\", 42)\n}\n"
	colors := colorsOf(darkTheme, "go", code)
	if colors == nil {
		t.Fatal("go must be highlighted")
	}
	if colors[0] != darkTheme.syntax[synKeyword] {
		t.Errorf("'package' must use the keyword colour")
	}
}

// Online-free check that a highlighted block actually draws more than one
// colour: the body and mono fonts are embedded, so no download is needed.
func TestCodeBlockDrawsSeveralColours(t *testing.T) {
	data, err := Render(Request{Format: FormatMarkdown, Content: "```python\ndef f(x):\n    return \"ok\"  # done\n```\n"})
	if err != nil {
		t.Fatal(err)
	}
	img := decodePNG(t, data)
	seen := map[[3]uint32]bool{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a == 0 {
				continue
			}
			seen[[3]uint32{r >> 12, g >> 12, bl >> 12}] = true
		}
	}
	if len(seen) < 4 {
		t.Fatalf("code block should use several colours, saw %d", len(seen))
	}
}

// A plain-text block must still render with a single text colour.
func TestPlainCodeBlockStaysMonochrome(t *testing.T) {
	colors := colorsOf(lightTheme, "", "a b c")
	if colors != nil {
		t.Fatal("plain block must not allocate per-rune colours")
	}
}

package richimage

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// reStructuredText delegates code-block bodies with the package-level Get, so
// it reaches the global registry directly. Raku must be unreachable there too.
func rstDirective(lang string) string {
	return ".. code-block:: " + lang + "\n\n    say q{a};\n\n"
}

func TestRSTDirectiveCannotReachRaku(t *testing.T) {
	// Control: Raku colours this body when it is reachable at all.
	ctl, err := chroma.Tokenise(lexers.Raku, nil, "say q{a};\n")
	if err != nil {
		t.Fatal(err)
	}
	reachable := false
	for _, tok := range ctl {
		if tok.Type != chroma.Text && tok.Type != chroma.TextWhitespace {
			reachable = true
		}
	}
	if !reachable {
		t.Fatal("control failed: Raku does not colour this body, so the test proves nothing")
	}

	raku, same := rstDirective("raku"), rstDirective("zzzz")
	withHighlighter(t, func() {
		// The directive must actually match, or the test proves nothing.
		rst := lexers.Get("rst")
		if rst == nil {
			t.Fatal("chroma must provide the reStructuredText lexer")
		}
		toks, err := chroma.Tokenise(rst, nil, raku)
		if err != nil {
			t.Fatal(err)
		}
		matched := false
		for _, tok := range toks {
			if tok.Type == chroma.Keyword && strings.TrimSpace(tok.Value) == "raku" {
				matched = true
			}
		}
		if !matched {
			t.Fatal("the code-block directive did not match, so the test would prove nothing")
		}
	})
	if !sameColors(colorsOf(lightTheme, "rst", raku), colorsOf(lightTheme, "rst", same)) {
		t.Fatal("an RST code-block naming raku was highlighted: the package-level registry reached Raku")
	}
}

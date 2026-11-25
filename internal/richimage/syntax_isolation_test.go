package richimage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// withHighlighter runs fn while holding the highlight semaphore, the same way
// codeColors does, so registry access in tests is serialised too.
func withHighlighter(t *testing.T, fn func()) {
	t.Helper()
	if err := acquireHighlight(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer releaseHighlight()
	fn()
}

// Every lookup path must miss Raku: name, alias, case variants, filename and
// the patterns Raku itself declares.
func TestRakuUnreachableFromAllLookupPaths(t *testing.T) {
	raku := lexers.Raku // the object itself, which no lookup may return any more
	if raku == nil || raku.Config().Name != "Raku" {
		t.Fatal("chroma must provide the Raku lexer for this test to mean anything")
	}
	withHighlighter(t, func() {
		reg := highlightRegistry()
		for _, name := range []string{"raku", "Raku", "perl6", "pl6", "RAKU"} {
			if got := reg.Get(name); got != nil && excludedLexer(got) {
				t.Errorf("Get(%q) reached Raku", name)
			}
		}
		for _, pattern := range raku.Config().Filenames {
			name := strings.Replace(pattern, "*", "x", 1)
			if got := reg.Match(name); got != nil && excludedLexer(got) {
				t.Errorf("Match(%q) reached Raku", name)
			}
		}
		for _, l := range reg.Lexers {
			if excludedLexer(l) {
				t.Fatal("the installed registry must not contain Raku")
			}
		}
		if lexers.Get("raku") != nil || lexers.Get("perl6") != nil {
			t.Fatal("package-level lexers.Get must not reach Raku")
		}
	})
}

// Nested dispatch: a Markdown fence naming Raku must not reach Raku. The only
// observable difference between reaching it and not is the colouring, so the
// Raku fence must colour exactly like a fence naming an unknown language.
func TestNestedRakuFenceIsNotDispatched(t *testing.T) {
	body := "say q{a};\n"
	// Control: when Raku is reachable it colours this body, so the check below
	// is meaningful and would fail if the exclusion were bypassed.
	raku := lexers.Raku
	ctl, err := chroma.Tokenise(raku, nil, body)
	if err != nil {
		t.Fatal(err)
	}
	coloured := false
	for _, tok := range ctl {
		if tok.Type != chroma.Text && tok.Type != chroma.TextWhitespace {
			coloured = true
		}
	}
	if !coloured {
		t.Fatal("control failed: Raku does not colour this body, so the test proves nothing")
	}
	// Same-length fence names, so the only difference is the language lookup.
	withRaku := "```raku\n" + body + "```\n"
	withOther := "```zzzz\n" + body + "```\n"
	if !sameColors(colorsOf(lightTheme, "markdown", withRaku), colorsOf(lightTheme, "markdown", withOther)) {
		t.Fatal("a nested raku fence was highlighted: nested dispatch reached Raku")
	}
}

// Unfinished and malformed Raku, whether top-level or nested, must not change
// how a later, unrelated highlight is coloured.
func TestUnfinishedInputDoesNotLeakIntoLaterHighlights(t *testing.T) {
	py := "def f(x):\n    return \"ok\"  # done\n"
	before := colorsOf(lightTheme, "python", py)
	hostile := []string{
		"my $s = q{ unfinished",
		"s/( unclosed",
		"m!abc",
		"```raku\nsay q{ open\n```\n",
		"```raku\n$x = qq[ never closed\n",
	}
	for _, src := range hostile {
		colorsOf(lightTheme, "raku", src)
		colorsOf(lightTheme, "markdown", src)
		if after := colorsOf(lightTheme, "python", py); !sameColors(after, before) {
			t.Fatalf("after %q, python colouring changed", src)
		}
	}
}

// A canceled caller must return promptly while the highlighter is busy.
func TestCancelledCallerDoesNotWaitForHighlighter(t *testing.T) {
	highlightSem <- struct{}{} // the highlighter is busy
	defer releaseHighlight()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := codeColors(ctx, lightTheme, "python", "x = 1\n")
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled caller stayed blocked on the highlighter")
	}
}

// Blocks above the size bound stay plain: tokenisation cannot be interrupted,
// so its cost must be capped before it starts.
func TestLargeBlockStaysPlain(t *testing.T) {
	code := strings.Repeat("x = 1\n", maxHighlightBytes/6+10)
	if colors := colorsOf(lightTheme, "python", code); colors != nil {
		t.Fatal("oversized block must not be highlighted")
	}
}

// A block without a language never takes the highlighter at all.
func TestNoLanguageSkipsHighlighter(t *testing.T) {
	highlightSem <- struct{}{}
	defer releaseHighlight()
	colors, err := codeColors(context.Background(), lightTheme, "", "x = 1\n")
	if err != nil || colors != nil {
		t.Fatalf("no-language block must return plain without waiting: %v %v", colors, err)
	}
}

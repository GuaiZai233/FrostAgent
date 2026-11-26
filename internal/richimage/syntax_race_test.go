package richimage

import (
	"image/color"
	"sync"
	"testing"
)

// Raku rewrites shared lexer rules while tokenising, so it must never be
// highlighted, under any alias, and its different delimiters must not leak.
func TestRakuIsNotHighlighted(t *testing.T) {
	for _, info := range []string{"raku", "perl6", "pl6", "Raku"} {
		if syntaxLexer(info) != nil {
			t.Errorf("%q must stay plain text", info)
		}
	}
	snippets := []string{
		"my $x = q{a};\n",
		"s/a/b/; m!c!;\n",
		"say qq[x] ~ qw<y>;\n",
		"my $s = q{ unfinished",
	}
	for _, s := range snippets {
		if colorsOf(lightTheme, "raku", s) != nil {
			t.Errorf("raku snippet %q must not be coloured", s)
		}
	}
}

// Highlighting from many goroutines must match the sequential result exactly.
// Run with -race: the highlight lock must cover lookup, tokenisation and colouring.
func TestConcurrentHighlightingIsDeterministic(t *testing.T) {
	type sample struct {
		info, code string
	}
	samples := []sample{
		{"python", "def f(x):\n    return \"ok\"  # done\n"},
		{"go", "package main\n\nfunc main() {\n\tprintln(\"hi\", 42)\n}\n"},
		{"rust", "fn main() { let x = 1; }\n"},
		{"json", "{\"a\": [1, 2, true]}\n"},
		{"raku", "say q{a};\n"},
		{"python", "unterminated = '''abc"},
	}
	want := make([][]color.RGBA, len(samples))
	for i, s := range samples {
		want[i] = colorsOf(lightTheme, s.info, s.code)
	}

	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for round := 0; round < 30; round++ {
				// Rotate the order so goroutines interleave different languages.
				i := (g + round) % len(samples)
				got := colorsOf(lightTheme, samples[i].info, samples[i].code)
				if !sameColors(got, want[i]) {
					select {
					case errs <- samples[i].info:
					default:
					}
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for info := range errs {
		t.Errorf("concurrent %s highlighting differed from the sequential result", info)
	}
}

func sameColors(a, b []color.RGBA) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

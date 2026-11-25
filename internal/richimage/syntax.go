package richimage

import (
	"context"
	"image/color"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Syntax highlighting uses chroma's lexers (MIT, see THIRD_PARTY_NOTICES.md)
// through a private registry that this package owns, with three safety rules:
//
//  1. Raku is never registered. Its tokenisation rewrites shared lexer rules
//     that persist between calls (chroma lexers/raku.go, replaceRule), and
//     nested dispatch (Markdown or Svelte fences) would reach it through any
//     registry that contains it. Leaving it out of the private registry makes
//     every lookup path (name, alias, filename, MIME, nested) miss it.
//  2. Highlighting is serialised by a semaphore that honours cancellation, so
//     a canceled render never waits behind another render's tokenisation.
//  3. Blocks above maxHighlightBytes stay plain text. A running tokenisation
//     cannot be interrupted, so its cost must be bounded up front.

// maxHighlightBytes bounds the work of one tokenisation.
const maxHighlightBytes = 8 << 10

// highlightSem holds one token; acquiring it serialises highlighting.
var highlightSem = make(chan struct{}, 1)

var (
	privateOnce sync.Once
	privateReg  *chroma.LexerRegistry
)

// excludedLexer reports whether a lexer must never be reachable from here.
func excludedLexer(l chroma.Lexer) bool {
	return l.Config().Name == "Raku"
}

// highlightRegistry returns the private registry, building it once. Register
// points each shared lexer at this registry, so nested lookups made by those
// lexers also stay inside it. Callers must hold highlightSem.
func highlightRegistry() *chroma.LexerRegistry {
	privateOnce.Do(func() {
		reg := chroma.NewLexerRegistry()
		for _, l := range lexers.GlobalLexerRegistry.Lexers {
			if excludedLexer(l) {
				continue
			}
			reg.Register(l)
		}
		privateReg = reg
	})
	return privateReg
}

// acquireHighlight waits for the semaphore or for ctx, whichever comes first.
func acquireHighlight(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case highlightSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseHighlight() { <-highlightSem }

// syntaxLexer returns the lexer for a fence info string such as "python" or
// "python title=x", or nil when the private registry has none.
// Callers must hold highlightSem.
func syntaxLexer(info string) chroma.Lexer {
	fields := strings.Fields(info)
	if len(fields) == 0 {
		return nil
	}
	lexer := highlightRegistry().Get(strings.ToLower(fields[0]))
	if lexer == nil || excludedLexer(lexer) {
		return nil
	}
	return lexer
}

// syntaxClass groups token types into the few colours a theme provides.
type syntaxClass int

const (
	synPlain syntaxClass = iota
	synKeyword
	synString
	synComment
	synNumber
	synFunction
	synBuiltin
	synOperator
	synClasses
)

func classifyToken(t chroma.TokenType) syntaxClass {
	switch {
	case t.InCategory(chroma.Comment):
		return synComment
	case t.InSubCategory(chroma.LiteralString):
		return synString
	case t.InSubCategory(chroma.LiteralNumber):
		return synNumber
	case t.InCategory(chroma.Keyword):
		return synKeyword
	case t.InSubCategory(chroma.NameFunction):
		return synFunction
	case t.InSubCategory(chroma.NameBuiltin):
		return synBuiltin
	case t.InCategory(chroma.Operator):
		return synOperator
	}
	return synPlain
}

// codeColors returns one colour per rune of code, following the highlighter
// for the fence language. It returns (nil, nil) when the language is unknown,
// the block is too large, or the tokens do not cover the code exactly, so the
// caller keeps the plain colour. It returns an error only when ctx ends while
// waiting for the highlighter.
func codeColors(ctx context.Context, th theme, info, code string) ([]color.RGBA, error) {
	if len(code) > maxHighlightBytes || strings.TrimSpace(info) == "" {
		return nil, nil
	}
	if err := acquireHighlight(ctx); err != nil {
		return nil, err
	}
	defer releaseHighlight()
	lexer := syntaxLexer(info)
	if lexer == nil {
		return nil, nil
	}
	tokens, err := chroma.Tokenise(lexer, nil, code)
	if err != nil {
		return nil, nil
	}
	var colors []color.RGBA
	for _, tok := range tokens {
		col := th.syntax[classifyToken(tok.Type)]
		for range tok.Value {
			colors = append(colors, col)
		}
	}
	if len([]rune(code)) != len(colors) {
		return nil, nil
	}
	return colors, nil
}

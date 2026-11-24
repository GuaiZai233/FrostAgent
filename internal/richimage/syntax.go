package richimage

import (
	"image/color"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Syntax highlighting uses chroma's own lexer registry (MIT, see
// THIRD_PARTY_NOTICES.md), so every language chroma knows is highlighted,
// with two safety rules on top of it:
//
//   - Chroma's lexers keep their compiled rules in one shared structure and
//     tokenise against it without copying, so highlighting is serialised.
//   - The Raku lexer rewrites its own shared rules while tokenising (see
//     chroma's lexers/raku.go, replaceRule). Those rewrites persist between
//     calls, so one snippet could change how the next one is highlighted.
//     Raku is therefore left as plain text.

// highlightMu serialises the lookup, tokenisation and colouring of one block.
// Each block takes microseconds to a few milliseconds, so the lock is short.
var highlightMu sync.Mutex

// excludedLexers names lexers whose tokenisation mutates shared rules.
var excludedLexers = map[string]bool{
	"Raku": true,
}

// syntaxLexer returns the lexer for a fence info string such as "python" or
// "python title=x", or nil when chroma has no usable lexer for it.
// Callers must hold highlightMu.
func syntaxLexer(info string) chroma.Lexer {
	fields := strings.Fields(info)
	if len(fields) == 0 {
		return nil
	}
	lexer := lexers.Get(strings.ToLower(fields[0]))
	if lexer == nil || excludedLexers[lexer.Config().Name] {
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
// for the fence language. It returns nil when the language is unknown or the
// tokens do not cover the code exactly, so callers keep the plain colour.
func codeColors(th theme, info, code string) []color.RGBA {
	highlightMu.Lock()
	defer highlightMu.Unlock()
	lexer := syntaxLexer(info)
	if lexer == nil {
		return nil
	}
	tokens, err := chroma.Tokenise(lexer, nil, code)
	if err != nil {
		return nil
	}
	var colors []color.RGBA
	for _, tok := range tokens {
		col := th.syntax[classifyToken(tok.Type)]
		for range tok.Value {
			colors = append(colors, col)
		}
	}
	if len([]rune(code)) != len(colors) {
		return nil
	}
	return colors
}

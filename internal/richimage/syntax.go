package richimage

import (
	"embed"
	"image/color"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
)

// Syntax highlighting uses chroma's lexers (MIT, see THIRD_PARTY_NOTICES.md)
// for a fixed set of languages. Only those rule files are embedded, which keeps
// the binary small compared with pulling in every lexer.

//go:embed syntaxdata/*.xml
var syntaxFS embed.FS

// xmlLexerFiles maps a fence language name or alias to an embedded rule file.
// Go is handled by syntax_go.go because its upstream rules are Go code.
var xmlLexerFiles = map[string]string{
	"python": "syntaxdata/python.xml", "py": "syntaxdata/python.xml", "python3": "syntaxdata/python.xml",
	"javascript": "syntaxdata/javascript.xml", "js": "syntaxdata/javascript.xml", "jsx": "syntaxdata/javascript.xml",
	"typescript": "syntaxdata/typescript.xml", "ts": "syntaxdata/typescript.xml",
	"bash": "syntaxdata/bash.xml", "sh": "syntaxdata/bash.xml", "shell": "syntaxdata/bash.xml", "zsh": "syntaxdata/bash.xml",
	"json": "syntaxdata/json.xml",
}

// xmlLexers caches the parsed rule files; each is parsed at most once.
var xmlLexers sync.Map // file name -> *sync.OnceValue[chroma.Lexer]

func cachedXMLLexer(file string) chroma.Lexer {
	once, _ := xmlLexers.LoadOrStore(file, sync.OnceValue(func() chroma.Lexer {
		l, err := chroma.NewXMLLexer(syntaxFS, file)
		if err != nil {
			return nil
		}
		return l
	}))
	return once.(func() chroma.Lexer)()
}

// syntaxLexer returns the lexer for a fence info string such as "python" or
// "python title=x", or nil when the language is not highlighted.
func syntaxLexer(info string) chroma.Lexer {
	fields := strings.Fields(info)
	if len(fields) == 0 {
		return nil
	}
	name := strings.ToLower(fields[0])
	if name == "go" || name == "golang" {
		return goSyntaxLexer()
	}
	if file, ok := xmlLexerFiles[name]; ok {
		return cachedXMLLexer(file)
	}
	return nil
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

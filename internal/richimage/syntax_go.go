package richimage

// The Go rules below are adapted from chroma's lexers/go.go (MIT, Copyright
// Alec Thomas). Raw strings are plain string tokens here: the upstream version
// delegates them to an embedded text-template lexer that is not vendored.

import (
	"strings"
	"sync"

	. "github.com/alecthomas/chroma/v2" // nolint: revive
)

var goSyntaxLexer = sync.OnceValue(func() Lexer {
	return MustNewLexer(
		&Config{
			Name:      "Go",
			Aliases:   []string{"go", "golang"},
			Filenames: []string{"*.go"},
			MimeTypes: []string{"text/x-gosrc"},
		},
		goRules,
	).SetAnalyser(func(text string) float32 {
		if strings.Contains(text, "fmt.") && strings.Contains(text, "package ") {
			return 0.5
		}
		if strings.Contains(text, "package ") {
			return 0.1
		}
		return 0.0
	})
})

func goRules() Rules {
	return Rules{
		"root": {
			goRule(`\n`, TextWhitespace, nil),
			goRule(`\s+`, TextWhitespace, nil),
			goRule(`//[^\s][^\n\r]*`, CommentPreproc, nil),
			goRule(`//\s+[^\n\r]*`, CommentSingle, nil),
			goRule(`/(\\\n)?[*](.|\n)*?[*](\\\n)?/`, CommentMultiline, nil),
			goRule(`(import|package)\b`, KeywordNamespace, nil),
			goRule(`(var|func|struct|map|chan|type|interface|const)\b`, KeywordDeclaration, nil),
			goRule(Words(``, `\b`, `break`, `default`, `select`, `case`, `defer`, `go`, `else`, `goto`, `switch`, `fallthrough`, `if`, `range`, `continue`, `for`, `return`), Keyword, nil),
			goRule(`(true|false|iota|nil)\b`, KeywordConstant, nil),
			goRule(Words(``, `\b(\()`, `uint`, `uint8`, `uint16`, `uint32`, `uint64`, `int`, `int8`, `int16`, `int32`, `int64`, `float`, `float32`, `float64`, `complex64`, `complex128`, `byte`, `rune`, `string`, `bool`, `error`, `uintptr`, `print`, `println`, `panic`, `recover`, `close`, `complex`, `real`, `imag`, `len`, `cap`, `append`, `copy`, `delete`, `new`, `make`, `clear`, `min`, `max`), ByGroups(NameBuiltin, Punctuation), nil),
			goRule(Words(``, `\b`, `uint`, `uint8`, `uint16`, `uint32`, `uint64`, `int`, `int8`, `int16`, `int32`, `int64`, `float`, `float32`, `float64`, `complex64`, `complex128`, `byte`, `rune`, `string`, `bool`, `error`, `uintptr`, `any`), KeywordType, nil),
			goRule(`\d+i`, LiteralNumber, nil),
			goRule(`\d+\.\d*([Ee][-+]\d+)?i`, LiteralNumber, nil),
			goRule(`\.\d+([Ee][-+]\d+)?i`, LiteralNumber, nil),
			goRule(`\d+[Ee][-+]\d+i`, LiteralNumber, nil),
			goRule(`\d+(\.\d+[eE][+\-]?\d+|\.\d*|[eE][+\-]?\d+)`, LiteralNumberFloat, nil),
			goRule(`\.\d+([eE][+\-]?\d+)?`, LiteralNumberFloat, nil),
			goRule(`0[0-7]+`, LiteralNumberOct, nil),
			goRule(`0[xX][0-9a-fA-F_]+`, LiteralNumberHex, nil),
			goRule(`0b[01_]+`, LiteralNumberBin, nil),
			goRule(`(0|[1-9][0-9_]*)`, LiteralNumberInteger, nil),
			goRule(`'(\\['"\\abfnrtv]|\\x[0-9a-fA-F]{2}|\\[0-7]{1,3}|\\u[0-9a-fA-F]{4}|\\U[0-9a-fA-F]{8}|[^\\])'`, LiteralStringChar, nil),
			goRule("`[^`]*`", LiteralString, nil),
			goRule(`"(\\\\|\\"|[^"])*"`, LiteralString, nil),
			goRule(`(<<=|>>=|<<|>>|<=|>=|&\^=|&\^|\+=|-=|\*=|/=|%=|&=|\|=|&&|\|\||<-|\+\+|--|==|!=|:=|\.\.\.|[+\-*/%&])`, Operator, nil),
			goRule(`([a-zA-Z_]\w*)(\s*)(\()`, ByGroups(NameFunction, UsingSelf("root"), Punctuation), nil),
			goRule(`[|^<>=!()\[\]{}.,;:~]`, Punctuation, nil),
			goRule(`[^\W\d]\w*`, NameOther, nil),
		},
	}
}

// goRule builds a Rule with keyed fields; chroma's Rule literals are otherwise
// unkeyed, which go vet's composites check rejects for types from other packages.
func goRule(pattern string, t Emitter, m Mutator) Rule {
	return Rule{Pattern: pattern, Type: t, Mutator: m}
}

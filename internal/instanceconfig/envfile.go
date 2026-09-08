package instanceconfig

import (
	"fmt"
	"github.com/joho/godotenv"
	"strings"
	"unicode"
)

// FormatEnvEntry formats key=value for .env with secure dotenv-compatible serialization
// guaranteed to round-trip correctly with godotenv v1.5.1.
//
// godotenv v1.5.1 has known parser quirks:
//  1. In double quotes, closing quote checks `src[i-1] == '\\'` without backslash parity,
//     causing any value ending in backslash (e.g. C:\temp\) to fail with "unterminated quoted value".
//  2. Trailing quote trimming in double quotes strips escaped quotes (\").
//
// To guarantee round-trip fidelity:
//   - Simple values (no newlines, no $, no leading/trailing space, no leading quote, no inline comments)
//     are formatted unquoted (key=value), which natively preserves backslashes and quotes without escaping pitfalls.
//   - Values starting with a quote (without single quotes or newlines, not ending in \) are single-quoted (key='value').
//   - Values requiring quotes (empty string, leading/trailing space, $, newlines) are double-quoted with
//     proper escape sequences (\, \n, \r, \", \$).
//
// Before returning, godotenv.Unmarshal is executed on the formatted line. If the value cannot
// be safely parsed back to its exact original form, an error is returned to prevent persisting corrupt values.
func FormatEnvEntry(key, value string) (string, error) {
	if strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("value contains null byte (NUL) which cannot be represented in environment")
	}

	hasLeadingOrTrailingSpace := strings.HasPrefix(value, " ") || strings.HasPrefix(value, "\t") ||
		strings.HasSuffix(value, " ") || strings.HasSuffix(value, "\t")
	hasNewline := strings.ContainsAny(value, "\r\n")
	hasDollar := strings.ContainsRune(value, '$')
	startsQuote := strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "'")
	hasInlineComment := strings.Contains(value, " #") || strings.Contains(value, "\t#")

	canBeUnquoted := value != "" && !hasNewline && !hasLeadingOrTrailingSpace && !hasDollar && !startsQuote && !hasInlineComment
	canBeSingleQuoted := value != "" && !hasNewline && !strings.ContainsRune(value, '\'') && !strings.HasSuffix(value, "\\")

	var formatted string
	if canBeUnquoted {
		formatted = key + "=" + value
	} else if canBeSingleQuoted && startsQuote {
		formatted = key + "='" + value + "'"
	} else {
		var b strings.Builder
		b.WriteString(key)
		b.WriteString(`="`)
		for _, r := range value {
			switch r {
			case '\\':
				b.WriteString(`\\`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '"':
				b.WriteString(`\"`)
			case '$':
				b.WriteString(`\$`)
			default:
				b.WriteRune(r)
			}
		}
		b.WriteByte('"')
		formatted = b.String()
	}

	// Pre-write verification against godotenv parser
	parsed, err := godotenv.Unmarshal(formatted)
	if err != nil {
		return "", fmt.Errorf("value cannot be safely represented in .env: %w", err)
	}
	if parsedVal, ok := parsed[key]; !ok || parsedVal != value {
		return "", fmt.Errorf("value cannot be safely round-tripped in .env (got %q, want %q)", parsed[key], value)
	}
	if len(parsed) != 1 {
		return "", fmt.Errorf("value causes unexpected additional keys in .env: %v", parsed)
	}

	return formatted, nil
}

// envStatement represents a single statement in a .env file.
type envStatement struct {
	raw   string
	isKey bool
	key   string
}

// parseKeyAndSep parses a key name and locates the separator (= or :) at the start of s.
// It mirrors godotenv v1.5.1 locateKeyName semantics, supporting optional 'export' prefix
// and variable characters [A-Za-z0-9_.].
func parseKeyAndSep(s string) (key string, sepOffset int, ok bool) {
	trimmed := s
	prefixLen := 0
	if strings.HasPrefix(trimmed, "export") && len(trimmed) > 6 && (trimmed[6] == ' ' || trimmed[6] == '\t') {
		afterExport := strings.TrimLeft(trimmed[6:], " \t")
		prefixLen = len(s) - len(afterExport)
		trimmed = afterExport
	}

	for i := 0; i < len(trimmed); i++ {
		c := trimmed[i]
		if c == ' ' || c == '\t' || c == '\r' {
			continue
		}
		if c == '=' || c == ':' {
			rawKey := strings.TrimSpace(trimmed[:i])
			if rawKey == "" {
				return "", -1, false
			}
			return rawKey, prefixLen + i, true
		}
		if c == '_' || c == '.' || unicode.IsLetter(rune(c)) || unicode.IsNumber(rune(c)) {
			continue
		}
		return "", -1, false
	}
	return "", -1, false
}

// parseEnvStatements parses raw .env content into statements, strictly mirroring
// godotenv v1.5.1 statement boundary and quote terminator semantics.
func parseEnvStatements(content string) []envStatement {
	var stmts []envStatement
	idx := 0
	n := len(content)

	for idx < n {
		// Locate statement begin, skipping whitespace
		pos := idx
		for pos < n && (content[pos] == ' ' || content[pos] == '\t' || content[pos] == '\r' || content[pos] == '\n') {
			pos++
		}

		if pos >= n {
			// Trailing whitespace to EOF
			stmts = append(stmts, envStatement{
				raw:   content[idx:n],
				isKey: false,
			})
			break
		}

		// Comment line starting with #
		if content[pos] == '#' {
			lineEnd := strings.IndexByte(content[pos:], '\n')
			var commentEnd int
			if lineEnd == -1 {
				commentEnd = n
			} else {
				commentEnd = pos + lineEnd + 1
			}
			stmts = append(stmts, envStatement{
				raw:   content[idx:commentEnd],
				isKey: false,
			})
			idx = commentEnd
			continue
		}

		// Try to parse a key-value assignment starting at pos
		key, sepOffset, ok := parseKeyAndSep(content[pos:])
		if !ok {
			// Not a valid key statement; consume up to next newline as non-key
			lineEnd := strings.IndexByte(content[pos:], '\n')
			var nextIdx int
			if lineEnd == -1 {
				nextIdx = n
			} else {
				nextIdx = pos + lineEnd + 1
			}
			stmts = append(stmts, envStatement{
				raw:   content[idx:nextIdx],
				isKey: false,
			})
			idx = nextIdx
			continue
		}

		// Preserve any trivia/whitespace between idx and pos
		if pos > idx {
			stmts = append(stmts, envStatement{
				raw:   content[idx:pos],
				isKey: false,
			})
			idx = pos
		}

		// Statement starts at idx (which now equals pos)
		sepAbsPos := pos + sepOffset
		valOffset := sepAbsPos + 1
		for valOffset < n && (content[valOffset] == ' ' || content[valOffset] == '\t') {
			valOffset++
		}

		if valOffset >= n {
			// Empty value at EOF
			stmts = append(stmts, envStatement{
				raw:   content[idx:n],
				isKey: true,
				key:   key,
			})
			idx = n
			break
		}

		quote := content[valOffset]
		if quote == '"' || quote == '\'' {
			// Quoted value. Pinned to godotenv v1.5.1 semantics:
			// godotenv checks `prevChar := src[i-1]; prevChar == '\\'` without backslash parity.
			closingQuotePos := -1
			for p := valOffset + 1; p < n; p++ {
				if content[p] == quote {
					if content[p-1] == '\\' {
						continue
					}
					closingQuotePos = p
					break
				}
			}

			var valEnd int
			if closingQuotePos != -1 {
				valEnd = closingQuotePos + 1
			} else {
				valEnd = n
			}

			// Check what follows the closing quote on the same line.
			// godotenv.extractVarValue returns src[i+1:] immediately to main loop.
			// If non-comment characters follow on the same line, multiple statements exist.
			lineEnd := strings.IndexByte(content[valEnd:], '\n')
			var restOfLine string
			var restEnd int
			if lineEnd == -1 {
				restOfLine = content[valEnd:]
				restEnd = n
			} else {
				restOfLine = content[valEnd : valEnd+lineEnd+1]
				restEnd = valEnd + lineEnd + 1
			}

			trimmedRest := strings.TrimLeft(restOfLine, " \t\r\n")
			if trimmedRest == "" || strings.HasPrefix(trimmedRest, "#") {
				// Sole statement on this line; include remainder of line
				stmts = append(stmts, envStatement{
					raw:   content[idx:restEnd],
					isKey: true,
					key:   key,
				})
				idx = restEnd
			} else {
				// Multiple statements on the same line (e.g. KEY1="val" KEY2=val)
				stmts = append(stmts, envStatement{
					raw:   content[idx:valEnd],
					isKey: true,
					key:   key,
				})
				idx = valEnd
			}
		} else {
			// Unquoted value: godotenv reads until newline/EOF
			lineEnd := strings.IndexByte(content[valOffset:], '\n')
			var stmtEnd int
			if lineEnd == -1 {
				stmtEnd = n
			} else {
				stmtEnd = valOffset + lineEnd + 1
			}

			stmts = append(stmts, envStatement{
				raw:   content[idx:stmtEnd],
				isKey: true,
				key:   key,
			})
			idx = stmtEnd
		}
	}

	return stmts
}

// mutateEnvStatements updates the first occurrence of key with formatted and eliminates
// any duplicate occurrences of the same key. If key was not present, it is appended.
// The updated entry always terminates with a newline to prevent swallowing any sibling
// inline statements that may have followed it on the same line.
func mutateEnvStatements(stmts []envStatement, key, formatted string) []envStatement {
	found := false
	var result []envStatement
	for _, stmt := range stmts {
		if stmt.isKey && stmt.key == key {
			if !found {
				result = append(result, envStatement{
					raw:   formatted + "\n",
					isKey: true,
					key:   key,
				})
				found = true
			}
			continue
		}
		result = append(result, stmt)
	}
	if !found {
		if len(result) > 0 && !strings.HasSuffix(result[len(result)-1].raw, "\n") {
			result = append(result, envStatement{raw: "\n"})
		}
		result = append(result, envStatement{
			raw:   formatted + "\n",
			isKey: true,
			key:   key,
		})
	}
	return result
}

// removeKeyFromStatements removes all occurrences of key from stmts.
func removeKeyFromStatements(stmts []envStatement, key string) []envStatement {
	var result []envStatement
	for _, stmt := range stmts {
		if stmt.isKey && stmt.key == key {
			continue
		}
		result = append(result, stmt)
	}
	return result
}

// renderEnvStatements renders statements into bytes, preserving exact original bytes
// and guaranteeing trailing newline termination when modified.
func renderEnvStatements(stmts []envStatement) []byte {
	var b strings.Builder
	for _, s := range stmts {
		b.WriteString(s.raw)
	}
	res := b.String()
	if len(res) > 0 && !strings.HasSuffix(res, "\n") {
		res += "\n"
	}
	return []byte(res)
}

// MutateEnv preserves unrelated statements and validates updated value round trips.
func MutateEnv(raw, key, value string, remove bool) (string, error) {
	stmts := parseEnvStatements(raw)
	if remove {
		stmts = removeKeyFromStatements(stmts, key)
	} else {
		formatted, err := FormatEnvEntry(key, value)
		if err != nil {
			return "", err
		}
		stmts = mutateEnvStatements(stmts, key, formatted)
	}
	return string(renderEnvStatements(stmts)), nil
}

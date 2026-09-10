package security

import (
	"encoding/base64"
	"regexp"
	"strings"
	"unicode"
)

// tolerantPercentUnescape scans s and decodes any valid %[0-9a-fA-F]{2} sequence
// into its single byte value, while preserving malformed escapes (e.g. %ZZ, dangling %)
// and '+' verbatim.
func tolerantPercentUnescape(s string) (string, bool) {
	if !strings.Contains(s, "%") {
		return s, false
	}
	var b strings.Builder
	b.Grow(len(s))
	changed := false
	for i := 0; i < len(s); {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			b.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
			i += 3
			changed = true
		} else {
			b.WriteByte(s[i])
			i++
		}
	}
	if !changed {
		return s, false
	}
	return b.String(), true
}

func isHex(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
}

func unhex(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

// stripZeroWidthAndControl removes invisible characters, zero-width spaces,
// directional formatters, full-width confusables, and mathematical stylized runes.
func stripZeroWidthAndControl(s string) (string, bool) {
	stripped := strings.Map(func(r rune) rune {
		// Zero-width & invisible formatters
		switch r {
		case rune(0x200b), rune(0x200c), rune(0x200d), rune(0x200e), rune(0x200f), rune(0x2060), rune(0xfeff):
			return -1
		case rune(0x180e), rune(0x00ad):
			return -1
		case rune(0x3000): // ideographic full-width space
			return ' '
		default:
			// Directional formatting
			if (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
				return -1
			}
			// Tags block
			if r >= 0xe0000 && r <= 0xe007f {
				return -1
			}
			// Full-width ASCII normalization: 0xFF01..0xFF5E -> 0x0021..0x007E
			if r >= 0xff01 && r <= 0xff5e {
				return r - 0xfee0
			}
			// Mathematical Alphanumeric Symbols: U+1D400..U+1D7FF
			if r >= 0x1d400 && r <= 0x1d7ff {
				if ascii := normalizeMathRune(r); ascii != 0 {
					return ascii
				}
			}
			return r
		}
	}, s)
	return stripped, stripped != s
}

// normalizeMixedScriptCyrillic replaces Cyrillic lookalikes with Latin equivalents ONLY
// in mixed-script tokens or contexts where Cyrillic lookalikes are used to disguise Latin words.
// It preserves genuine Cyrillic text (e.g. Russian, Ukrainian) by detecting non-lookalike
// native Cyrillic runes (such as б, в, г, д, ж, з, и, й, л, п, т, ф, ц, ч, ш, щ, ъ, ы, ь, э, ю, я).
func normalizeMixedScriptCyrillic(s string) (string, bool) {
	if !hasCyrillic(s) {
		return s, false
	}

	var b strings.Builder
	b.Grow(len(s))
	changed := false

	runes := []rune(s)
	n := len(runes)

	for i := 0; i < n; {
		if isWordDelimiter(runes[i]) {
			b.WriteRune(runes[i])
			i++
			continue
		}

		// Find token boundary
		start := i
		for i < n && !isWordDelimiter(runes[i]) {
			i++
		}
		token := runes[start:i]

		hasLatin := false
		hasCyrillicNonLookalike := false
		hasLookalike := false

		for _, r := range token {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				hasLatin = true
			} else if isCyrillicRune(r) {
				if normalizeCyrillicLookalike(r) != 0 {
					hasLookalike = true
				} else {
					hasCyrillicNonLookalike = true
				}
			}
		}

		// If the token is a genuine Cyrillic word, leave it untouched.
		// If it has Latin characters and Cyrillic lookalikes (e.g. "ignоrе"),
		// or is an evasion token with no native Cyrillic characters, convert lookalikes.
		if hasLookalike && !hasCyrillicNonLookalike && (hasLatin || !hasAnyCyrillicNonLookalike(s)) {
			for _, r := range token {
				if ascii := normalizeCyrillicLookalike(r); ascii != 0 {
					b.WriteRune(ascii)
					changed = true
				} else {
					b.WriteRune(r)
				}
			}
		} else {
			for _, r := range token {
				b.WriteRune(r)
			}
		}
	}

	if !changed {
		return s, false
	}
	return b.String(), true
}

func isWordDelimiter(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
}

func isCyrillicRune(r rune) bool {
	return (r >= 0x0400 && r <= 0x052f) || (r >= 0x2de0 && r <= 0x2dff) || (r >= 0xa640 && r <= 0xa69f)
}

func hasCyrillic(s string) bool {
	for _, r := range s {
		if isCyrillicRune(r) {
			return true
		}
	}
	return false
}

func hasAnyCyrillicNonLookalike(s string) bool {
	for _, r := range s {
		if isCyrillicRune(r) && normalizeCyrillicLookalike(r) == 0 {
			return true
		}
	}
	return false
}

func normalizeMathRune(r rune) rune {
	// Latin capital letters in math alphanumeric blocks
	// (Bold, Italic, Bold Italic, Script, Fraktur, Double-struck, Sans-serif, etc.)
	offsetsCap := []rune{
		0x1d400, 0x1d434, 0x1d468, 0x1d49c, 0x1d4d0, 0x1d504, 0x1d538,
		0x1d56c, 0x1d5a0, 0x1d5d4, 0x1d608, 0x1d63c, 0x1d670,
	}
	for _, base := range offsetsCap {
		if r >= base && r < base+26 {
			return 'A' + (r - base)
		}
	}
	// Latin small letters in math alphanumeric blocks
	offsetsSmall := []rune{
		0x1d41a, 0x1d44e, 0x1d482, 0x1d4b6, 0x1d4ea, 0x1d51e, 0x1d552,
		0x1d586, 0x1d5ba, 0x1d5ee, 0x1d622, 0x1d656, 0x1d68a,
	}
	for _, base := range offsetsSmall {
		if r >= base && r < base+26 {
			return 'a' + (r - base)
		}
	}
	return 0
}

func normalizeCyrillicLookalike(r rune) rune {
	switch r {
	case 0x0430: // а
		return 'a'
	case 0x0441: // с
		return 'c'
	case 0x0435: // е
		return 'e'
	case 0x0456: // і
		return 'i'
	case 0x0458: // ј
		return 'j'
	case 0x043e: // о
		return 'o'
	case 0x0440: // р
		return 'p'
	case 0x0455: // ѕ
		return 's'
	case 0x0443: // у
		return 'y'
	case 0x0445: // х
		return 'x'
	case 0x0410: // А
		return 'A'
	case 0x0412: // В
		return 'B'
	case 0x0421: // С
		return 'C'
	case 0x0415: // Е
		return 'E'
	case 0x041d: // Н
		return 'H'
	case 0x0406: // І
		return 'I'
	case 0x0408: // Ј
		return 'J'
	case 0x041a: // К
		return 'K'
	case 0x041c: // М
		return 'M'
	case 0x041e: // О
		return 'O'
	case 0x0420: // Р
		return 'P'
	case 0x0422: // Т
		return 'T'
	case 0x0425: // Х
		return 'X'
	case 0x0423: // У
		return 'Y'
	}
	return 0
}

var b64TokenRegex = regexp.MustCompile(`\b[A-Za-z0-9+/_-]{16,}={0,2}\b`)

// replaceEmbeddedBase64 finds embedded Base64 candidate tokens in content
// and replaces them with their decoded printable representation.
func replaceEmbeddedBase64(content string) (string, bool) {
	if len(content) > MaxInspectionSize {
		return content, false
	}
	matches := b64TokenRegex.FindAllStringIndex(content, -1)
	if len(matches) == 0 {
		return content, false
	}
	var sb strings.Builder
	lastIdx := 0
	modified := false
	for _, match := range matches {
		token := content[match[0]:match[1]]
		decoded, ok := tryDecodeBase64(token)
		if ok && decoded != token {
			sb.WriteString(content[lastIdx:match[0]])
			sb.WriteString(decoded)
			lastIdx = match[1]
			modified = true
		}
	}
	if !modified {
		return content, false
	}
	sb.WriteString(content[lastIdx:])
	return sb.String(), true
}

func tryDecodeBase64(s string) (string, bool) {
	if len(s) < 8 {
		return "", false
	}
	// Try standard base64
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		// Try URL-safe base64
		decoded, err = base64.URLEncoding.DecodeString(s)
		if err != nil {
			// Try unpadded
			decoded, err = base64.RawStdEncoding.DecodeString(s)
			if err != nil {
				decoded, err = base64.RawURLEncoding.DecodeString(s)
			}
		}
	}
	if err == nil && len(decoded) > 0 && len(decoded) <= MaxInspectionSize {
		decodedStr := string(decoded)
		if isValidPrintableText(decodedStr) {
			return decodedStr, true
		}
	}
	return "", false
}

// normalizeBounded applies bounded multi-pass normalization to uncover
// obfuscated, percent-escaped, zero-width, full-width, homoglyph, and Base64-encoded payloads.
func normalizeBounded(content string) (string, bool) {
	if len(content) > MaxInspectionSize {
		content = content[:MaxInspectionSize]
	}
	content, stripped := stripZeroWidthAndControl(content)
	modified := stripped

	if mixed, mc := normalizeMixedScriptCyrillic(content); mc {
		content = mixed
		modified = true
	}

	for range 3 {
		layerChanged := false

		// 1. Tolerant percent unescape
		if decoded, ok := tolerantPercentUnescape(content); ok && decoded != content && len(decoded) <= MaxInspectionSize {
			content = decoded
			modified = true
			layerChanged = true
			if s, st := stripZeroWidthAndControl(content); st {
				content = s
			}
			if s, mc := normalizeMixedScriptCyrillic(content); mc {
				content = s
			}
		}

		// 2. Full-string Base64 unescape
		trimmed := strings.TrimSpace(content)
		if decoded, ok := tryDecodeBase64(trimmed); ok && decoded != content {
			content = decoded
			modified = true
			layerChanged = true
			if s, st := stripZeroWidthAndControl(content); st {
				content = s
			}
			if s, mc := normalizeMixedScriptCyrillic(content); mc {
				content = s
			}
		}

		// 3. Embedded Base64 unescape
		if embedded, ok := replaceEmbeddedBase64(content); ok && embedded != content {
			content = embedded
			modified = true
			layerChanged = true
			if s, st := stripZeroWidthAndControl(content); st {
				content = s
			}
			if s, mc := normalizeMixedScriptCyrillic(content); mc {
				content = s
			}
		}

		if !layerChanged {
			break
		}
	}
	return content, modified
}

func isValidPrintableText(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
		if r == unicode.ReplacementChar {
			return false
		}
	}
	return true
}

package admincmd

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	DefaultPrefix         = "/"
	AdminCommandPrefixEnv = "ADMIN_COMMAND_PREFIX"
)

type CommandType string

const (
	CmdReset   CommandType = "reset"
	CmdBan     CommandType = "ban"
	CmdUnban   CommandType = "unban"
	CmdCompact CommandType = "compact"
	CmdReflect CommandType = "reflect"
)

// ParsedCommand represents a parsed administrator message command.
type ParsedCommand struct {
	Type    CommandType
	Args    []string
	RawArgs string
}

// NormalizePrefix returns the configured prefix or the default prefix if empty.
func NormalizePrefix(prefix string) string {
	p := strings.TrimSpace(prefix)
	if p == "" {
		return DefaultPrefix
	}
	return p
}

// IsWordPrefix reports whether the prefix ends with a word character (alphanumeric/underscore/CJK),
// requiring whitespace separation from the command name.
func IsWordPrefix(prefix string) bool {
	p := strings.TrimSpace(prefix)
	if p == "" {
		return false
	}
	lastRune, _ := utf8.DecodeLastRuneInString(p)
	return unicode.IsLetter(lastRune) || unicode.IsDigit(lastRune) || lastRune == '_'
}

// PrefixSeparator returns a space separator for word prefixes or empty string for symbol prefixes.
func PrefixSeparator(prefix string) string {
	if IsWordPrefix(prefix) {
		return " "
	}
	return ""
}

// FormatUsage returns concise usage instructions formatted with the specified prefix.
func FormatUsage(prefix string) string {
	p := NormalizePrefix(prefix)
	sep := PrefixSeparator(p)
	return fmt.Sprintf("管理员指令使用指南：\n"+
		"%s%sreset - 重置当前会话与上下文\n"+
		"%s%sban <userID> - 全局安全封禁用户\n"+
		"%s%sunban <userID> - 解除用户全局安全封禁\n"+
		"%s%scompact - 立即压缩总结当前会话上下文\n"+
		"%s%sreflect - 触发记忆反思",
		p, sep,
		p, sep,
		p, sep,
		p, sep,
		p, sep,
	)
}

// ParseCandidate parses the message text (with bot @ mention stripped) against the configured command prefix.
// - If the text does not match the command prefix, isCandidate is false and err is nil.
// - If the text matches the command prefix, isCandidate is true.
// - If isCandidate is true but the syntax is invalid or command unknown, err is non-nil.
func ParseCandidate(text, prefix string) (cmd ParsedCommand, isCandidate bool, err error) {
	prefix = NormalizePrefix(prefix)
	text = strings.TrimSpace(text)
	if text == "" {
		return cmd, false, nil
	}

	isWord := IsWordPrefix(prefix)
	var remainder string

	if isWord {
		if len(text) < len(prefix) {
			return cmd, false, nil
		}
		if !strings.EqualFold(text[:len(prefix)], prefix) {
			return cmd, false, nil
		}
		afterPrefix := text[len(prefix):]
		if len(afterPrefix) == 0 {
			return cmd, true, errors.New("缺少指令名称")
		}
		firstRune, _ := utf8.DecodeRuneInString(afterPrefix)
		if !unicode.IsSpace(firstRune) {
			return cmd, false, nil
		}
		remainder = strings.TrimSpace(afterPrefix)
	} else {
		if len(text) < len(prefix) {
			return cmd, false, nil
		}
		if !strings.EqualFold(text[:len(prefix)], prefix) {
			return cmd, false, nil
		}
		remainder = strings.TrimSpace(text[len(prefix):])
	}

	isCandidate = true
	if remainder == "" {
		return cmd, true, errors.New("缺少指令名称")
	}

	parts := strings.Fields(remainder)
	cmdName := strings.ToLower(parts[0])
	args := parts[1:]
	rawArgs := ""
	if len(parts) > 1 {
		rawArgs = strings.TrimSpace(remainder[len(parts[0]):])
	}

	switch CommandType(cmdName) {
	case CmdReset:
		if len(args) != 0 {
			return cmd, true, fmt.Errorf("reset 指令不需要任何参数")
		}
		return ParsedCommand{Type: CmdReset, Args: args, RawArgs: rawArgs}, true, nil
	case CmdBan:
		if len(args) != 1 {
			return cmd, true, fmt.Errorf("ban 指令格式错误，需要指定一个用户ID：ban <userID>")
		}
		return ParsedCommand{Type: CmdBan, Args: args, RawArgs: rawArgs}, true, nil
	case CmdUnban:
		if len(args) != 1 {
			return cmd, true, fmt.Errorf("unban 指令格式错误，需要指定一个用户ID：unban <userID>")
		}
		return ParsedCommand{Type: CmdUnban, Args: args, RawArgs: rawArgs}, true, nil
	case CmdCompact:
		if len(args) != 0 {
			return cmd, true, fmt.Errorf("compact 指令不需要任何参数")
		}
		return ParsedCommand{Type: CmdCompact, Args: args, RawArgs: rawArgs}, true, nil
	case CmdReflect:
		if len(args) != 0 {
			return cmd, true, fmt.Errorf("reflect 指令不需要任何参数")
		}
		return ParsedCommand{Type: CmdReflect, Args: args, RawArgs: rawArgs}, true, nil
	default:
		return cmd, true, fmt.Errorf("未知指令：%s", cmdName)
	}
}

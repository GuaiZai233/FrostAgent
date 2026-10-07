package admincmd

import (
	"errors"
	"fmt"
	"strings"
)

// StripLeadingMention removes a leading @mention or [@mention] component from text,
// returning the trimmed remainder of the message.
func StripLeadingMention(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "[@") {
		if _, after, found := strings.Cut(text, "]"); found {
			return strings.TrimSpace(after)
		}
	}
	if strings.HasPrefix(text, "@") {
		// Find first whitespace separator
		idx := strings.IndexAny(text, " \t\r\n　")
		if idx != -1 {
			return strings.TrimSpace(text[idx+1:])
		}
	}
	return text
}

// CleanTargetID normalizes and validates a target user ID parameter from admin commands.
// It accepts bare IDs, stripped @mentions, and bracket mention tokens (e.g. [@123456789]).
// It rejects empty inputs, @all/wildcards, multiple concatenated mentions,
// and tokens containing whitespace, colons, brackets, or non-ASCII characters (e.g. nicknames).
func CleanTargetID(raw string, platform string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("目标用户ID不能为空")
	}

	// Strip outer brackets if present (e.g. "[@123456789]" or "[123456789]")
	if strings.HasPrefix(trimmed, "[@") && strings.HasSuffix(trimmed, "]") {
		trimmed = trimmed[2 : len(trimmed)-1]
	} else if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		trimmed = trimmed[1 : len(trimmed)-1]
	}

	// Strip leading @ if present (e.g. "@123456789")
	trimmed = strings.TrimPrefix(trimmed, "@")
	trimmed = strings.TrimSpace(trimmed)

	if trimmed == "" {
		return "", errors.New("目标用户ID不能为空")
	}

	// Reject wildcards / all-mentions
	if strings.EqualFold(trimmed, "all") || trimmed == "全体成员" || trimmed == "0" {
		return "", errors.New("不支持对全体成员或全局通配符执行该操作")
	}

	// Reject brackets, colons, spaces, and other dangerous delimiter characters
	if strings.ContainsAny(trimmed, " \t\r\n\x00[]:@<>/\\\"'") {
		return "", fmt.Errorf("目标用户ID %q 包含非法字符", raw)
	}

	// Validate safe identifier characters (ASCII alphanumeric, -, _, .)
	for i := 0; i < len(trimmed); i++ {
		c := trimmed[i]
		isAlphanumeric := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !isAlphanumeric && c != '-' && c != '_' && c != '.' {
			return "", fmt.Errorf("目标用户ID %q 包含非法字符或非ASCII字符（禁止使用昵称）", raw)
		}
	}

	if len(trimmed) > 256 {
		return "", errors.New("目标用户ID过长")
	}

	return trimmed, nil
}


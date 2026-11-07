package admincmd

import (
	"strings"
	"testing"
)

func TestNormalizePrefix(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "/"},
		{"   ", "/"},
		{"/", "/"},
		{"!", "!"},
		{"execute", "execute"},
		{"  execute  ", "execute"},
	}

	for _, tt := range tests {
		got := NormalizePrefix(tt.input)
		if got != tt.expected {
			t.Errorf("NormalizePrefix(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestIsWordPrefix(t *testing.T) {
	tests := []struct {
		prefix string
		isWord bool
	}{
		{"", false},
		{"/", false},
		{"!", false},
		{"#", false},
		{"execute", true},
		{"cmd", true},
		{"指令", true},
		{"/cmd", true}, // ends with letter 'd'
		{"cmd/", false}, // ends with symbol '/'
	}

	for _, tt := range tests {
		got := IsWordPrefix(tt.prefix)
		if got != tt.isWord {
			t.Errorf("IsWordPrefix(%q) = %v; want %v", tt.prefix, got, tt.isWord)
		}
	}
}

func TestPrefixSeparator(t *testing.T) {
	if PrefixSeparator("/") != "" {
		t.Errorf("PrefixSeparator(\"/\") want empty string, got %q", PrefixSeparator("/"))
	}
	if PrefixSeparator("execute") != " " {
		t.Errorf("PrefixSeparator(\"execute\") want space, got %q", PrefixSeparator("execute"))
	}
}

func TestFormatUsage(t *testing.T) {
	usageSymbol := FormatUsage("/")
	if !strings.Contains(usageSymbol, "/reset") || !strings.Contains(usageSymbol, "/ban <userID>") {
		t.Errorf("FormatUsage(\"/\") missing expected lines: %s", usageSymbol)
	}

	usageWord := FormatUsage("execute")
	if !strings.Contains(usageWord, "execute reset") || !strings.Contains(usageWord, "execute ban <userID>") {
		t.Errorf("FormatUsage(\"execute\") missing expected lines: %s", usageWord)
	}
}

func TestParseCandidate(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		prefix      string
		wantType    CommandType
		wantArgs    []string
		isCandidate bool
		wantErr     bool
	}{
		// Symbol prefix tests
		{
			name:        "symbol prefix reset without space",
			text:        "/reset",
			prefix:      "/",
			wantType:    CmdReset,
			wantArgs:    nil,
			isCandidate: true,
			wantErr:     false,
		},
		{
			name:        "symbol prefix reset with space",
			text:        "/ reset",
			prefix:      "/",
			wantType:    CmdReset,
			wantArgs:    nil,
			isCandidate: true,
			wantErr:     false,
		},
		{
			name:        "symbol prefix reset case insensitive",
			text:        "/ReSeT",
			prefix:      "/",
			wantType:    CmdReset,
			wantArgs:    nil,
			isCandidate: true,
			wantErr:     false,
		},
		{
			name:        "symbol prefix reset with extra args error",
			text:        "/reset now",
			prefix:      "/",
			isCandidate: true,
			wantErr:     true,
		},
		{
			name:        "symbol prefix ban valid",
			text:        "/ban 10001",
			prefix:      "/",
			wantType:    CmdBan,
			wantArgs:    []string{"10001"},
			isCandidate: true,
			wantErr:     false,
		},
		{
			name:        "symbol prefix ban missing args error",
			text:        "/ban",
			prefix:      "/",
			isCandidate: true,
			wantErr:     true,
		},
		{
			name:        "symbol prefix ban extra args error",
			text:        "/ban 10001 extra",
			prefix:      "/",
			isCandidate: true,
			wantErr:     true,
		},
		{
			name:        "symbol prefix unban valid",
			text:        "/unban 10001",
			prefix:      "/",
			wantType:    CmdUnban,
			wantArgs:    []string{"10001"},
			isCandidate: true,
			wantErr:     false,
		},
		{
			name:        "symbol prefix unban missing args error",
			text:        "/unban",
			prefix:      "/",
			isCandidate: true,
			wantErr:     true,
		},
		{
			name:        "symbol prefix compact valid",
			text:        "/compact",
			prefix:      "/",
			wantType:    CmdCompact,
			wantArgs:    nil,
			isCandidate: true,
			wantErr:     false,
		},
		{
			name:        "symbol prefix compact extra args error",
			text:        "/compact 123",
			prefix:      "/",
			isCandidate: true,
			wantErr:     true,
		},
		{
			name:        "symbol prefix reflect valid",
			text:        "/reflect",
			prefix:      "/",
			wantType:    CmdReflect,
			wantArgs:    nil,
			isCandidate: true,
			wantErr:     false,
		},
		{
			name:        "symbol prefix unknown command error",
			text:        "/foobar",
			prefix:      "/",
			isCandidate: true,
			wantErr:     true,
		},
		{
			name:        "symbol prefix only error",
			text:        "/",
			prefix:      "/",
			isCandidate: true,
			wantErr:     true,
		},
		{
			name:        "symbol prefix non-candidate text",
			text:        "hello world",
			prefix:      "/",
			isCandidate: false,
			wantErr:     false,
		},
		// Word prefix tests (e.g. execute)
		{
			name:        "word prefix valid with space",
			text:        "execute reset",
			prefix:      "execute",
			wantType:    CmdReset,
			wantArgs:    nil,
			isCandidate: true,
			wantErr:     false,
		},
		{
			name:        "word prefix case insensitive",
			text:        "ExEcUtE reset",
			prefix:      "execute",
			wantType:    CmdReset,
			wantArgs:    nil,
			isCandidate: true,
			wantErr:     false,
		},
		{
			name:        "word prefix without space is non-candidate",
			text:        "executereset",
			prefix:      "execute",
			isCandidate: false,
			wantErr:     false,
		},
		{
			name:        "word prefix alone error",
			text:        "execute",
			prefix:      "execute",
			isCandidate: true,
			wantErr:     true,
		},
		{
			name:        "word prefix ban valid",
			text:        "execute ban 10001",
			prefix:      "execute",
			wantType:    CmdBan,
			wantArgs:    []string{"10001"},
			isCandidate: true,
			wantErr:     false,
		},
		// Empty message
		{
			name:        "empty text",
			text:        "   ",
			prefix:      "/",
			isCandidate: false,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, isCand, err := ParseCandidate(tt.text, tt.prefix)
			if isCand != tt.isCandidate {
				t.Fatalf("ParseCandidate(%q, %q) isCandidate = %v; want %v", tt.text, tt.prefix, isCand, tt.isCandidate)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseCandidate(%q, %q) err = %v; wantErr = %v", tt.text, tt.prefix, err, tt.wantErr)
			}
			if isCand && !tt.wantErr {
				if cmd.Type != tt.wantType {
					t.Errorf("cmd.Type = %v; want %v", cmd.Type, tt.wantType)
				}
				if len(cmd.Args) != len(tt.wantArgs) {
					t.Fatalf("cmd.Args len = %d; want %d", len(cmd.Args), len(tt.wantArgs))
				}
				for i, arg := range cmd.Args {
					if arg != tt.wantArgs[i] {
						t.Errorf("cmd.Args[%d] = %q; want %q", i, arg, tt.wantArgs[i])
					}
				}
			}
		})
	}
}

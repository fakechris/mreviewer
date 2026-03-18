package commands

import (
	"testing"
)

// TestParse verifies the /ai-review command parser.
func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantNil  bool
		wantKind CommandKind
		wantArgs string
	}{
		{
			name:    "not a command",
			input:   "This is a normal comment",
			wantNil: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantNil: true,
		},
		{
			name:     "rerun command",
			input:    "/ai-review rerun",
			wantKind: CommandRerun,
		},
		{
			name:     "ignore command",
			input:    "/ai-review ignore",
			wantKind: CommandIgnore,
		},
		{
			name:     "resolve command",
			input:    "/ai-review resolve",
			wantKind: CommandResolve,
		},
		{
			name:     "focus command with path",
			input:    "/ai-review focus src/auth/",
			wantKind: CommandFocus,
			wantArgs: "src/auth/",
		},
		{
			name:     "focus command with spaces",
			input:    "/ai-review focus   src/auth/  ",
			wantKind: CommandFocus,
			wantArgs: "src/auth/",
		},
		{
			name:     "unknown command",
			input:    "/ai-review unknown-cmd",
			wantKind: CommandUnknown,
			wantArgs: "unknown-cmd",
		},
		{
			name:     "bare command (no subcommand)",
			input:    "/ai-review",
			wantKind: CommandUnknown,
		},
		{
			name:     "leading whitespace",
			input:    "  /ai-review rerun",
			wantKind: CommandRerun,
		},
		{
			name:     "case insensitive subcommand",
			input:    "/ai-review RERUN",
			wantKind: CommandRerun,
		},
		{
			name:    "prefix only match - different prefix",
			input:   "/ai-review-something",
			wantNil: true,
		},
		{
			name:     "rerun with extra args",
			input:    "/ai-review rerun now please",
			wantKind: CommandRerun,
			wantArgs: "now please",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := Parse(tc.input)
			if tc.wantNil {
				if result != nil {
					t.Errorf("expected nil, got %+v", result)
				}
				return
			}
			if result == nil {
				t.Fatal("expected non-nil result, got nil")
			}
			if result.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", result.Kind, tc.wantKind)
			}
			if result.Args != tc.wantArgs {
				t.Errorf("Args = %q, want %q", result.Args, tc.wantArgs)
			}
		})
	}
}

// TestIsCommand verifies the fast-path command check.
func TestIsCommand(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"/ai-review rerun", true},
		{"/ai-review ignore", true},
		{"/ai-review", true},
		{"  /ai-review focus path/", true},
		{"normal comment", false},
		{"", false},
		{"/other-command", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			if got := IsCommand(tc.input); got != tc.want {
				t.Errorf("IsCommand(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

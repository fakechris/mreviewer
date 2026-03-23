package notecommand

import "strings"

const commandPrefix = "/ai-review"

type CommandKind string

const (
	CommandRerun   CommandKind = "rerun"
	CommandIgnore  CommandKind = "ignore"
	CommandResolve CommandKind = "resolve"
	CommandFocus   CommandKind = "focus"
	CommandUnknown CommandKind = "unknown"
)

type ParsedCommand struct {
	Kind CommandKind
	Args string
}

func Parse(noteBody string) *ParsedCommand {
	trimmed := strings.TrimSpace(noteBody)
	if !strings.HasPrefix(trimmed, commandPrefix) {
		return nil
	}

	afterPrefix := trimmed[len(commandPrefix):]
	if len(afterPrefix) > 0 && afterPrefix[0] != ' ' && afterPrefix[0] != '\t' && afterPrefix[0] != '\n' {
		return nil
	}

	rest := strings.TrimSpace(afterPrefix)
	parts := strings.SplitN(rest, " ", 2)
	if len(parts) == 0 || parts[0] == "" {
		return &ParsedCommand{Kind: CommandUnknown}
	}

	keyword := strings.ToLower(parts[0])
	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}

	switch keyword {
	case "rerun":
		return &ParsedCommand{Kind: CommandRerun, Args: args}
	case "ignore":
		return &ParsedCommand{Kind: CommandIgnore, Args: args}
	case "resolve":
		return &ParsedCommand{Kind: CommandResolve, Args: args}
	case "focus":
		return &ParsedCommand{Kind: CommandFocus, Args: args}
	default:
		return &ParsedCommand{Kind: CommandUnknown, Args: rest}
	}
}

func IsCommand(noteBody string) bool {
	return strings.HasPrefix(strings.TrimSpace(noteBody), commandPrefix)
}

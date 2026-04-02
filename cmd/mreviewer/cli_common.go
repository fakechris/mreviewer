package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

var (
	cliVersion   = "dev"
	cliCommit    = ""
	cliBuildDate = ""
)

type commonCLIFlags struct {
	verbose int
}

func printTopLevelHelp(stdout io.Writer) {
	if stdout == nil {
		return
	}
	_, _ = fmt.Fprintln(stdout, "Usage: mreviewer <command> [options]")
	_, _ = fmt.Fprintln(stdout)
	_, _ = fmt.Fprintln(stdout, "Commands: review, init, doctor, serve")
	_, _ = fmt.Fprintln(stdout)
	_, _ = fmt.Fprintln(stdout, "Review:")
	_, _ = fmt.Fprintln(stdout, "  review      Review a GitHub or GitLab PR/MR (start here)")
	_, _ = fmt.Fprintln(stdout, "  serve       Run local webhook + admin runtime")
	_, _ = fmt.Fprintln(stdout)
	_, _ = fmt.Fprintln(stdout, "Setup:")
	_, _ = fmt.Fprintln(stdout, "  init        Generate a personal config template")
	_, _ = fmt.Fprintln(stdout, "  doctor      Validate config, database, LLM, and platform access")
	_, _ = fmt.Fprintln(stdout)
	_, _ = fmt.Fprintln(stdout, "Information:")
	_, _ = fmt.Fprintln(stdout, "  help        Show top-level or subcommand help")
	_, _ = fmt.Fprintln(stdout, "  version     Show CLI version")
	_, _ = fmt.Fprintln(stdout)
	_, _ = fmt.Fprintln(stdout, "Global flags:")
	_, _ = fmt.Fprintln(stdout, "  --help       Show top-level help")
	_, _ = fmt.Fprintln(stdout, "  --version    Show CLI version")
	_, _ = fmt.Fprintln(stdout, "  --verbose    Increase detail; repeat -vv/-vvv/-vvvv for debug traces")
	_, _ = fmt.Fprintln(stdout)
	_, _ = fmt.Fprintln(stdout, "Examples:")
	_, _ = fmt.Fprintln(stdout, "  mreviewer init --provider openai")
	_, _ = fmt.Fprintln(stdout, "  mreviewer doctor --json")
	_, _ = fmt.Fprintln(stdout, "  mreviewer review --target https://github.com/acme/repo/pull/17 --dry-run -vv")
	_, _ = fmt.Fprintln(stdout, "  mreviewer serve --config config.yaml --dry-run")
}

func printVersion(stdout io.Writer) {
	if stdout == nil {
		return
	}
	version := strings.TrimSpace(cliVersion)
	if version == "" {
		version = "dev"
	}
	_, _ = fmt.Fprintf(stdout, "mreviewer %s\n", version)
}

func extractCommonCLIFlags(args []string) ([]string, commonCLIFlags, error) {
	cleaned := make([]string, 0, len(args))
	flags := commonCLIFlags{}
	afterTerminator := false
	for _, arg := range args {
		if afterTerminator {
			cleaned = append(cleaned, arg)
			continue
		}
		switch {
		case arg == "--":
			afterTerminator = true
			cleaned = append(cleaned, arg)
		case arg == "--verbose":
			flags.verbose++
		case len(arg) >= 2 && strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--"):
			trimmed := strings.TrimPrefix(arg, "-")
			if trimmed != "" && strings.Trim(trimmed, "v") == "" {
				flags.verbose += len(trimmed)
				continue
			}
			cleaned = append(cleaned, arg)
		default:
			cleaned = append(cleaned, arg)
		}
	}
	return cleaned, flags, nil
}

func cliTracef(w io.Writer, verbose int, level int, format string, args ...any) {
	if w == nil || verbose < level {
		return
	}
	_, _ = fmt.Fprintf(w, format+"\n", args...)
}

func setFlagSetUsage(fs *flag.FlagSet, helpText string) {
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), strings.TrimSpace(helpText))
		_, _ = fmt.Fprintln(fs.Output())
		_, _ = fmt.Fprintln(fs.Output())
		fs.PrintDefaults()
	}
}

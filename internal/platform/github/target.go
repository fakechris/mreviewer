package github

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

func ResolveTarget(rawURL string) (core.ReviewTarget, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return core.ReviewTarget{}, fmt.Errorf("github adapter: parse url: %w", err)
	}

	parts := strings.SplitN(strings.Trim(parsed.Path, "/"), "/pull/", 2)
	if len(parts) != 2 {
		return core.ReviewTarget{}, fmt.Errorf("github adapter: invalid pull request url: %s", rawURL)
	}

	changeNumber, err := strconv.ParseInt(strings.Trim(parts[1], "/"), 10, 64)
	if err != nil {
		return core.ReviewTarget{}, fmt.Errorf("github adapter: parse pull request number: %w", err)
	}

	return core.ReviewTarget{
		Platform:     core.PlatformGitHub,
		URL:          strings.TrimSpace(rawURL),
		BaseURL:      strings.TrimRight(parsed.Scheme+"://"+parsed.Host, "/"),
		Repository:   strings.Trim(parts[0], "/"),
		ChangeNumber: changeNumber,
	}, nil
}

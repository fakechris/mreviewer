package main

import (
	"strings"
	"testing"

	"github.com/mreviewer/mreviewer/internal/config"
)

func TestValidateWorkerConfigRequiresGitLabToken(t *testing.T) {
	err := validateWorkerConfig(&config.Config{GitLabBaseURL: "https://gitlab.example.com"})
	if err == nil {
		t.Fatal("expected missing token validation error")
	}
	if !strings.Contains(err.Error(), "GITLAB_TOKEN") {
		t.Fatalf("error = %q, want GITLAB_TOKEN mention", err.Error())
	}
}

func TestValidateWorkerConfigAllowsConfiguredToken(t *testing.T) {
	err := validateWorkerConfig(&config.Config{GitLabBaseURL: "https://gitlab.example.com", GitLabToken: "secret-token"})
	if err != nil {
		t.Fatalf("validateWorkerConfig: %v", err)
	}
}

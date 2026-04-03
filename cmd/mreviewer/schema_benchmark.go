package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mreviewer/mreviewer/internal/config"
	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/llm"
)

type schemaBenchmarkRouteSummary struct {
	Route                 string         `json:"route"`
	Model                 string         `json:"model"`
	Requests              int            `json:"requests"`
	InitialSchemaAccuracy float64        `json:"initial_schema_accuracy"`
	RepairRate            float64        `json:"repair_rate"`
	FinalSuccessRate      float64        `json:"final_success_rate"`
	FailureReasons        map[string]int `json:"failure_reasons"`
}

type schemaBenchmarkOutput struct {
	Routes []schemaBenchmarkRouteSummary `json:"routes"`
}

func runSchemaBenchmarkCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("schema-benchmark", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagSetUsage(fs, `
Usage: mreviewer schema-benchmark --routes <route[,route...]> --input <requests.jsonl> [options]

Benchmark configured provider routes against the first-class review schema harness.
Outputs Wonder Verifier-style summary metrics as JSON.
`)
	var configPath string
	var routesCSV string
	var inputPath string
	fs.StringVar(&configPath, "config", "config.yaml", "Path to config file")
	fs.StringVar(&routesCSV, "routes", "", "Comma separated configured model routes")
	fs.StringVar(&inputPath, "input", "", "JSONL file containing serialized ReviewRequest payloads")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if strings.TrimSpace(routesCSV) == "" {
		_, _ = fmt.Fprintln(stderr, "--routes is required")
		return 2
	}
	if strings.TrimSpace(inputPath) == "" {
		_, _ = fmt.Fprintln(stderr, "--input is required")
		return 2
	}
	if extra := fs.Args(); len(extra) > 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected positional arguments: %s\n", strings.Join(extra, ", "))
		return 2
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load config failed: %v\n", err)
		return 1
	}
	providers, err := config.BuildProviderConfigs(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build providers failed: %v\n", err)
		return 1
	}
	requests, err := loadSchemaBenchmarkRequests(inputPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load requests failed: %v\n", err)
		return 1
	}

	var output schemaBenchmarkOutput
	for _, route := range splitCSVArg(routesCSV) {
		cfg, ok := providers[route]
		if !ok {
			_, _ = fmt.Fprintf(stderr, "route %q is not configured\n", route)
			return 1
		}
		cfg.RouteName = route
		provider, err := llm.NewProviderFromConfig(cfg)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "build provider %q failed: %v\n", route, err)
			return 1
		}

		summary := schemaBenchmarkRouteSummary{
			Route:          route,
			Model:          cfg.Model,
			Requests:       len(requests),
			FailureReasons: map[string]int{},
		}
		var initialValidCount int
		var repairCount int
		var successCount int

		for _, request := range requests {
			response, reviewErr := provider.Review(context.Background(), request)
			report := response.SchemaReport
			if report == nil {
				report = llm.SchemaReportFromError(reviewErr)
			}
			if report != nil {
				if report.Initial.Valid {
					initialValidCount++
				}
				if report.RepairAttempted {
					repairCount++
				}
			}
			if reviewErr == nil {
				successCount++
				continue
			}
			reason := strings.TrimSpace(reviewErr.Error())
			if reason == "" {
				reason = "unknown_error"
			}
			summary.FailureReasons[reason]++
		}

		if summary.Requests > 0 {
			summary.InitialSchemaAccuracy = float64(initialValidCount) / float64(summary.Requests)
			summary.RepairRate = float64(repairCount) / float64(summary.Requests)
			summary.FinalSuccessRate = float64(successCount) / float64(summary.Requests)
		}
		output.Routes = append(output.Routes, summary)
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		_, _ = fmt.Fprintf(stderr, "encode output failed: %v\n", err)
		return 1
	}
	return 0
}

func loadSchemaBenchmarkRequests(path string) ([]ctxpkg.ReviewRequest, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var requests []ctxpkg.ReviewRequest
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var request ctxpkg.ReviewRequest
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			return nil, fmt.Errorf("parse jsonl line: %w", err)
		}
		requests = append(requests, request)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return requests, nil
}

func splitCSVArg(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

# Anthropic Go SDK Research Report

## 1. Official Anthropic Go SDK

**Yes, there is an official Go SDK** maintained by Anthropic.

| Detail | Value |
|---|---|
| **Repository** | https://github.com/anthropics/anthropic-sdk-go |
| **Import path** | `github.com/anthropics/anthropic-sdk-go` (imported as `anthropic`) |
| **Latest version** | `v1.26.0` (released 2026-02-19) |
| **Go requirement** | Go 1.22+ |
| **License** | MIT |
| **Stars** | ~900 |
| **Used by** | 1,000+ projects |

Install:
```bash
go get -u 'github.com/anthropics/anthropic-sdk-go@v1.26.0'
```

Import:
```go
import (
    "github.com/anthropics/anthropic-sdk-go"        // imported as anthropic
    "github.com/anthropics/anthropic-sdk-go/option"  // request options
)
```

---

## 2. Custom Base URL Support

The SDK **natively supports custom base URLs** via `option.WithBaseURL()`. This is perfect for the MiniMax Anthropic-compatible API.

```go
client := anthropic.NewClient(
    option.WithBaseURL("https://api.minimaxi.com/anthropic"),
    option.WithAPIKey("your-minimax-api-key"),
)
```

The `option.WithAPIKey()` sets the `x-api-key` header. The SDK automatically includes `anthropic-version: 2023-06-01`.

You can also set custom headers if needed:
```go
client := anthropic.NewClient(
    option.WithBaseURL("https://api.minimaxi.com/anthropic"),
    option.WithAPIKey("your-minimax-api-key"),
    option.WithHeader("anthropic-version", "2023-06-01"),
)
```

---

## 3. Making Messages API Calls (Non-Streaming)

### Basic Usage

```go
package main

import (
    "context"
    "fmt"

    "github.com/anthropics/anthropic-sdk-go"
    "github.com/anthropics/anthropic-sdk-go/option"
)

func main() {
    client := anthropic.NewClient(
        option.WithBaseURL("https://api.minimaxi.com/anthropic"),
        option.WithAPIKey("your-minimax-api-key"),
    )

    message, err := client.Messages.New(context.TODO(), anthropic.MessageNewParams{
        Model:     "MiniMax-M2.5", // custom model string (anthropic.Model is just a string type)
        MaxTokens: 1024,
        Messages: []anthropic.MessageParam{
            anthropic.NewUserMessage(anthropic.NewTextBlock("What is a quaternion?")),
        },
    })
    if err != nil {
        panic(err)
    }

    // Access text content
    for _, block := range message.Content {
        switch b := block.AsAny().(type) {
        case anthropic.TextBlock:
            fmt.Println(b.Text)
        }
    }
}
```

### With System Prompt

```go
message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
    Model:     "MiniMax-M2.5",
    MaxTokens: 1024,
    System: []anthropic.TextBlockParam{
        {Text: "You are a code review assistant."},
    },
    Messages: []anthropic.MessageParam{
        anthropic.NewUserMessage(anthropic.NewTextBlock("Review this code...")),
    },
})
```

### Multi-Turn Conversations

```go
messages := []anthropic.MessageParam{
    anthropic.NewUserMessage(anthropic.NewTextBlock("Hello")),
}

message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
    Model:     "MiniMax-M2.5",
    MaxTokens: 1024,
    Messages:  messages,
})

// Append assistant response and continue
messages = append(messages, message.ToParam())
messages = append(messages, anthropic.NewUserMessage(
    anthropic.NewTextBlock("Follow up question..."),
))
```

### Key Request Parameters (`MessageNewParams`)

| Field | Type | Required | Notes |
|---|---|---|---|
| `MaxTokens` | `int64` | Yes | Always required |
| `Messages` | `[]MessageParam` | Yes | Conversation messages |
| `Model` | `Model` (string) | Yes | e.g. `"MiniMax-M2.5"` |
| `Temperature` | `param.Opt[float64]` | No | 0.0-1.0, default 1.0 |
| `TopP` | `param.Opt[float64]` | No | Nucleus sampling |
| `TopK` | `param.Opt[int64]` | No | Top-K sampling |
| `System` | `[]TextBlockParam` | No | System prompt |
| `StopSequences` | `[]string` | No | Custom stop strings |
| `OutputConfig` | `OutputConfigParam` | No | Structured output config |

### Key Response Structure (`Message`)

| Field | Type | Notes |
|---|---|---|
| `Content` | `[]ContentBlockUnion` | Text, tool use, etc. |
| `ID` | `string` | Message ID |
| `Model` | `Model` | Model used |
| `Role` | `MessageRole` | Always "assistant" |
| `StopReason` | `MessageStopReason` | "end_turn", "max_tokens", "stop_sequence" |
| `Usage` | `Usage` | Token counts |

---

## 4. Streaming Responses

```go
stream := client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
    Model:     "MiniMax-M2.5",
    MaxTokens: 1024,
    Messages: []anthropic.MessageParam{
        anthropic.NewUserMessage(anthropic.NewTextBlock("Explain streaming...")),
    },
})

// Accumulate into a full message
message := anthropic.Message{}
for stream.Next() {
    event := stream.Current()
    err := message.Accumulate(event)
    if err != nil {
        panic(err)
    }

    // Process real-time text deltas
    switch eventVariant := event.AsAny().(type) {
    case anthropic.ContentBlockDeltaEvent:
        switch deltaVariant := eventVariant.Delta.AsAny().(type) {
        case anthropic.TextDelta:
            fmt.Print(deltaVariant.Text) // Print as it arrives
        }
    }
}

if stream.Err() != nil {
    panic(stream.Err())
}

// `message` now has the fully accumulated response
fmt.Println("\n\nFull text:", message.Content[0].Text)
```

Key points:
- `client.Messages.NewStreaming()` returns a stream object
- Use `stream.Next()` to iterate over SSE events
- `message.Accumulate(event)` builds up the full message
- Check `stream.Err()` after the loop
- SDK recommends streaming for long-running requests

---

## 5. Structured JSON Output

The SDK supports structured outputs via `OutputConfig.Format` (GA since v1.20.0).

### Using `OutputConfigParam` with `JSONOutputFormatParam`

```go
message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
    Model:     "MiniMax-M2.5",
    MaxTokens: 1024,
    Messages: []anthropic.MessageParam{
        anthropic.NewUserMessage(anthropic.NewTextBlock("Extract info from: John Smith (john@example.com) wants Enterprise plan")),
    },
    OutputConfig: anthropic.OutputConfigParam{
        Format: anthropic.JSONOutputFormatParam{
            Schema: map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "name":         map[string]any{"type": "string"},
                    "email":        map[string]any{"type": "string"},
                    "plan_interest": map[string]any{"type": "string"},
                },
                "required":             []string{"name", "email", "plan_interest"},
                "additionalProperties": false,
            },
        },
    },
})
if err != nil {
    panic(err)
}

// Response is valid JSON in message.Content[0].Text
var result map[string]any
json.Unmarshal([]byte(message.Content[0].Text), &result)
```

### Parsing Structured JSON Response

```go
import "encoding/json"

// Define your Go struct matching the schema
type ReviewResult struct {
    Summary  string   `json:"summary"`
    Issues   []Issue  `json:"issues"`
    Score    int      `json:"score"`
}
type Issue struct {
    File     string `json:"file"`
    Line     int    `json:"line"`
    Severity string `json:"severity"`
    Message  string `json:"message"`
}

// After getting the response:
var review ReviewResult
if err := json.Unmarshal([]byte(message.Content[0].Text), &review); err != nil {
    // handle parse error
}
```

### Important Notes for MiniMax Compatibility

- MiniMax may or may not support the `output_config.format` field for constrained decoding
- **Fallback approach**: Use strong system prompts + `stop_sequences` to enforce JSON:

```go
message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
    Model:     "MiniMax-M2.5",
    MaxTokens: 4096,
    System: []anthropic.TextBlockParam{
        {Text: `You MUST respond with ONLY valid JSON matching this schema:
{
  "summary": "string",
  "issues": [{"file": "string", "line": "number", "severity": "string", "message": "string"}],
  "score": "number"
}
Do not include any text outside the JSON.`},
    },
    Messages: []anthropic.MessageParam{
        anthropic.NewUserMessage(anthropic.NewTextBlock("Review this merge request...")),
    },
})
```

---

## 6. Error Handling

### Error Types and Retries

The SDK provides built-in retry logic with exponential backoff:

```go
// Default: 2 retries
// Auto-retried errors: connection errors, 408, 409, 429, 5xx
client := anthropic.NewClient(
    option.WithBaseURL("https://api.minimaxi.com/anthropic"),
    option.WithAPIKey("your-key"),
    option.WithMaxRetries(3), // customize retries (default is 2)
)
```

### Error Inspection

```go
import "errors"

message, err := client.Messages.New(ctx, params)
if err != nil {
    var apierr *anthropic.Error
    if errors.As(err, &apierr) {
        fmt.Println("Status code:", apierr.StatusCode)
        fmt.Println("Request ID:", apierr.RequestID)
        
        switch apierr.StatusCode {
        case 400:
            // Bad request - invalid params
            fmt.Println("Bad request:", string(apierr.DumpResponse(true)))
        case 401:
            // Authentication error
            fmt.Println("Auth failed - check API key")
        case 429:
            // Rate limited - SDK auto-retries this
            fmt.Println("Rate limited after retries")
        case 500, 502, 503:
            // Server error - SDK auto-retries this
            fmt.Println("Server error after retries")
        case 529:
            // Overloaded
            fmt.Println("API overloaded")
        }
    }
    // Non-API errors (network, DNS, timeout, etc.)
    return err
}
```

### Timeout Configuration

```go
// Overall request timeout (spans all retries)
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
defer cancel()

message, err := client.Messages.New(ctx, params,
    // Per-retry timeout
    option.WithRequestTimeout(30*time.Second),
)
```

### Retry Summary Table

| Error | Auto-Retried? |
|---|---|
| Connection errors | ✅ Yes |
| 408 Request Timeout | ✅ Yes |
| 409 Conflict | ✅ Yes |
| 429 Rate Limit | ✅ Yes |
| 5xx Server Errors | ✅ Yes |
| 400 Bad Request | ❌ No |
| 401 Unauthorized | ❌ No |
| 403 Forbidden | ❌ No |
| 404 Not Found | ❌ No |

---

## 7. Recommendation for MiniMax Integration

### Recommended Approach: Use the Official SDK with Custom Base URL

```go
package main

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "time"

    "github.com/anthropics/anthropic-sdk-go"
    "github.com/anthropics/anthropic-sdk-go/option"
)

func NewMiniMaxClient(apiKey string) *anthropic.Client {
    return anthropic.NewClient(
        option.WithBaseURL("https://api.minimaxi.com/anthropic"),
        option.WithAPIKey(apiKey),
        option.WithMaxRetries(3),
    )
}

func ReviewMR(ctx context.Context, client *anthropic.Client, diff string) (*ReviewResult, error) {
    ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
    defer cancel()

    message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
        Model:     "MiniMax-M2.5",
        MaxTokens: 4096,
        System: []anthropic.TextBlockParam{
            {Text: "You are a code review assistant. Respond with valid JSON only."},
        },
        Messages: []anthropic.MessageParam{
            anthropic.NewUserMessage(anthropic.NewTextBlock(
                fmt.Sprintf("Review this merge request diff:\n\n%s", diff),
            )),
        },
        // Try structured output (may not be supported by MiniMax)
        OutputConfig: anthropic.OutputConfigParam{
            Format: anthropic.JSONOutputFormatParam{
                Schema: map[string]any{
                    "type": "object",
                    "properties": map[string]any{
                        "summary": map[string]any{"type": "string"},
                        "issues": map[string]any{
                            "type": "array",
                            "items": map[string]any{
                                "type": "object",
                                "properties": map[string]any{
                                    "file":     map[string]any{"type": "string"},
                                    "line":     map[string]any{"type": "integer"},
                                    "severity": map[string]any{"type": "string", "enum": []string{"critical", "major", "minor", "info"}},
                                    "message":  map[string]any{"type": "string"},
                                },
                                "required":             []string{"file", "severity", "message"},
                                "additionalProperties": false,
                            },
                        },
                        "score": map[string]any{"type": "integer"},
                    },
                    "required":             []string{"summary", "issues", "score"},
                    "additionalProperties": false,
                },
            },
        },
    },
        option.WithRequestTimeout(60*time.Second),
    )
    if err != nil {
        var apierr *anthropic.Error
        if errors.As(err, &apierr) {
            return nil, fmt.Errorf("API error %d: %s", apierr.StatusCode, apierr.RequestID)
        }
        return nil, fmt.Errorf("request failed: %w", err)
    }

    // Extract text from response
    var responseText string
    for _, block := range message.Content {
        if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
            responseText = tb.Text
            break
        }
    }

    var result ReviewResult
    if err := json.Unmarshal([]byte(responseText), &result); err != nil {
        return nil, fmt.Errorf("failed to parse response JSON: %w", err)
    }

    return &result, nil
}

type ReviewResult struct {
    Summary string  `json:"summary"`
    Issues  []Issue `json:"issues"`
    Score   int     `json:"score"`
}

type Issue struct {
    File     string `json:"file"`
    Line     int    `json:"line,omitempty"`
    Severity string `json:"severity"`
    Message  string `json:"message"`
}
```

### Why the Official SDK Over Raw HTTP

1. **Built-in retry logic** with exponential backoff for 429/5xx
2. **Custom base URL support** via `option.WithBaseURL()`
3. **Proper SSE streaming** support with accumulator pattern
4. **Type-safe request/response** structs
5. **Automatic header management** (`x-api-key`, `anthropic-version`, `content-type`)
6. **Middleware support** for logging, metrics, etc.
7. **Actively maintained** (v1.26.0, 54 releases, weekly updates)

### Dependencies Added

```
github.com/anthropics/anthropic-sdk-go v1.26.0
github.com/tidwall/sjson  (transitive - used for JSON path operations)
```

---

## 8. Key Import Paths Summary

| Package | Import | Purpose |
|---|---|---|
| Main SDK | `github.com/anthropics/anthropic-sdk-go` | Core types & client (imported as `anthropic`) |
| Options | `github.com/anthropics/anthropic-sdk-go/option` | `WithBaseURL`, `WithAPIKey`, `WithMaxRetries`, etc. |
| Param helpers | `github.com/anthropics/anthropic-sdk-go/packages/param` | `param.Opt[T]`, `param.Null[T]()` |
| Response JSON | `github.com/anthropics/anthropic-sdk-go/packages/respjson` | `respjson.Field` for metadata |
| Bedrock | `github.com/anthropics/anthropic-sdk-go/bedrock` | AWS Bedrock integration |
| Vertex | `github.com/anthropics/anthropic-sdk-go/vertex` | Google Vertex AI integration |
| Tool Runner | `github.com/anthropics/anthropic-sdk-go/toolrunner` | Automatic tool use loops |

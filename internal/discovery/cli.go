package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The cli backend shells out to the local `claude` CLI in headless print
// mode (`claude -p ... --output-format json`), which authenticates with the
// machine's logged-in Claude subscription instead of a metered API key.
// There is no forced tool use in this mode, so the prompt itself carries the
// output contract and the reply is parsed as bare JSON.

// cliResult is the wrapper `claude -p --output-format json` prints: the
// model's final text is in Result, with billing/usage metadata around it.
type cliResult struct {
	IsError    bool   `json:"is_error"`
	Result     string `json:"result"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
	ModelUsage map[string]json.RawMessage `json:"modelUsage"`
}

func (e *Extractor) callCLI(ctx context.Context, imagePath string) (callResult, error) {
	absImage, err := filepath.Abs(imagePath)
	if err != nil {
		return callResult{}, err
	}
	schemaJSON, _ := json.MarshalIndent(recordPageTool.InputSchema.Properties, "", "  ")
	prompt := fmt.Sprintf(`%s

The page scan is the image file at: %s
Use the Read tool to view that file, then reply with ONLY one JSON object — no markdown fences, no commentary before or after. Its shape (JSON Schema properties; include every field, using "" or [] when a value is absent):

%s`, pagePrompt, absImage, schemaJSON)

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "-p", prompt, "--output-format", "json")
	// ANTHROPIC_API_KEY takes precedence over the claude.ai login inside the
	// CLI — with it set, the CLI bills the (possibly unfunded) API key. Strip
	// it so the subprocess authenticates as the logged-in subscription.
	cmd.Env = environWithout("ANTHROPIC_API_KEY")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return callResult{}, fmt.Errorf("claude CLI: %v: %s", err, firstLine(stderr.String()))
	}

	var wrapper cliResult
	if err := json.Unmarshal(stdout.Bytes(), &wrapper); err != nil {
		return callResult{}, fmt.Errorf("claude CLI: unparseable wrapper: %w", err)
	}
	if wrapper.IsError {
		return callResult{}, fmt.Errorf("claude CLI: %s", firstLine(wrapper.Result))
	}

	// Without forced tool use the model can still wrap the JSON in fences or
	// a stray sentence; take the outermost {...} span before parsing.
	body := wrapper.Result
	if start, end := strings.Index(body, "{"), strings.LastIndex(body, "}"); start >= 0 && end > start {
		body = body[start : end+1]
	}
	var extract PageExtract
	if err := json.Unmarshal([]byte(body), &extract); err != nil {
		return callResult{}, fmt.Errorf("claude CLI: reply is not the expected JSON: %w", err)
	}

	model := "claude-cli"
	for name := range wrapper.ModelUsage {
		model = "claude-cli/" + name
	}
	return callResult{
		extract: extract, model: model, stopReason: wrapper.StopReason,
		inputTokens: wrapper.Usage.InputTokens, outputTokens: wrapper.Usage.OutputTokens,
	}, nil
}

// environWithout is os.Environ() minus the named variables.
func environWithout(names ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		drop := false
		for _, name := range names {
			if strings.HasPrefix(kv, name+"=") {
				drop = true
			}
		}
		if !drop {
			env = append(env, kv)
		}
	}
	return env
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

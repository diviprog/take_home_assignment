package discovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// The page-level survey labels an item only from the page where it STARTS;
// continuation pages contribute prose to the stitched description but no
// labels. For single-page items that is exact. For multi-page items — the
// four artifacts open with a full page of lore — it means the labeled fields
// systematically miss the mechanics on later pages. Relabel closes that gap:
// after stitching, any item whose prose crossed a page break gets one more
// model pass over its complete text, and the label lists are re-derived from
// everything the item actually says. Same cache discipline as the page
// extractor, so re-runs are free.

const relabelPrompt = `You are labeling one complete magic-item entry from a fantasy catalog. The item's full body text is given below (it was transcribed verbatim from scans; it may include long lore/backstory sections before any mechanics).

Read ALL of it — mechanics often appear only after pages of lore — and fill the label fields:

1. effect_kinds_raw: 1-6 short labels in your own words for what the item does mechanically (e.g. "damage bonus", "fear on others", "movement", "healing"). Base them on effect text anywhere in the body. If the body truly contains no mechanics, return an empty list.
2. creatures_mentioned / environments_mentioned / spells_mentioned: verbatim nouns the mechanics reference (a bonus against undead, works only in forests, casts a named spell). Flavor-text mentions don't count.
3. limitations_raw: verbatim usage limits ("3 times per day", "until the next dawn", "regains 1d3 charges at midnight"), each as its own list entry.
4. charges_text / cursed_text / variant_table_text: verbatim charge mechanics, curse-or-drawback wording, and variant tables (state the dimension the variants vary over). Empty string if absent.
5. oddities: anything structurally notable that fits no other field.

Copy the source's exact words in the verbatim fields, including spelling errors. Only effect_kinds_raw is in your own words.`

var recordItemLabelsTool = anthropic.ToolParam{
	Name:        "record_item_labels",
	Description: anthropic.String("Record the label fields for one complete catalog item."),
	InputSchema: anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"effect_kinds_raw":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"creatures_mentioned":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"environments_mentioned": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"spells_mentioned":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"limitations_raw":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"charges_text":           map[string]any{"type": "string"},
			"cursed_text":            map[string]any{"type": "string"},
			"variant_table_text":     map[string]any{"type": "string"},
			"oddities":               map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		Required: []string{"effect_kinds_raw", "creatures_mentioned", "environments_mentioned",
			"spells_mentioned", "limitations_raw", "charges_text", "cursed_text",
			"variant_table_text", "oddities"},
	},
}

// ItemLabels is the relabeled subset of RawItem fields.
type ItemLabels struct {
	EffectKindsRaw        []string `json:"effect_kinds_raw"`
	CreaturesMentioned    []string `json:"creatures_mentioned"`
	EnvironmentsMentioned []string `json:"environments_mentioned"`
	SpellsMentioned       []string `json:"spells_mentioned"`
	LimitationsRaw        []string `json:"limitations_raw"`
	ChargesText           string   `json:"charges_text"`
	CursedText            string   `json:"cursed_text"`
	VariantTableText      string   `json:"variant_table_text"`
	Oddities              []string `json:"oddities"`
}

type relabelCacheEntry struct {
	Name         string     `json:"name"`
	Model        string     `json:"model"`
	InputHash    string     `json:"input_hash"`
	StopReason   string     `json:"stop_reason"`
	InputTokens  int64      `json:"input_tokens"`
	OutputTokens int64      `json:"output_tokens"`
	Labels       ItemLabels `json:"labels"`
}

// Relabeler runs the deep-label pass with the same dual-backend + disk-cache
// pattern as the page Extractor and the vocab Proposer.
type Relabeler struct {
	client   anthropic.Client
	cacheDir string
	backend  string
}

func NewRelabeler(cacheDir, backend string) (*Relabeler, error) {
	switch backend {
	case "api":
		if os.Getenv("ANTHROPIC_API_KEY") == "" {
			return nil, fmt.Errorf("backend api: ANTHROPIC_API_KEY is not set")
		}
	case "cli":
		if _, err := exec.LookPath("claude"); err != nil {
			return nil, fmt.Errorf("backend cli: claude CLI not found in PATH")
		}
	default:
		return nil, fmt.Errorf("unknown backend %q (want api or cli)", backend)
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	return &Relabeler{client: anthropic.NewClient(option.WithMaxRetries(4)), cacheDir: cacheDir, backend: backend}, nil
}

// NeedsRelabel reports whether an item's labels came from an incomplete read:
// its prose crossed at least one page break.
func NeedsRelabel(it StitchedItem) bool {
	return it.PdfPageEnd > it.PdfPageStart
}

// Relabel returns fresh labels for one item's complete text, from cache when
// the text hasn't changed. The merge into the item is the caller's business.
func (r *Relabeler) Relabel(ctx context.Context, it StitchedItem, refresh bool) (ItemLabels, bool, error) {
	input := fmt.Sprintf("Item: %s\nType line: %s\n\nFull body text:\n%s", it.Name, it.TypeLine, it.Description)

	modelID := string(extractionModel)
	if r.backend == "cli" {
		modelID = "claude-cli"
	}
	schemaJSON, _ := json.Marshal(recordItemLabelsTool.InputSchema.Properties)
	h := sha256.New()
	h.Write([]byte(modelID))
	h.Write([]byte(relabelPrompt))
	h.Write(schemaJSON)
	h.Write([]byte(input))
	hash := hex.EncodeToString(h.Sum(nil))[:12]
	slug := strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			return c
		}
		if c >= 'A' && c <= 'Z' {
			return c + 32
		}
		return '-'
	}, it.Name)
	cachePath := filepath.Join(r.cacheDir, fmt.Sprintf("%s.%s.json", slug, hash))

	if !refresh {
		if raw, err := os.ReadFile(cachePath); err == nil {
			var entry relabelCacheEntry
			if err := json.Unmarshal(raw, &entry); err == nil {
				return entry.Labels, true, nil
			}
		}
	}

	var labels ItemLabels
	var model, stopReason string
	var inTok, outTok int64
	var err error
	if r.backend == "cli" {
		labels, model, stopReason, inTok, outTok, err = r.relabelCLI(ctx, input)
	} else {
		labels, model, stopReason, inTok, outTok, err = r.relabelAPI(ctx, input)
	}
	if err != nil {
		return ItemLabels{}, false, fmt.Errorf("relabel %q: %w", it.Name, err)
	}

	entry := relabelCacheEntry{
		Name: it.Name, Model: model, InputHash: hash, StopReason: stopReason,
		InputTokens: inTok, OutputTokens: outTok, Labels: labels,
	}
	raw, _ := json.MarshalIndent(entry, "", "  ")
	if err := os.WriteFile(cachePath, raw, 0o644); err != nil {
		return ItemLabels{}, false, err
	}
	return labels, false, nil
}

// Merge folds relabeled fields into the item: lists are replaced when the
// relabel found anything (its read is a superset of the first page's), and
// scalar texts are filled only if the page pass left them empty — a verbatim
// scalar captured at page level is already exact.
func (l ItemLabels) Merge(it *StitchedItem) {
	if len(l.EffectKindsRaw) > 0 {
		it.EffectKindsRaw = l.EffectKindsRaw
	}
	if len(l.CreaturesMentioned) > 0 {
		it.CreaturesMentioned = l.CreaturesMentioned
	}
	if len(l.EnvironmentsMentioned) > 0 {
		it.EnvironmentsMentioned = l.EnvironmentsMentioned
	}
	if len(l.SpellsMentioned) > 0 {
		it.SpellsMentioned = l.SpellsMentioned
	}
	if len(l.LimitationsRaw) > 0 {
		it.LimitationsRaw = l.LimitationsRaw
	}
	if strings.TrimSpace(it.ChargesText) == "" {
		it.ChargesText = l.ChargesText
	}
	if strings.TrimSpace(it.CursedText) == "" {
		it.CursedText = l.CursedText
	}
	if strings.TrimSpace(it.VariantTableText) == "" {
		it.VariantTableText = l.VariantTableText
	}
	it.Oddities = append(it.Oddities, l.Oddities...)
}

func (r *Relabeler) relabelAPI(ctx context.Context, input string) (ItemLabels, string, string, int64, int64, error) {
	msg, err := r.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:       extractionModel,
		MaxTokens:   8192,
		Temperature: anthropic.Float(0),
		ToolChoice:  anthropic.ToolChoiceParamOfTool(recordItemLabelsTool.Name),
		Tools:       []anthropic.ToolUnionParam{{OfTool: &recordItemLabelsTool}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(relabelPrompt + "\n\n" + input)),
		},
	})
	if err != nil {
		return ItemLabels{}, "", "", 0, 0, err
	}
	for _, block := range msg.Content {
		if block.Type == "tool_use" && block.Name == recordItemLabelsTool.Name {
			var labels ItemLabels
			if err := json.Unmarshal(block.Input, &labels); err != nil {
				return ItemLabels{}, "", "", 0, 0, fmt.Errorf("bad tool input: %w", err)
			}
			return labels, string(extractionModel), string(msg.StopReason),
				msg.Usage.InputTokens, msg.Usage.OutputTokens, nil
		}
	}
	return ItemLabels{}, "", "", 0, 0, fmt.Errorf("no tool_use block (stop_reason=%s)", msg.StopReason)
}

func (r *Relabeler) relabelCLI(ctx context.Context, input string) (ItemLabels, string, string, int64, int64, error) {
	schemaJSON, _ := json.MarshalIndent(recordItemLabelsTool.InputSchema.Properties, "", "  ")
	full := fmt.Sprintf(`%s

%s

Reply with ONLY one JSON object — no markdown fences, no commentary before or after. Its shape (JSON Schema properties; include every field, using "" or [] when a value is absent):

%s`, relabelPrompt, input, schemaJSON)

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "-p", full, "--output-format", "json")
	cmd.Env = environWithout("ANTHROPIC_API_KEY")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return ItemLabels{}, "", "", 0, 0, fmt.Errorf("claude CLI: %v: %s", err, firstLine(stderr.String()))
	}

	var wrapper cliResult
	if err := json.Unmarshal(stdout.Bytes(), &wrapper); err != nil {
		return ItemLabels{}, "", "", 0, 0, fmt.Errorf("claude CLI: unparseable wrapper: %w", err)
	}
	if wrapper.IsError {
		return ItemLabels{}, "", "", 0, 0, fmt.Errorf("claude CLI: %s", firstLine(wrapper.Result))
	}
	body := wrapper.Result
	if start, end := strings.Index(body, "{"), strings.LastIndex(body, "}"); start >= 0 && end > start {
		body = body[start : end+1]
	}
	var labels ItemLabels
	if err := json.Unmarshal([]byte(body), &labels); err != nil {
		return ItemLabels{}, "", "", 0, 0, fmt.Errorf("claude CLI: reply is not the expected JSON (%v); stop_reason=%s reply starts: %s",
			err, wrapper.StopReason, firstLine(wrapper.Result))
	}
	model := "claude-cli"
	for name := range wrapper.ModelUsage {
		model = "claude-cli/" + name
	}
	return labels, model, wrapper.StopReason, wrapper.Usage.InputTokens, wrapper.Usage.OutputTokens, nil
}

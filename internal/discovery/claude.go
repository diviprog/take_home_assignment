package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const extractionModel = anthropic.ModelClaudeOpus5

// pagePrompt is identical for every page — the page-specific input is the
// image itself. Keeping it constant keeps the cache key simple and the run
// reproducible.
const pagePrompt = `You are transcribing one page of a scanned fantasy magic-item catalog into a structured JSON record.

How the catalog is laid out: each item starts with a TITLE IN SMALL CAPS followed immediately by an italic type line like "Wondrous item, uncommon (requires attunement)" or "Weapon (any sword), very rare". Everything after that, until the next title+type-line pair, is that item's body prose. Bold or italic sub-headings inside the prose (e.g. "Immunities.", "Curse.") are NOT new items — a new item requires the title + italic type line pair.

Rules:
1. VERBATIM. Copy the source's exact words everywhere, including spelling errors (e.g. "Wonderous"), inconsistent capitalization, and odd phrasing. Do not correct, summarize, or translate into your own vocabulary — except in effect_kinds_raw, where you write your own short labels.
2. If the page begins mid-sentence or mid-item (prose before any title), put that text in leading_continuation_text. If the whole page is continuation (no titles at all), the entire page body goes there and items is empty.
3. For each item, split the italic type line into category_raw, subtype_raw (the parenthetical, e.g. "any sword" — empty if none), rarity_raw, and attunement_raw (the attunement parenthetical verbatim, e.g. "requires attunement by a bard" — empty if absent). Also keep the whole line in type_line.
4. worn_or_held_raw: only if the text states where the item is worn or held, give the source's phrase; otherwise empty.
5. description: the item's complete body prose verbatim, including sub-headed paragraphs and any tables (render tables as "col: value" lines).
6. effect_kinds_raw: 1-4 short labels in your own words for what the item does mechanically (e.g. "damage bonus", "fear on others", "movement", "healing"). Base them on the effect text, never on what kind of object it is.
7. creatures_mentioned / environments_mentioned / spells_mentioned: verbatim nouns the mechanics reference (a bonus against undead, works only in forests, casts a named spell). Flavor-text mentions don't count.
8. limitations_raw: verbatim usage limits ("3 times per day", "until the next dawn", "regains 1d3 charges at midnight").
9. charges_text / cursed_text / variant_table_text: verbatim charge mechanics, curse wording, and variant tables (state the dimension the variants vary over). Empty if absent.
10. oddities: anything structurally notable that fits no other field.
11. printed_page_number: the page number printed on the scan itself, as a string; empty if none is visible.
12. last_item_continues: true if the final item's prose appears to be cut off by the page break.
13. Ignore watermarks and order numbers stamped by the print shop.`

// recordPageTool forces the model's answer into the PageExtract shape. Forced
// tool use (tool_choice) means the response is always a single JSON object
// matching this schema — no prose to strip, no markdown fences to parse.
var recordPageTool = anthropic.ToolParam{
	Name:        "record_page",
	Description: anthropic.String("Record the structured transcription of one catalog page."),
	InputSchema: anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"printed_page_number":       map[string]any{"type": "string"},
			"leading_continuation_text": map[string]any{"type": "string"},
			"last_item_continues":       map[string]any{"type": "boolean"},
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":                   map[string]any{"type": "string"},
						"type_line":              map[string]any{"type": "string"},
						"category_raw":           map[string]any{"type": "string"},
						"subtype_raw":            map[string]any{"type": "string"},
						"rarity_raw":             map[string]any{"type": "string"},
						"attunement_raw":         map[string]any{"type": "string"},
						"worn_or_held_raw":       map[string]any{"type": "string"},
						"description":            map[string]any{"type": "string"},
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
					"required": []string{"name", "type_line", "category_raw", "subtype_raw", "rarity_raw",
						"attunement_raw", "worn_or_held_raw", "description", "effect_kinds_raw",
						"creatures_mentioned", "environments_mentioned", "spells_mentioned",
						"limitations_raw", "charges_text", "cursed_text", "variant_table_text", "oddities"},
				},
			},
		},
		Required: []string{"printed_page_number", "leading_continuation_text", "last_item_continues", "items"},
	},
}

// cacheEntry is what we persist per page: the extract plus enough metadata to
// audit it later (which model, what input hash, token cost).
type cacheEntry struct {
	PdfPage      int         `json:"pdf_page"`
	Model        string      `json:"model"`
	InputHash    string      `json:"input_hash"`
	StopReason   string      `json:"stop_reason"`
	InputTokens  int64       `json:"input_tokens"`
	OutputTokens int64       `json:"output_tokens"`
	Extract      PageExtract `json:"extract"`
}

// Extractor calls the vision model once per page, caching every response on
// disk. The cache key hashes (backend model, prompt, schema, image bytes), so
// a change to any input transparently invalidates only what it affects, while
// re-running with nothing changed costs zero API calls. That property is what
// makes iterating on the stitcher/tally free.
//
// Two backends produce identical PageExtract records:
//
//	"api" — the Anthropic Messages API with forced tool use (needs a funded
//	        ANTHROPIC_API_KEY; the reproducible path a reviewer would run)
//	"cli" — the local `claude` CLI in headless -p mode, which bills the
//	        machine's logged-in Claude subscription instead of API credits
type Extractor struct {
	client   anthropic.Client
	cacheDir string
	backend  string
}

func NewExtractor(cacheDir, backend string) (*Extractor, error) {
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
	client := anthropic.NewClient(option.WithMaxRetries(4))
	return &Extractor{client: client, cacheDir: cacheDir, backend: backend}, nil
}

// ExtractPage returns the structured transcription of one page, from cache if
// present, calling the API otherwise. fromCache reports which happened.
func (e *Extractor) ExtractPage(ctx context.Context, pdfPage int, imagePath string, refresh bool) (PageExtract, bool, error) {
	img, err := os.ReadFile(imagePath)
	if err != nil {
		return PageExtract{}, false, err
	}

	// The cli backend uses whatever model the local claude login defaults to,
	// so its cache identity is the backend name; the model that actually ran
	// is recorded in the cache entry for auditing.
	modelID := string(extractionModel)
	if e.backend == "cli" {
		modelID = "claude-cli"
	}
	schemaJSON, _ := json.Marshal(recordPageTool.InputSchema.Properties)
	h := sha256.New()
	h.Write([]byte(modelID))
	h.Write([]byte(pagePrompt))
	h.Write(schemaJSON)
	h.Write(img)
	hash := hex.EncodeToString(h.Sum(nil))[:12]
	cachePath := filepath.Join(e.cacheDir, fmt.Sprintf("page-%02d.%s.json", pdfPage, hash))

	if !refresh {
		if raw, err := os.ReadFile(cachePath); err == nil {
			var entry cacheEntry
			if err := json.Unmarshal(raw, &entry); err == nil {
				entry.Extract.PdfPage = pdfPage
				return entry.Extract, true, nil
			}
		}
	}

	var res callResult
	if e.backend == "cli" {
		res, err = e.callCLI(ctx, imagePath)
	} else {
		res, err = e.callAPI(ctx, img)
	}
	if err != nil {
		return PageExtract{}, false, fmt.Errorf("page %d: %w", pdfPage, err)
	}
	res.extract.PdfPage = pdfPage

	entry := cacheEntry{
		PdfPage: pdfPage, Model: res.model, InputHash: hash,
		StopReason:  res.stopReason,
		InputTokens: res.inputTokens, OutputTokens: res.outputTokens,
		Extract: res.extract,
	}
	raw, _ := json.MarshalIndent(entry, "", "  ")
	if err := os.WriteFile(cachePath, raw, 0o644); err != nil {
		return PageExtract{}, false, err
	}
	return res.extract, false, nil
}

// callResult is one backend call's extract plus the audit metadata that goes
// into the cache entry.
type callResult struct {
	extract      PageExtract
	model        string
	stopReason   string
	inputTokens  int64
	outputTokens int64
}

func (e *Extractor) callAPI(ctx context.Context, img []byte) (callResult, error) {
	msg, err := e.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     extractionModel,
		MaxTokens: 16384,
		// Forced tool use pins the output shape; temperature 0 keeps repeat
		// runs as reproducible as the API allows.
		Temperature: anthropic.Float(0),
		ToolChoice:  anthropic.ToolChoiceParamOfTool(recordPageTool.Name),
		Tools:       []anthropic.ToolUnionParam{{OfTool: &recordPageTool}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(
				anthropic.NewImageBlockBase64("image/jpeg", base64.StdEncoding.EncodeToString(img)),
				anthropic.NewTextBlock(pagePrompt),
			),
		},
	})
	if err != nil {
		return callResult{}, err
	}

	var extract PageExtract
	found := false
	for _, block := range msg.Content {
		if block.Type == "tool_use" && block.Name == recordPageTool.Name {
			if err := json.Unmarshal(block.Input, &extract); err != nil {
				return callResult{}, fmt.Errorf("bad tool input: %w", err)
			}
			found = true
		}
	}
	if !found {
		// A refusal or truncation lands here; the caller quarantines the page
		// rather than aborting the whole run.
		return callResult{}, fmt.Errorf("no tool_use block (stop_reason=%s)", msg.StopReason)
	}
	return callResult{
		extract: extract, model: string(extractionModel),
		stopReason:  string(msg.StopReason),
		inputTokens: msg.Usage.InputTokens, outputTokens: msg.Usage.OutputTokens,
	}, nil
}

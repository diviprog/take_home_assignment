package extraction

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

	"oddities/internal/discovery"
)

const normalizeModel = anthropic.ModelClaudeOpus5

// normalizePrompt carries the frozen vocabulary's semantics and the
// ownership/fold rules summarized in notes/decisions.md. The closed member
// lists themselves live in the tool schema as JSON enums — on the api backend
// the model physically cannot answer off-vocabulary; on the cli backend the
// same schema travels in the prompt and Validate re-checks everything.
const normalizePrompt = `You are normalizing one magic-item catalog entry into a frozen ontology vocabulary. The item's verbatim survey record follows this briefing. Fill every field of the record_normalized_item tool; use "" / [] / 0 for absent values.

FIELD RULES

1. category: from the type line. The source's "Wonderous item" misspelling folds to wondrous_item. is_consumable: true for single-use items (all potions; anything the text says is destroyed/consumed by use).

2. rarity: the type line's grade (very rare -> very_rare). If the type line says rarity "varies", leave rarity "" and give each variant its grade in variants (the Pouch of False Coins states CP=uncommon, SP=rare, GP=very_rare, PP=legendary). Leave "" only for varies/unstated — never guess a grade.

3. variants: only when the source presents keyed variants (coin types, gemstone colors). variant_key and detail are source-verbatim; rarity per variant only when the source grades them separately.

4. requires_attunement: true iff the type line (or body) requires attunement. attuners: ONLY when the source restricts who may attune ("by a bard, cleric, ..." -> one entry per listed kind, lowercase singular; kind: class for character classes, ancestry for peoples like elf, other for anything else such as an alignment requirement). Unrestricted attunement = empty list.

5. worn_slot + slot_provenance: where the item is worn or wielded.
   - stated: the text names it ("while wearing this helm" -> head). The head slot merges hat/helmet/mask/crown/headband/circlet — one slot, per the client's own rule.
   - inferred: the text is silent but the category implies it: weapon -> main_hand (a shield -> off_hand), armor -> torso, ring -> finger.
   - none (stated or inferred): carried/used without occupying a slot — a potion, a scroll "in your possession", a pipe at the lips.
   - "" both fields only if you genuinely cannot place it.
   hands_required: only for main_hand/off_hand items whose base item implies it (greatsword 2, longsword 1, shield 1); 0 when unknown or not wielded.

6. applicability, from the type-line parenthetical ONLY:
   - no parenthetical -> "" and no base-item fields.
   - a named base item ("(dagger)", "(plate)") -> specific; base_item_name lowercase canonical, plus base_item_weapon_family (sword/axe/hammer/pick/shield — a dagger is NOT a sword) or base_item_armor_weight (light/medium/heavy) when the base item has one.
   - a quantified class ("(any sword)" -> family_or_class + applies_weapon_family; "(any medium or heavy armor)" -> family_or_class + applies_min_armor_weight=medium) — exactly one quantifier.
   - bare "(any)" -> any_in_category.

7. effects: one entry per effect family the item's mechanics touch — compound items get several rows. source_label: the survey label(s) or a short phrase the tag folds from. Tag hazard for non-curse drawbacks (rupture mechanics, vulnerabilities, costs of use).

8. creature_targets: one entry per (creature, relationship) the MECHANICS reference. role: harms (bonus damage/AC against), controls (commands), summons (conjures), protects_from (defense against). species: the specific lowercase noun when named ("bronze dragon"); family carries the closed facet. min_cr for stated CR thresholds. Age-scaled tables (Wyrmling/Young/Adult/Ancient) are ONE row with the table summarized in qualifier — never one row per age. Flavor-text mentions are not targets.

9. limitations: one entry per usage-limit clause; detail is the clause VERBATIM. Kinds: uses_per_period ("3 times per day"), per_rest, cooldown ("not again until..."), charges (all charge mechanics, including recharge), duration ("for 1 hour"), depletion (permanent expiry/crumble after N uses), exclusivity (can't combine with X), activation_condition (works only when/where — this owns environment conditions like "fully immersed in water" or "in a wooded environment"; water/underwater phrasings are the same condition), proximity (range limits on an ongoing bond), resistible (saving throw negates), target_restriction (creature-free scoping like "one size smaller than you"), termination (effect ends when...), one_time (single use ever).
   OWNERSHIP — do NOT create limitation rows for: the plain wear/wield condition (that is worn_slot's), attunement requirements (requires_attunement's), or creature-identity scoping (creature_targets' min_cr/qualifier).

10. spells: spells the item casts, stores, or mimics; lowercase canonical names; the three "locate ..." spells are distinct spells, never folded. school: the spell's school of magic if you know it confidently, else "".

11. is_cursed: ONLY when the source marks the item cursed. curse_or_drawback_text: verbatim curse or detrimental-property wording (artifact drawbacks land here even when is_cursed stays false).

12. unplaced: any mechanically meaningful fact this vocabulary cannot hold. Empty when everything found a home.

Base every answer on the record below; quote the source's own words in verbatim fields.`

// normalizeTool constrains the model's answer to the frozen vocabulary: every
// closed field is a JSON-schema enum built from the same generated constants
// Validate checks against, so prompt, schema, and database cannot disagree.
var normalizeTool = anthropic.ToolParam{
	Name:        "record_normalized_item",
	Description: anthropic.String("Record one catalog item normalized into the frozen ontology vocabulary."),
	InputSchema: anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"category":                 map[string]any{"type": "string", "enum": keys(categories)},
			"rarity":                   map[string]any{"type": "string", "enum": withEmpty(rarities)},
			"is_consumable":            map[string]any{"type": "boolean"},
			"requires_attunement":      map[string]any{"type": "boolean"},
			"attuners":                 arrayOf(map[string]any{"name": map[string]any{"type": "string"}, "kind": map[string]any{"type": "string", "enum": keys(attunerKinds)}}, "name", "kind"),
			"worn_slot":                map[string]any{"type": "string", "enum": withEmpty(wornSlots)},
			"slot_provenance":          map[string]any{"type": "string", "enum": withEmpty(provenances)},
			"hands_required":           map[string]any{"type": "integer"},
			"applicability":            map[string]any{"type": "string", "enum": withEmpty(applicability)},
			"base_item_name":           map[string]any{"type": "string"},
			"base_item_weapon_family":  map[string]any{"type": "string", "enum": withEmpty(weaponFamilies)},
			"base_item_armor_weight":   map[string]any{"type": "string", "enum": withEmpty(armorWeights)},
			"applies_weapon_family":    map[string]any{"type": "string", "enum": withEmpty(weaponFamilies)},
			"applies_min_armor_weight": map[string]any{"type": "string", "enum": withEmpty(armorWeights)},
			"is_cursed":                map[string]any{"type": "boolean"},
			"curse_or_drawback_text":   map[string]any{"type": "string"},
			"variants":                 arrayOf(map[string]any{"variant_key": map[string]any{"type": "string"}, "detail": map[string]any{"type": "string"}, "rarity": map[string]any{"type": "string", "enum": withEmpty(rarities)}}, "variant_key", "detail", "rarity"),
			"effects":                  arrayOf(map[string]any{"category": map[string]any{"type": "string", "enum": keys(effects)}, "source_label": map[string]any{"type": "string"}}, "category", "source_label"),
			"creature_targets":         arrayOf(map[string]any{"family": map[string]any{"type": "string", "enum": keys(families)}, "role": map[string]any{"type": "string", "enum": keys(targetRoles)}, "species": map[string]any{"type": "string"}, "min_cr": map[string]any{"type": "integer"}, "qualifier": map[string]any{"type": "string"}}, "family", "role", "species", "min_cr", "qualifier"),
			"limitations":              arrayOf(map[string]any{"kind": map[string]any{"type": "string", "enum": keys(limitations)}, "detail": map[string]any{"type": "string"}}, "kind", "detail"),
			"spells":                   arrayOf(map[string]any{"name": map[string]any{"type": "string"}, "school": map[string]any{"type": "string", "enum": withEmpty(spellSchools)}}, "name", "school"),
			"unplaced":                 map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		Required: []string{"category", "rarity", "is_consumable", "requires_attunement", "attuners",
			"worn_slot", "slot_provenance", "hands_required",
			"applicability", "base_item_name", "base_item_weapon_family", "base_item_armor_weight",
			"applies_weapon_family", "applies_min_armor_weight",
			"is_cursed", "curse_or_drawback_text",
			"variants", "effects", "creature_targets", "limitations", "spells", "unplaced"},
	},
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Deterministic order keeps the cache key stable across runs.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func withEmpty(m map[string]bool) []string { return append([]string{""}, keys(m)...) }

func arrayOf(props map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "object", "properties": props, "required": required},
	}
}

// itemInput renders the survey record the model normalizes from: the verbatim
// type line and prose plus the (relabeled) survey lists, which anchor the
// effect/creature/spell fields to what Pass A already found.
func itemInput(it discovery.StitchedItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Item: %s\nType line: %s\n", it.Name, it.TypeLine)
	if it.WornOrHeldRaw != "" {
		fmt.Fprintf(&b, "Worn/held phrase: %s\n", it.WornOrHeldRaw)
	}
	list := func(name string, vals []string) {
		if len(vals) > 0 {
			fmt.Fprintf(&b, "%s: %s\n", name, strings.Join(vals, " | "))
		}
	}
	list("Survey effect labels", it.EffectKindsRaw)
	list("Survey creatures", it.CreaturesMentioned)
	list("Survey environments", it.EnvironmentsMentioned)
	list("Survey spells", it.SpellsMentioned)
	list("Survey limitations", it.LimitationsRaw)
	if it.ChargesText != "" {
		fmt.Fprintf(&b, "Charges text: %s\n", it.ChargesText)
	}
	if it.CursedText != "" {
		fmt.Fprintf(&b, "Curse text: %s\n", it.CursedText)
	}
	if it.VariantTableText != "" {
		fmt.Fprintf(&b, "Variant table: %s\n", it.VariantTableText)
	}
	fmt.Fprintf(&b, "\nFull body text:\n%s", it.Description)
	return b.String()
}

// Normalizer runs the strict pass with the same dual-backend + disk-cache
// discipline as Pass A's three callers (the pattern is duplicated on purpose
// — each pass owns its prompt, schema, and cache identity end to end).
type Normalizer struct {
	client   anthropic.Client
	cacheDir string
	backend  string
}

func NewNormalizer(cacheDir, backend string) (*Normalizer, error) {
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
	return &Normalizer{client: anthropic.NewClient(option.WithMaxRetries(4)), cacheDir: cacheDir, backend: backend}, nil
}

type normalizeCacheEntry struct {
	Name         string         `json:"name"`
	Model        string         `json:"model"`
	InputHash    string         `json:"input_hash"`
	StopReason   string         `json:"stop_reason"`
	InputTokens  int64          `json:"input_tokens"`
	OutputTokens int64          `json:"output_tokens"`
	Normalized   NormalizedItem `json:"normalized"`
}

// Normalize returns one item's frozen-vocabulary record, cached on
// (model, prompt, schema, item text) like every other model pass.
func (n *Normalizer) Normalize(ctx context.Context, it discovery.StitchedItem, refresh bool) (NormalizedItem, bool, error) {
	input := itemInput(it)

	modelID := string(normalizeModel)
	if n.backend == "cli" {
		modelID = "claude-cli"
	}
	schemaJSON, _ := json.Marshal(normalizeTool.InputSchema.Properties)
	h := sha256.New()
	h.Write([]byte(modelID))
	h.Write([]byte(normalizePrompt))
	h.Write(schemaJSON)
	h.Write([]byte(input))
	hash := hex.EncodeToString(h.Sum(nil))[:12]
	cachePath := filepath.Join(n.cacheDir, fmt.Sprintf("%s.%s.json", slugify(it.Name), hash))

	if !refresh {
		if raw, err := os.ReadFile(cachePath); err == nil {
			var entry normalizeCacheEntry
			if err := json.Unmarshal(raw, &entry); err == nil {
				return entry.Normalized, true, nil
			}
		}
	}

	var norm NormalizedItem
	var model, stopReason string
	var inTok, outTok int64
	var err error
	if n.backend == "cli" {
		norm, model, stopReason, inTok, outTok, err = n.normalizeCLI(ctx, input)
	} else {
		norm, model, stopReason, inTok, outTok, err = n.normalizeAPI(ctx, input)
	}
	if err != nil {
		return NormalizedItem{}, false, fmt.Errorf("normalize %q: %w", it.Name, err)
	}

	entry := normalizeCacheEntry{
		Name: it.Name, Model: model, InputHash: hash, StopReason: stopReason,
		InputTokens: inTok, OutputTokens: outTok, Normalized: norm,
	}
	raw, _ := json.MarshalIndent(entry, "", "  ")
	if err := os.WriteFile(cachePath, raw, 0o644); err != nil {
		return NormalizedItem{}, false, err
	}
	return norm, false, nil
}

func (n *Normalizer) normalizeAPI(ctx context.Context, input string) (NormalizedItem, string, string, int64, int64, error) {
	msg, err := n.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:       normalizeModel,
		MaxTokens:   8192,
		Temperature: anthropic.Float(0),
		ToolChoice:  anthropic.ToolChoiceParamOfTool(normalizeTool.Name),
		Tools:       []anthropic.ToolUnionParam{{OfTool: &normalizeTool}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(normalizePrompt + "\n\n" + input)),
		},
	})
	if err != nil {
		return NormalizedItem{}, "", "", 0, 0, err
	}
	for _, block := range msg.Content {
		if block.Type == "tool_use" && block.Name == normalizeTool.Name {
			var norm NormalizedItem
			if err := json.Unmarshal(block.Input, &norm); err != nil {
				return NormalizedItem{}, "", "", 0, 0, fmt.Errorf("bad tool input: %w", err)
			}
			return norm, string(normalizeModel), string(msg.StopReason),
				msg.Usage.InputTokens, msg.Usage.OutputTokens, nil
		}
	}
	return NormalizedItem{}, "", "", 0, 0, fmt.Errorf("no tool_use block (stop_reason=%s)", msg.StopReason)
}

func (n *Normalizer) normalizeCLI(ctx context.Context, input string) (NormalizedItem, string, string, int64, int64, error) {
	schemaJSON, _ := json.MarshalIndent(normalizeTool.InputSchema.Properties, "", "  ")
	full := fmt.Sprintf(`%s

%s

Reply with ONLY one JSON object — no markdown fences, no commentary before or after. Its shape (JSON Schema properties; enums are CLOSED — answer only with listed members; include every field, using "" / [] / 0 when a value is absent):

%s`, normalizePrompt, input, schemaJSON)

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "-p", full, "--output-format", "json")
	// As in Pass A: ANTHROPIC_API_KEY would override the CLI's subscription
	// login, billing an unfunded key. Strip it from the subprocess.
	cmd.Env = environWithout("ANTHROPIC_API_KEY")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return NormalizedItem{}, "", "", 0, 0, fmt.Errorf("claude CLI: %v: %s", err, firstLine(stderr.String()))
	}

	var wrapper cliResult
	if err := json.Unmarshal(stdout.Bytes(), &wrapper); err != nil {
		return NormalizedItem{}, "", "", 0, 0, fmt.Errorf("claude CLI: unparseable wrapper: %w", err)
	}
	if wrapper.IsError {
		return NormalizedItem{}, "", "", 0, 0, fmt.Errorf("claude CLI: %s", firstLine(wrapper.Result))
	}
	body := wrapper.Result
	if start, end := strings.Index(body, "{"), strings.LastIndex(body, "}"); start >= 0 && end > start {
		body = body[start : end+1]
	}
	var norm NormalizedItem
	if err := json.Unmarshal([]byte(body), &norm); err != nil {
		return NormalizedItem{}, "", "", 0, 0, fmt.Errorf("claude CLI: reply is not the expected JSON (%v); stop_reason=%s reply starts: %s",
			err, wrapper.StopReason, firstLine(wrapper.Result))
	}
	model := "claude-cli"
	for name := range wrapper.ModelUsage {
		model = "claude-cli/" + name
	}
	return norm, model, wrapper.StopReason, wrapper.Usage.InputTokens, wrapper.Usage.OutputTokens, nil
}

// cliResult mirrors internal/discovery's wrapper for `claude -p
// --output-format json`; duplicated here so each pass stays self-contained.
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

func slugify(name string) string {
	return strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			return c
		}
		if c >= 'A' && c <= 'Z' {
			return c + 32
		}
		return '-'
	}, name)
}

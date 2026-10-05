package builder

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/bot/templates/booking"
	"github.com/pooya79/Piko/internal/bot/templates/inquiry"
	"github.com/pooya79/Piko/internal/bot/templates/registration"
)

type candidateContextKey struct{}

// Tools only stage in memory. The terminal service transaction alone can save.
// Genkit can execute tools concurrently, so protect the staged candidate.
type draftCandidate struct {
	mu      sync.Mutex
	base    flow.Definition
	staged  *flow.Definition
	invalid bool
	name    string
	action  *bot.ActionProposal
}

type initialDraftInput struct {
	Name       string `json:"name" jsonschema:"description=Recognizable suggested Persian workspace name, 1 to 80 characters"`
	Definition string `json:"definition" jsonschema:"description=Complete validated supported Flow JSON"`
}

func (s *Service) initialDraftTools() []ai.ToolRef {
	prepare := genkit.DefineTool(s.genkit, "prepare_bot", "Validate and stage an initial Bot Flow and suggested workspace name. No Bot is created until successful final completion. Use only for the explicitly authorized build request.", func(ctx *ai.ToolContext, input initialDraftInput) (validationResult, error) {
		c, err := candidateFromContext(ctx)
		if err != nil {
			return validationResult{}, err
		}
		run, ok := ctx.Value(runContextKey{}).(admittedRun)
		if !ok || run.botID != 0 {
			return validationResult{}, ErrUnavailable
		}
		d, key := validateCandidate(input.Definition)
		name := strings.TrimSpace(input.Name)
		if bot.ValidateName(name) != nil {
			key = "bot.create.name.error"
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		c.staged, c.name, c.invalid = nil, "", key != ""
		if key == "" {
			c.staged, c.name = &d, name
		}
		return validationResult{Valid: key == "", Staged: key == "", Error: key}, nil
	})
	// Initial generation can read approved Templates and validate repairs, but
	// receives neither a shared-Draft read nor a replacement-Draft staging tool.
	return []ai.ToolRef{s.templateTool, s.validationTool, prepare}
}

type draftInput struct {
	Definition string `json:"definition" jsonschema:"description=Complete Flow JSON using approved message/menu Blocks and sequential Forms/Questions; no executable or unsupported fields"`
}

type validationResult struct {
	Valid  bool   `json:"valid"`
	Staged bool   `json:"staged"`
	Error  string `json:"error,omitempty"`
}

type draftSnapshot struct {
	Definition flow.Definition `json:"definition"`
	Revision   int64           `json:"revision"`
}

type draftTemplate struct {
	Name       string          `json:"name"`
	Definition flow.Definition `json:"definition"`
}

// Genkit executes tool requests concurrently. Require one per model response
// so preparation and validation have an unambiguous order, even if a provider
// ignores parallel_tool_calls=false. Repairs still use subsequent model calls.
func (s *Service) sequentialDraftTools(context.Context) (*ai.Hooks, error) {
	return &ai.Hooks{WrapTool: func(ctx context.Context, params *ai.ToolParams, next ai.ToolNext) (*ai.MultipartToolResponse, error) {
		run, ok := ctx.Value(runContextKey{}).(admittedRun)
		if ok {
			keys := map[string]string{"read_draft": "read.draft", "read_templates": "read.templates", "validate_draft": "validate.draft", "prepare_draft": "prepare.draft", "prepare_bot": "prepare.draft"}
			if key, ok := keys[params.Request.Name]; ok {
				_ = s.display(run.id, "", "builder.progress."+key)
			}
		}
		return next(ctx, params)
	}, WrapModel: func(ctx context.Context, params *ai.ModelParams, next ai.ModelNext) (*ai.ModelResponse, error) {
		response, err := next(ctx, params)
		if response != nil && len(response.ToolRequests()) > 1 {
			return nil, errors.New("builder requires sequential tool requests")
		}
		return response, err
	}}, nil
}

func candidateFromContext(ctx context.Context) (*draftCandidate, error) {
	c, ok := ctx.Value(candidateContextKey{}).(*draftCandidate)
	if !ok || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	return c, nil
}

func validateCandidate(data string) (flow.Definition, string) {
	d, err := flow.Decode(data)
	if err != nil {
		var invalid *flow.Invalid
		if errors.As(err, &invalid) {
			return d, invalid.Key
		}
		return d, "draft.error.definition"
	}
	return d, ""
}

func (s *Service) draftTools() []ai.ToolRef {
	templates := genkit.DefineTool(s.genkit, "read_templates", "Read approved Inquiry, Registration and Booking request starter Flows. Customize with supported Questions; Registration does not guarantee acceptance/capacity and Booking request does not promise a reservation. This tool does not stage or save.", func(ctx *ai.ToolContext, _ struct{}) ([]draftTemplate, error) {
		if _, err := candidateFromContext(ctx); err != nil {
			return nil, err
		}
		return []draftTemplate{
			{Name: "inquiry", Definition: inquiry.Default().Definition()},
			{Name: "registration", Definition: registration.Default().Definition()},
			{Name: "booking", Definition: booking.Default().Definition()},
		}, nil
	})
	read := genkit.DefineTool(s.genkit, "read_draft", "Read this run's owner-authorized shared Draft snapshot and original revision. Manual edits may advance it; stale results will be rejected.", func(ctx *ai.ToolContext, _ struct{}) (draftSnapshot, error) {
		var out draftSnapshot
		c, err := candidateFromContext(ctx)
		if err != nil {
			return out, err
		}
		run, ok := ctx.Value(runContextKey{}).(admittedRun)
		if !ok {
			return out, ErrUnavailable
		}
		out.Definition, out.Revision = c.base, run.revision
		return out, nil
	})
	validate := genkit.DefineTool(s.genkit, "validate_draft", "Validate complete candidate Flow JSON using Piko's approved schema and limits. Forms support short_text, long_text, phone, number, single_choice and Jalali date Questions. This tool does not stage or save.", func(ctx *ai.ToolContext, input draftInput) (validationResult, error) {
		c, err := candidateFromContext(ctx)
		if err != nil {
			return validationResult{}, err
		}
		_, key := validateCandidate(input.Definition)
		if key != "" {
			// A failed repair cannot fall back to a previously staged proposal.
			// A later successful prepare must supply the entire repaired Flow.
			c.mu.Lock()
			c.staged, c.invalid = nil, true
			c.mu.Unlock()
		}
		return validationResult{Valid: key == "", Error: key}, nil
	})
	prepare := genkit.DefineTool(s.genkit, "prepare_draft", "Validate and stage a complete Flow replacement. May add, edit, remove or reorder approved message/menu destinations, Forms and Questions. Call once per model response. Nothing is saved yet; only a successful final reply permits one atomic revision-guarded save.", func(ctx *ai.ToolContext, input draftInput) (validationResult, error) {
		c, err := candidateFromContext(ctx)
		if err != nil {
			return validationResult{}, err
		}
		d, key := validateCandidate(input.Definition)
		c.mu.Lock()
		defer c.mu.Unlock()
		c.staged, c.invalid = nil, key != ""
		if key == "" {
			c.staged = &d
		}
		return validationResult{Valid: key == "", Staged: key == "", Error: key}, nil
	})
	s.templateTool, s.validationTool = templates, validate
	return []ai.ToolRef{read, templates, validate, prepare, s.actionTool()}
}

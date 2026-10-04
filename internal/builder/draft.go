package builder

import (
	"context"
	"errors"
	"reflect"
	"sync"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
	"github.com/pooya79/Piko/internal/bot/flow"
)

type candidateContextKey struct{}

// Tools only stage in memory. The terminal service transaction alone can save.
// Genkit can execute tools concurrently, so protect the staged candidate.
type draftCandidate struct {
	mu      sync.Mutex
	base    flow.Definition
	staged  *flow.Definition
	invalid bool
}

type draftInput struct {
	Definition string `json:"definition" jsonschema:"description=Complete Flow JSON including unchanged existing Forms; no executable or unsupported fields"`
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

// Genkit executes tool requests concurrently. Require one per model response
// so preparation and validation have an unambiguous order, even if a provider
// ignores parallel_tool_calls=false. Repairs still use subsequent model calls.
func sequentialDraftTools(context.Context) (*ai.Hooks, error) {
	return &ai.Hooks{WrapModel: func(ctx context.Context, params *ai.ModelParams, next ai.ModelNext) (*ai.ModelResponse, error) {
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

func validateCandidate(base flow.Definition, data string) (flow.Definition, string) {
	d, err := flow.Decode(data)
	if err != nil {
		var invalid *flow.Invalid
		if errors.As(err, &invalid) {
			return d, invalid.Key
		}
		return d, "draft.error.definition"
	}
	// This slice edits message/menu Blocks only. Full immutable Forms must survive,
	// including their references (enforced by the shared Flow validator).
	if len(d.Forms) != len(base.Forms) || (len(d.Forms) != 0 && !reflect.DeepEqual(d.Forms, base.Forms)) {
		return d, "builder.error.forms.preserve"
	}
	return d, ""
}

func (s *Service) draftTools() []ai.ToolRef {
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
	validate := genkit.DefineTool(s.genkit, "validate_draft", "Validate complete candidate Flow JSON using Piko's approved schema and limits. Preserve existing Forms. This tool does not stage or save.", func(ctx *ai.ToolContext, input draftInput) (validationResult, error) {
		c, err := candidateFromContext(ctx)
		if err != nil {
			return validationResult{}, err
		}
		_, key := validateCandidate(c.base, input.Definition)
		return validationResult{Valid: key == "", Error: key}, nil
	})
	prepare := genkit.DefineTool(s.genkit, "prepare_draft", "Validate and stage a complete message/menu Flow replacement, preserving existing Forms. May add, edit, remove message destinations. Call once per model response. Nothing is saved yet; only a successful final reply permits one atomic revision-guarded save.", func(ctx *ai.ToolContext, input draftInput) (validationResult, error) {
		c, err := candidateFromContext(ctx)
		if err != nil {
			return validationResult{}, err
		}
		d, key := validateCandidate(c.base, input.Definition)
		c.mu.Lock()
		defer c.mu.Unlock()
		c.staged, c.invalid = nil, key != ""
		if key == "" {
			c.staged = &d
		}
		return validationResult{Valid: key == "", Staged: key == "", Error: key}, nil
	})
	return []ai.ToolRef{read, validate, prepare}
}

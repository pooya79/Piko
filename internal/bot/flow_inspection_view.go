package bot

import (
	"strings"

	"github.com/pooya79/Piko/internal/bot/flow"
)

func inspectionBlocks(c flow.Canvas) []flow.CanvasBlock {
	blocks := append([]flow.CanvasBlock{}, c.Start...)
	seen := make(map[string]bool)
	for _, b := range blocks {
		seen[b.Key] = true
	}
	for _, branch := range c.Branches {
		for _, b := range branch.Blocks {
			if !seen[b.Key] {
				blocks = append(blocks, b)
				seen[b.Key] = true
			}
		}
	}
	return blocks
}

func inspectionTitle(n flow.CanvasBlock) string {
	if n.Question != nil {
		return n.Question.Label
	}
	text := []rune(strings.Join(strings.Fields(n.Text), " "))
	if len(text) > 44 {
		return string(text[:44]) + "…"
	}
	return string(text)
}

func inspectionIcon(n flow.CanvasBlock) string {
	if n.Question != nil {
		switch n.Question.Type {
		case "phone":
			return "phone"
		case "date":
			return "calendar-blank"
		case "number":
			return "hash"
		case "single_choice":
			return "list-bullets"
		default:
			return "text-t"
		}
	}
	switch n.Kind {
	case "welcome":
		return "robot"
	case "menu":
		return "squares-four"
	case "review":
		return "tray"
	case "ack":
		return "check-circle"
	default:
		return "chat-circle-dots"
	}
}

// Main edges describe the forward interaction; loops and edit paths are overlays.
func inspectionPrimary(e flow.CanvasEdge) bool {
	return e.LabelKey == "" || e.LabelKey == "flow.next" || e.LabelKey == "flow.answer" || e.LabelKey == "flow.confirm"
}

func inspectionMode(view FlowInspection) string {
	if view.Published {
		return "published"
	}
	return "draft"
}

package flow

import (
	"encoding/base64"
	"encoding/json"
)

// Canvas is a read-only projection of approved runtime paths, never an editor.
type Canvas struct {
	Start    []CanvasBlock
	Branches []CanvasBranch
}

type CanvasBranch struct {
	Label  string
	Blocks []CanvasBlock
}

type CanvasBlock struct {
	Key, Kind, ID, FormID, Text string
	Question                    *Question
	Edges                       []CanvasEdge
	Context                     string
}

type CanvasEdge struct {
	Target, Label, LabelKey string
}

func canvasKey(kind string, ids ...string) string {
	key := kind
	for _, id := range ids {
		key += ":" + base64.RawURLEncoding.EncodeToString([]byte(id))
	}
	return key
}

func canvasBlock(kind, id, formID, text string, value any) CanvasBlock {
	data, _ := json.Marshal(value)
	key := canvasKey(kind, id)
	if formID != "" {
		key = canvasKey(kind, formID, id)
	}
	return CanvasBlock{Key: key, Kind: kind, ID: id, FormID: formID, Text: text, Context: string(data)}
}

// ProjectCanvas mirrors Start/Choose/Advance: single menu destinations and
// sequential Questions, with only the runtime's back/skip/edit/cancel actions.
func ProjectCanvas(d Definition) Canvas {
	welcome := canvasBlock("block", d.Welcome.ID, "", d.Welcome.Text, d.Welcome)
	menu := canvasBlock("block", d.Menu.ID, "", d.Menu.Text, d.Menu)
	welcome.Kind, menu.Kind = "welcome", "menu"
	welcome.Edges = []CanvasEdge{{Target: menu.Key, LabelKey: "flow.next"}}
	graph := Canvas{}
	for _, choice := range d.Menu.Choices {
		branch := CanvasBranch{Label: choice.Label}
		if text, ok := d.Message(choice.Target); ok {
			var value Block
			for _, b := range d.Messages {
				if b.ID == choice.Target {
					value = b
				}
			}
			n := canvasBlock("block", choice.Target, "", text, value)
			n.Kind = "message"
			n.Edges = []CanvasEdge{{Target: menu.Key, LabelKey: "flow.return.menu"}}
			branch.Blocks = []CanvasBlock{n}
		} else if f, ok := d.Form(choice.Target); ok {
			review := canvasBlock("review", f.ID, "", f.Review, f.Review)
			ack := canvasBlock("ack", f.ID, "", f.Acknowledgement, f.Acknowledgement)
			for i, q := range f.Questions {
				n := canvasBlock("question", q.ID, f.ID, q.Prompt, q)
				n.Question = &q
				next := review.Key
				if i+1 < len(f.Questions) {
					next = canvasKey("question", f.ID, f.Questions[i+1].ID)
				}
				n.Edges = []CanvasEdge{{Target: next, LabelKey: "flow.answer"}}
				if !q.Required {
					n.Edges = append(n.Edges, CanvasEdge{Target: next, LabelKey: "flow.skip"})
				}
				if i > 0 {
					n.Edges = append(n.Edges, CanvasEdge{Target: canvasKey("question", f.ID, f.Questions[i-1].ID), LabelKey: "flow.back"})
				}
				n.Edges = append(n.Edges, CanvasEdge{Target: review.Key, LabelKey: "flow.edit.return"})
				if !q.Required {
					n.Edges = append(n.Edges, CanvasEdge{Target: review.Key, LabelKey: "flow.edit.skip"})
				}
				// Cancel ends the Interaction; Start again emits Welcome then Menu.
				n.Edges = append(n.Edges, CanvasEdge{Target: welcome.Key, LabelKey: "flow.cancel.restart"})
				branch.Blocks = append(branch.Blocks, n)
				review.Edges = append(review.Edges, CanvasEdge{Target: n.Key, Label: q.Label, LabelKey: "flow.edit"})
			}
			review.Edges = append(review.Edges, CanvasEdge{Target: ack.Key, LabelKey: "flow.confirm"}, CanvasEdge{Target: branch.Blocks[len(branch.Blocks)-1].Key, LabelKey: "flow.back"}, CanvasEdge{Target: welcome.Key, LabelKey: "flow.cancel.restart"})
			ack.Edges = []CanvasEdge{{Target: welcome.Key, LabelKey: "flow.restart"}}
			branch.Blocks = append(branch.Blocks, review, ack)
		}
		if len(branch.Blocks) > 0 {
			menu.Edges = append(menu.Edges, CanvasEdge{Target: branch.Blocks[0].Key, Label: choice.Label})
			graph.Branches = append(graph.Branches, branch)
		}
	}
	graph.Start = []CanvasBlock{welcome, menu}
	return graph
}

func (c Canvas) Block(key string) (CanvasBlock, bool) {
	for _, b := range c.Start {
		if b.Key == key {
			return b, true
		}
	}
	for _, branch := range c.Branches {
		for _, b := range branch.Blocks {
			if b.Key == key {
				return b, true
			}
		}
	}
	return CanvasBlock{}, false
}

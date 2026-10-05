package builder

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/locale"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

type DraftChange struct {
	Action, Kind, ID, FormID string
	Fields                   []ChangedField
}

type ChangedField struct {
	Name, Before, After string
}

func (field ChangedField) LocaleKey() string {
	return "changes.field." + strings.ReplaceAll(field.Name, "_", ".")
}

func (field ChangedField) Display(ctx context.Context, value string) string {
	if value == "" {
		return locale.T(ctx, "changes.unset")
	}
	if field.Name == "required" {
		return locale.T(ctx, "changes.value."+value)
	}
	return value
}

type draftItem struct {
	kind, id, formID string
	fields           map[string]json.RawMessage
}

func (item draftItem) key() string {
	// IDs are arbitrary owner text; JSON tuples avoid delimiter collisions.
	data, _ := json.Marshal([]string{item.kind, item.formID, item.id})
	return string(data)
}

func item(kind, id, formID string, value any) draftItem {
	data, _ := json.Marshal(value)
	fields := map[string]json.RawMessage{}
	_ = json.Unmarshal(data, &fields)
	for _, key := range []string{"id", "choices", "questions"} {
		delete(fields, key)
	}
	return draftItem{kind: kind, id: id, formID: formID, fields: fields}
}

func draftItems(d flow.Definition) []draftItem {
	if d.Version == 0 {
		return nil
	}
	items := []draftItem{item("flow", "", "", struct {
		Version int `json:"version"`
	}{d.Version}), item("welcome", d.Welcome.ID, "", d.Welcome), item("menu", d.Menu.ID, "", d.Menu)}
	for _, c := range d.Menu.Choices {
		items = append(items, item("choice", c.ID, "", c))
	}
	for _, m := range d.Messages {
		items = append(items, item("message", m.ID, "", m))
	}
	for _, f := range d.Forms {
		items = append(items, item("form", f.ID, "", f))
		for _, q := range f.Questions {
			items = append(items, item("question", q.ID, f.ID, q))
		}
	}
	return items
}

func fieldText(data json.RawMessage) string {
	var text string
	if json.Unmarshal(data, &text) == nil {
		return text
	}
	if len(data) == 0 {
		return ""
	}
	return string(data)
}

func changedFields(before, after map[string]json.RawMessage) []ChangedField {
	keys := make([]string, 0, len(before)+len(after))
	for key := range before {
		keys = append(keys, key)
	}
	for key := range after {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var fields []ChangedField
	for _, key := range slices.Compact(keys) {
		old, next := fieldText(before[key]), fieldText(after[key])
		if old != next {
			fields = append(fields, ChangedField{Name: key, Before: old, After: next})
		}
	}
	return fields
}

// Compare identities and settings independently of model prose and JSON field
// order. Questions are scoped to Forms, where their IDs are unique.
func draftChanges(before, after flow.Definition) []DraftChange {
	oldItems, nextItems := draftItems(before), draftItems(after)
	old, next := map[string]draftItem{}, map[string]draftItem{}
	for _, row := range oldItems {
		old[row.key()] = row
	}
	for _, row := range nextItems {
		next[row.key()] = row
	}
	var changes []DraftChange
	for _, row := range oldItems {
		if _, exists := next[row.key()]; !exists {
			changes = append(changes, DraftChange{Action: "removed", Kind: row.kind, ID: row.id, FormID: row.formID, Fields: changedFields(row.fields, nil)})
		}
	}
	for _, row := range nextItems {
		previous, exists := old[row.key()]
		action := "edited"
		if !exists {
			action = "added"
		}
		if fields := changedFields(previous.fields, row.fields); len(fields) > 0 {
			changes = append(changes, DraftChange{Action: action, Kind: row.kind, ID: row.id, FormID: row.formID, Fields: fields})
		}
	}
	// Additions/removals already have entries. Report ordering only when the
	// relative order of retained identities changed, including alongside edits.
	groups := []struct{ kind, formID string }{{"choice", ""}, {"message", ""}, {"form", ""}}
	for _, f := range after.Forms {
		groups = append(groups, struct{ kind, formID string }{"question", f.ID})
	}
	for _, group := range groups {
		var oldOrder, nextOrder []string
		for _, row := range oldItems {
			if row.kind == group.kind && row.formID == group.formID {
				if _, exists := next[row.key()]; exists {
					oldOrder = append(oldOrder, row.id)
				}
			}
		}
		for _, row := range nextItems {
			if row.kind == group.kind && row.formID == group.formID {
				if _, exists := old[row.key()]; exists {
					nextOrder = append(nextOrder, row.id)
				}
			}
		}
		if !slices.Equal(oldOrder, nextOrder) {
			changes = append(changes, DraftChange{Action: "reordered", Kind: group.kind, FormID: group.formID, Fields: []ChangedField{{Name: "order", Before: strings.Join(oldOrder, " → "), After: strings.Join(nextOrder, " → ")}}})
		}
	}
	return changes
}

// Only committed snapshots can describe saved changes. Undone runs retain the
// original diff as history, labelled undone, never as current work or an action.
func committedChanges(run dbgen.BuilderRun) ([]DraftChange, error) {
	if run.Status != string(RunSucceeded) || !run.AfterDefinition.Valid || !run.AfterRevision.Valid {
		return nil, nil
	}
	if run.Result != "saved" && run.Result != "created" && run.Result != "undone" {
		return nil, nil
	}
	var before flow.Definition
	var err error
	if run.BeforeDefinition.Valid {
		before, err = flow.Decode(run.BeforeDefinition.String)
		if err != nil {
			return nil, err
		}
	} else if run.Result != "created" {
		return nil, nil
	}
	after, err := flow.Decode(run.AfterDefinition.String)
	if err != nil {
		return nil, err
	}
	return draftChanges(before, after), nil
}

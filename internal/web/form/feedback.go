package form

import "strings"

// Feedback contains localized presentation copy. Handlers assign field
// errors only when validation identifies a field without revealing account state.
type Feedback struct {
	Message     string
	FieldErrors map[string]string
}

func (f Feedback) Invalid(field string) string {
	if f.FieldErrors[field] != "" {
		return "true"
	}
	return "false"
}

// Description preserves helper associations when a field also has an error.
func (f Feedback) Description(field, id, helper string) string {
	if f.FieldErrors[field] != "" {
		return strings.TrimSpace(helper + " " + id + "-error")
	}
	return helper
}

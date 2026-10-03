package auth

import (
	"net/http"

	forms "github.com/pooya79/Piko/internal/web/form"
)

// AccountForm retains only non-sensitive editable values. Passwords, receipts,
// and challenges never enter the retained-input presentation state.
type AccountForm struct {
	Email, Name string
	forms.Feedback
}

func accountForm(r *http.Request, message string) AccountForm {
	return AccountForm{Email: r.FormValue("email"), Name: r.FormValue("display_name"), Feedback: forms.Feedback{Message: message}}
}

package httpfixture

import (
	"strconv"
	"strings"
	"testing"
)

func ChangesPane(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, `<section id="studio-changes-panel"`)
	if start < 0 {
		t.Fatal("Changes pane missing")
	}
	end := strings.Index(body[start:], "</section>")
	if end < 0 {
		t.Fatal("Changes pane incomplete")
	}
	return body[start : start+end]
}

func ChangeRunHTML(t *testing.T, pane string, runID int) string {
	t.Helper()
	start := strings.Index(pane, `data-change-run="`+strconv.Itoa(runID)+`"`)
	if start < 0 {
		t.Fatal("Changes run missing")
	}
	end := strings.Index(pane[start:], "</article>")
	if end < 0 {
		t.Fatal("Changes run incomplete")
	}
	return pane[start : start+end]
}

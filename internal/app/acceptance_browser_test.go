package app

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/bot/templates/inquiry"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

// This opt-in fixture exercises real application HTTP and external wire
// adapters with synthetic replies. It grants no routes to the production app.
func TestAcceptanceBrowserFixture(t *testing.T) {
	addr := os.Getenv("PIKO_ACCEPTANCE_BROWSER_ADDR")
	if addr == "" {
		t.Skip("set PIKO_ACCEPTANCE_BROWSER_ADDR for integrated browser verification")
	}
	definition, err := json.Marshal(inquiry.Default().Definition())
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := generalDeployFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []struct {
				Role    string
				Content json.RawMessage
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input.Messages) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var latest, system string
		for _, message := range input.Messages {
			if message.Role == "user" {
				latest = string(message.Content)
			}
			if message.Role == "system" {
				system += string(message.Content)
			}
		}
		if strings.Contains(system, "Summarize older messages") {
			fixture.MemoryReply(w, "گفتگو دربارهٔ امکانات پیکو، ساخت ربات درخواست و پیشنهادهای نیازمند تأیید مالک است.")
			return
		}
		if input.Messages[len(input.Messages)-1].Role == "tool" {
			fixture.MemoryReply(w, "پیشنهاد آماده بررسی شماست.")
			return
		}
		if strings.Contains(latest, "ACCEPT_BUILD") {
			if strings.Contains(system, "general Piko chat without a Bot") {
				fixture.MemoryReply(w, `{"intent":"build"}`)
			} else {
				fixture.BuilderToolReply(w, "prepare_bot", map[string]string{"name": "پذیرش Example", "definition": string(definition)})
			}
			return
		}
		for _, action := range []string{"deploy", "pause", "resume"} {
			if strings.Contains(latest, "ACCEPT_"+strings.ToUpper(action)) {
				fixture.BuilderToolReply(w, "propose_action", map[string]string{"action": action})
				return
			}
		}
		fixture.MemoryReply(w, "پیکو برای جمع\u200cآوری درخواست با پرسش\u200cهای مشخص کمک می\u200cکند.")
	})
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("../../static"))))
	mux.Handle("/", a.server.Handler)
	server := &http.Server{Handler: mux}
	t.Cleanup(func() { _ = server.Close() })
	fmt.Printf("Acceptance browser fixture: http://%s\n", listener.Addr())
	if err := server.Serve(listener); err != http.ErrServerClosed {
		t.Fatal(err)
	}
}

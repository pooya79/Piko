package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/builder"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestStudioGeneralReplyStreamsBeforeCommittedHistory(t *testing.T) {
	for _, reply := range []string{"پاسخ موقت عمومی", `{"answer":"پاسخ موقت عمومی"`, `{name} پاسخ موقت عمومی`} {
		t.Run(reply, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			a, b := generalBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": reply}}}})
				fmt.Fprintf(w, "data: %s\n\n", frame)
				w.(http.Flusher).Flush()
				close(started)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\" کامل\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			})
			defer close(release)
			stop := startBuilderApp(t, a)
			defer stop()
			path := fixture.StartPikoChat(t, b)
			b.Post(path+"/messages", url.Values{"message": {"امکانات پیکو چیست؟"}})
			<-started
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+a.server.Addr+path+"/stream", nil)
			for _, cookie := range b.Jar.Cookies(b.Base) {
				req.AddCookie(cookie)
			}
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			scanner := bufio.NewScanner(response.Body)
			readBuilderEvent(t, scanner, "پاسخ موقت عمومی")
			if strings.Contains(fixture.StudioRequest(b, "GET", path, nil).Body.String(), "پاسخ موقت عمومی") {
				t.Fatal("provisional general text was saved before completion")
			}
			release <- struct{}{}
			readBuilderEvent(t, scanner, "succeeded")
			if !strings.Contains(fixture.StudioRequest(b, "GET", path, nil).Body.String(), "پاسخ موقت عمومی") {
				t.Fatal("committed general reply missing")
			}
		})
	}
}

package httpfixture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Existing scripted completions also exercise the real SDK streaming contract.
// Explicit SSE handlers pass through immediately for provider-barrier tests.
func StreamingBuilderProvider(provider http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := &BuilderProviderWriter{ResponseWriter: w}
		provider(out, r)
		if out.streaming {
			return
		}
		if out.code == 0 {
			out.code = 200
		}
		if out.code != 200 {
			w.WriteHeader(out.code)
			_, _ = w.Write(out.body.Bytes())
			return
		}
		var response map[string]json.RawMessage
		if json.Unmarshal(out.body.Bytes(), &response) != nil {
			w.WriteHeader(200)
			_, _ = w.Write(out.body.Bytes())
			return
		}
		var choices []map[string]json.RawMessage
		_ = json.Unmarshal(response["choices"], &choices)
		for i, choice := range choices {
			var message map[string]json.RawMessage
			_ = json.Unmarshal(choice["message"], &message)
			if tools, ok := message["tool_calls"]; ok {
				var calls []map[string]json.RawMessage
				_ = json.Unmarshal(tools, &calls)
				for j, call := range calls {
					call["index"] = json.RawMessage(fmt.Sprint(j))
				}
				message["tool_calls"], _ = json.Marshal(calls)
			}
			delta, _ := json.Marshal(message)
			delete(choice, "message")
			choice["delta"] = delta
			choice["index"] = json.RawMessage(fmt.Sprint(i))
		}
		response["choices"], _ = json.Marshal(choices)
		w.Header().Set("Content-Type", "text/event-stream")
		data, _ := json.Marshal(response)
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
	}
}

type BuilderProviderWriter struct {
	http.ResponseWriter
	body      bytes.Buffer
	code      int
	streaming bool
}

func (w *BuilderProviderWriter) WriteHeader(code int) {
	w.code = code
	w.streaming = strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream")
	if w.streaming {
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *BuilderProviderWriter) Write(p []byte) (int, error) {
	if w.code == 0 {
		w.WriteHeader(200)
	}
	if w.streaming {
		return w.ResponseWriter.Write(p)
	}
	return w.body.Write(p)
}

func (w *BuilderProviderWriter) Flush() {
	if w.code == 0 {
		w.WriteHeader(200)
	}
	w.ResponseWriter.(http.Flusher).Flush()
}

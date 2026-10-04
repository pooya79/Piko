package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pooya79/Piko/internal/locale"
)

// Display snapshots are bounded and disposable. Only finish persists model
// replies; a disconnected or slow viewer cannot block or cancel generation.
type liveRun struct {
	cancel         context.CancelFunc
	botID, chatID  int64
	mu             sync.Mutex
	text, progress string
}

type displaySnapshot struct {
	ID       int64     `json:"id"`
	Status   RunStatus `json:"status"`
	Text     string    `json:"text"`
	Progress string    `json:"progress"`
	Feedback string    `json:"feedback"`
}

func (s *Service) display(runID int64, text, progress string) error {
	s.mu.Lock()
	live := s.live[runID]
	s.mu.Unlock()
	if live == nil {
		return nil
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	if len(live.text)+len(text) > 128<<10 || !utf8.ValidString(text) {
		return ErrMessage
	}
	live.text += text
	if progress != "" {
		live.progress = progress
	}
	return nil
}

func (s *Service) snapshot(ctx context.Context, botID, chatID int64) (displaySnapshot, error) {
	run, err := s.Status(ctx, botID, chatID)
	if err != nil {
		return displaySnapshot{}, err
	}
	out := displaySnapshot{ID: run.ID, Status: run.Status}
	if run.ID != 0 {
		out.Feedback = locale.T(ctx, run.FeedbackKey())
	}
	if run.Status != RunRunning {
		return out, nil
	}
	s.mu.Lock()
	live := s.live[run.ID]
	s.mu.Unlock()
	if live != nil {
		live.mu.Lock()
		out.Text = live.text
		if live.progress != "" {
			out.Progress = locale.T(ctx, live.progress)
		}
		live.mu.Unlock()
	}
	return out, nil
}

// Stream is read-only. Reconnect sends a full current display snapshot instead
// of replaying deltas or admitting work; terminal history is restored on reload.
func (h *Handler) Stream(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	id, err := chatID(r)
	if err != nil {
		h.failed(w, r, err)
		return
	}
	snapshot, err := h.service.snapshot(r.Context(), b.ID, id)
	if err != nil {
		h.failed(w, r, err)
		return
	}
	controller := http.NewResponseController(w)
	// Renew a short write deadline for each frame, overriding the ordinary
	// server WriteTimeout without leaving slow clients unbounded.
	if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && err != http.ErrNotSupported {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	last := ""
	for {
		data, err := json.Marshal(snapshot)
		if err != nil {
			return
		}
		if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && err != http.ErrNotSupported {
			return
		}
		if string(data) != last {
			if _, err := fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", data); err != nil {
				return
			}
			last = string(data)
		} else if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
			return
		}
		if err := controller.Flush(); err != nil {
			return
		}
		if snapshot.Status != RunRunning {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
		snapshot, err = h.service.snapshot(r.Context(), b.ID, id)
		if err != nil {
			return
		}
	}
}

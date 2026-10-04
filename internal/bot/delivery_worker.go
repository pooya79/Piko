package bot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pooya79/Piko/internal/bot/flow"
	engine "github.com/pooya79/Piko/internal/bot/runtime"
	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

// RunDelivery owns two bounded workers. A persisted per-Bot lease serializes
// Participant transitions and sends across workers and server processes.
func (s *Service) RunDelivery(ctx context.Context) {
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { s.deliveryLoop(ctx) })
	}
	wg.Wait()
}

func (s *Service) deliveryLoop(ctx context.Context) {
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	for ctx.Err() == nil {
		claim, err := nonce()
		if err != nil {
			return
		}
		work, err := s.repo.q.ClaimDeliveryWork(ctx, dbgen.ClaimDeliveryWorkParams{WorkerNonce: claim, Mode: string(s.deliveryMode())})
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			continue
		}
		// Every operation is bounded below the lease duration, including SQLite waits.
		workCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
		retry, failed := s.processDelivery(workCtx, work)
		cancel()
		if ctx.Err() != nil {
			// Cancellation leaves the durable cursor untouched. Release immediately
			// so a clean restart need not wait for a synthetic transport backoff.
			retry, failed = 0, work.WorkerError
		}
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), deliverySaveTimeout)
		_, _ = s.repo.q.ReleaseDeliveryWork(releaseCtx, dbgen.ReleaseDeliveryWorkParams{BotID: work.BotID, WorkerNonce: claim, RetryAt: retry, WorkerError: failed})
		releaseCancel()
	}
}

func (s *Service) processDelivery(ctx context.Context, work dbgen.BotDelivery) (int64, int64) {
	row, err := s.repo.q.GetWorkerBotCredentials(ctx, dbgen.GetWorkerBotCredentialsParams{ID: work.BotID, WorkerNonce: work.WorkerNonce})
	if err != nil {
		return time.Now().Unix() + 5, 1
	}
	token, err := s.credentials.open(row.EncryptedToken, row.OwnerID, row.TelegramID)
	if err != nil {
		return time.Now().Unix() + 60, 1
	}
	update, err := s.repo.q.GetNextUpdate(ctx, work.BotID)
	if errors.Is(err, sql.ErrNoRows) && work.Mode == string(PollingDelivery) {
		err = s.pollDelivery(ctx, work, token)
		if err != nil {
			return time.Now().Unix() + 5, 1
		}
		return time.Now().Unix() + 1, 0
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0
	}
	if err != nil {
		return time.Now().Unix() + 5, 1
	}
	if !update.Output.Valid {
		update, err = s.stageDelivery(ctx, work, update)
		if err != nil {
			return time.Now().Unix() + 5, 1
		}
	}
	var actions []telegram.Action
	if json.Unmarshal([]byte(update.Output.String), &actions) != nil {
		return time.Now().Unix() + 60, 1
	}
	if len(actions) == 0 {
		err = s.repo.q.CompleteIgnoredUpdate(ctx, dbgen.CompleteIgnoredUpdateParams{ID: update.ID, BotID: work.BotID})
		if err != nil {
			return time.Now().Unix() + 5, 1
		}
		return 0, 0
	}
	for i := int(update.Cursor); i < len(actions); i++ {
		n, err := s.repo.q.RenewDeliveryWork(ctx, dbgen.RenewDeliveryWorkParams{BotID: work.BotID, WorkerNonce: work.WorkerNonce})
		if err != nil || n != 1 {
			return time.Now().Unix() + 5, 1
		}
		if err = s.telegram.Deliver(ctx, token, actions[i]); err != nil {
			if errors.Is(err, telegram.ErrForbidden) && actions[i].Message != nil {
				if err := s.repo.q.FailUndeliverableUpdate(ctx, dbgen.FailUndeliverableUpdateParams{ID: update.ID, BotID: work.BotID}); err != nil {
					return time.Now().Unix() + 5, 1
				}
				return 0, 1
			}
			delay := min(int64(60), int64(1)<<min(update.Attempts, 6))
			if err := s.repo.q.RecordUpdateFailure(ctx, dbgen.RecordUpdateFailureParams{ID: update.ID, BotID: work.BotID, RetryAt: time.Now().Unix() + delay}); err != nil {
				return time.Now().Unix() + 5, 1
			}
			return 0, 1
		}
		complete := int64(0)
		if i == len(actions)-1 {
			complete = 1
		}
		n, err = s.repo.q.AdvanceUpdateOutput(ctx, dbgen.AdvanceUpdateOutputParams{ID: update.ID, BotID: work.BotID, Cursor: int64(i), Complete: complete})
		// A crash after Telegram accepts but before this commit may repeat a send.
		if err != nil || n != 1 {
			return time.Now().Unix() + 5, 1
		}
	}
	return 0, 0
}

func (s *Service) pollDelivery(ctx context.Context, work dbgen.BotDelivery, token string) error {
	updates, err := s.telegram.Poll(ctx, token, work.PollingOffset)
	if err != nil {
		return err
	}
	offset := work.PollingOffset
	for _, u := range updates {
		if u.ID == nil || *u.ID < 0 {
			return errors.New("invalid update")
		}
		data, err := json.Marshal(u)
		if err != nil {
			return err
		}
		if err = s.accept(ctx, work.BotID, data, u); err != nil {
			return err
		}
		offset = max(offset, *u.ID+1)
	}
	// The next polling request confirms this offset only after durable acceptance.
	_, err = s.repo.q.AdvancePollingOffset(ctx, dbgen.AdvancePollingOffsetParams{BotID: work.BotID, WorkerNonce: work.WorkerNonce, PollingOffset: offset})
	return err
}

func (s *Service) stageDelivery(ctx context.Context, work dbgen.BotDelivery, u dbgen.BotUpdate) (dbgen.BotUpdate, error) {
	update, err := telegram.DecodeUpdate([]byte(u.Payload))
	if err != nil {
		return u, err
	}
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return u, err
	}
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)
	// Fence stale workers before making any state transition.
	if _, err = q.GetWorkerBotCredentials(ctx, dbgen.GetWorkerBotCredentialsParams{ID: work.BotID, WorkerNonce: work.WorkerNonce}); err != nil {
		return u, err
	}
	actions, err := s.transition(ctx, q, work.BotID, update, u.AcceptedWhilePaused != 0)
	if err != nil {
		return u, err
	}
	data, err := json.Marshal(actions)
	if err != nil {
		return u, err
	}
	n, err := q.StageUpdateOutput(ctx, dbgen.StageUpdateOutputParams{ID: u.ID, BotID: work.BotID, Output: sql.NullString{String: string(data), Valid: true}})
	if err != nil {
		return u, err
	}
	if n != 1 {
		return u, errors.New("update already staged")
	}
	if err = tx.Commit(); err != nil {
		return u, err
	}
	u.Output = sql.NullString{String: string(data), Valid: true}
	return u, nil
}

// transition runs inside the inbox staging transaction: progress, confirmation,
// and acknowledgement outputs either all commit or all roll back.
func (s *Service) transition(ctx context.Context, q *dbgen.Queries, botID int64, u telegram.Update, acceptedWhilePaused bool) ([]telegram.Action, error) {
	actions := []telegram.Action{}
	var participant, chatID int64
	var input engine.Input
	if c := u.Callback; c != nil {
		if c.ID != "" && len(c.ID) <= 256 {
			actions = append(actions, telegram.Action{CallbackID: c.ID, CallbackText: "این دکمه دیگر قابل استفاده نیست؛ از منوی تازه یا /start استفاده کنید."})
		} else {
			return actions, nil
		}
		if c.Message == nil || c.Message.Chat.Type != "private" || c.From.IsBot || c.From.ID <= 0 || c.From.ID != c.Message.Chat.ID {
			return actions, nil
		}
		participant, chatID = c.From.ID, c.Message.Chat.ID
	} else if m := u.Message; m != nil {
		if m.Chat.Type != "private" || m.From.IsBot || m.From.ID <= 0 || m.From.ID != m.Chat.ID {
			return actions, nil
		}
		participant, chatID = m.From.ID, m.Chat.ID
	} else {
		return actions, nil
	}
	// Use one instant for expiry and renewal within this immediate transaction.
	now := s.now().Unix()
	expiredState, err := q.DeleteExpiredParticipant(ctx, dbgen.DeleteExpiredParticipantParams{BotID: botID, ParticipantID: participant, Now: now})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	paused, pauseErr := q.GetBotPaused(ctx, botID)
	if pauseErr != nil {
		return nil, pauseErr
	}
	if paused != 0 || acceptedWhilePaused {
		const unavailable = "این ربات موقتاً در دسترس نیست؛ لطفاً بعداً دوباره تلاش کنید."
		if u.Callback != nil {
			actions[0].CallbackText = unavailable
		} else {
			actions = append(actions, telegram.Action{Message: &telegram.SendMessage{ChatID: chatID, Text: unavailable}})
		}
		// Expiry still runs above. Pause never renews inactivity or rotates the
		// step token, so unexpired questions and review buttons can continue.
		return actions, nil
	}
	var prior engine.State
	if err == nil {
		if err = json.Unmarshal([]byte(expiredState), &prior); err != nil {
			return nil, err
		}
	}
	expired := prior.Unfinished()
	if expired && u.Callback != nil {
		actions[0].CallbackText = "پیشرفت پس از ۲۴ ساعت بی\u200cفعالیتی منقضی شد؛ پاسخ\u200cهای ناتمام پاک شدند. برای شروع دوباره /start را بفرستید."
	}
	p, err := q.GetParticipant(ctx, dbgen.GetParticipantParams{BotID: botID, ParticipantID: participant, Now: now})
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var d flow.Definition
	var state engine.State
	var publication int64
	attempt := ""
	if exists {
		d, err = flow.Decode(p.Definition)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(p.Interaction), &state); err != nil {
			return nil, err
		}
		publication, attempt = p.PublicationID, p.AttemptID
	}
	var output engine.Output
	fresh := false
	if c := u.Callback; c != nil {
		if !exists || p.ChatID != chatID {
			return actions, nil
		}
		step, index, ok := strings.Cut(c.Data, ".")
		choice, e := strconv.Atoi(index)
		choices := engine.Current(d, state).Choices
		if !ok || e != nil || step != p.StepToken || choice < 0 || choice >= len(choices) {
			return actions, nil
		}
		input.Action = choices[choice].ID
		if input.Action == "again" {
			fresh = true
		} else {
			output, err = engine.Advance(d, state, input)
		}
		actions[0].CallbackText = ""
	} else {
		m := u.Message
		fields := strings.Fields(m.Text)
		start := len(fields) > 0 && (fields[0] == "/start" || strings.HasPrefix(fields[0], "/start@"))
		active := exists && state.Unfinished()
		if expired {
			fresh = true
		} else if start {
			if active {
				output = engine.OfferResume(d, state)
			} else {
				fresh = true
			}
		} else if active {
			input = engine.Input{Answer: true, Text: m.Text, Unsupported: m.Text == ""}
			if state.AwaitingResume || state.Phase != "question" {
				output = engine.Current(d, state)
			} else {
				output, err = engine.Advance(d, state, input)
			}
		} else {
			return actions, nil
		}
	}
	// A menu is not unfinished Form progress. If publication changed before a
	// Form was chosen, refresh it rather than interpreting an old route against
	// the new definition or starting a superseded Interaction.
	newForm := !state.Unfinished() && output.State.Unfinished()
	if fresh || newForm {
		latest, e := q.GetLatestPublication(ctx, botID)
		if errors.Is(e, sql.ErrNoRows) {
			return actions, nil
		}
		if e != nil {
			return nil, e
		}
		if fresh || latest.ID != publication {
			d, err = flow.Decode(latest.Definition)
			if err != nil {
				return nil, err
			}
			if newForm {
				actions[0].CallbackText = "منو به\u200cروز شده است؛ فرم را از منوی تازه انتخاب کنید."
			}
			publication, attempt = latest.ID, ""
			output, err = engine.Start(d)
			if expired {
				output.Messages = append([]string{"پیشرفت ناتمام پس از ۲۴ ساعت بی\u200cفعالیتی منقضی شد و پاسخ\u200cها پاک شدند؛ هیچ درخواستی ارسال نشد. از منوی تازه شروع کنید."}, output.Messages...)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if state.FormID == "" && output.State.FormID != "" {
		attempt, err = nonce()
		if err != nil {
			return nil, err
		}
	}
	if len(output.Confirmed) > 0 {
		if attempt == "" {
			return nil, errors.New("missing interaction attempt")
		}
		answers, e := json.Marshal(output.Confirmed)
		if e != nil {
			return nil, e
		}
		if err = q.CreateSubmission(ctx, dbgen.CreateSubmissionParams{BotID: botID, ParticipantID: participant, PublicationID: publication, AttemptID: attempt, Answers: string(answers)}); err != nil {
			return nil, err
		}
	}
	step, err := nonce()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(output.State)
	if err != nil {
		return nil, err
	}
	if err = q.SaveParticipant(ctx, dbgen.SaveParticipantParams{BotID: botID, ParticipantID: participant, ChatID: chatID, PublicationID: publication, StepToken: step, Interaction: string(data), AttemptID: attempt, ExpiresAt: now + 86400}); err != nil {
		return nil, err
	}
	messages := telegram.SplitMessages(output.Messages)
	for i, text := range messages {
		message := telegram.SendMessage{ChatID: chatID, Text: text}
		if i == len(messages)-1 && len(output.Choices) > 0 {
			message.Markup = &telegram.Markup{}
			for index, c := range output.Choices {
				message.Markup.Buttons = append(message.Markup.Buttons, []telegram.Button{{Text: c.Label, Data: step + "." + strconv.Itoa(index)}})
			}
		}
		actions = append(actions, telegram.Action{Message: &message})
	}
	return actions, nil
}

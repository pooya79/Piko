package worker

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"buildx/internal/auth"
	"buildx/internal/jobs"
	"buildx/internal/locale"
	"buildx/internal/mail"
	"buildx/internal/platform/database"
	"buildx/internal/platform/database/dbgen"
	"buildx/internal/platform/logging"
)

type Worker struct {
	client *river.Client[pgx.Tx]
	db     *pgxpool.Pool
	log    *slog.Logger
}

func New(ctx context.Context, cfg Config) (*Worker, error) {
	log := logging.New(cfg.LogLevel)
	catalog, err := locale.NewCatalog()
	if err != nil {
		return nil, err
	}
	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	sender, err := mail.NewSender(mail.Config{
		Address: cfg.SMTPAddress, From: cfg.SMTPFrom,
		Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, RequireTLS: cfg.SMTPRequireTLS,
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	mailer := auth.NewAccountService(auth.NewAccountRepository(db, nil), auth.NewService(dbgen.New(db)), []byte(cfg.SessionSecret), cfg.PublicBaseURL, catalog)
	client, err := jobs.NewClient(db, log, cfg.ShutdownPeriod, mailer, sender)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Worker{client: client, db: db, log: log}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	w.log.Info("worker starting", "queue", jobs.QueueMaintenance)
	if err := w.client.Start(ctx); err != nil {
		w.db.Close()
		return fmt.Errorf("start worker: %w", err)
	}
	<-w.client.Stopped()
	w.log.Info("worker stopped")
	w.db.Close()
	return nil
}

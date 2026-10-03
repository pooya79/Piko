package worker

import (
	"context"
	"log/slog"

	"database/sql"

	"buildx/internal/auth"
	"buildx/internal/jobs"
	"buildx/internal/locale"
	"buildx/internal/mail"
	"buildx/internal/platform/database"
	"buildx/internal/platform/database/dbgen"
	"buildx/internal/platform/logging"
)

type Worker struct {
	client *jobs.Client
	db     *sql.DB
	log    *slog.Logger
}

func New(ctx context.Context, cfg Config) (*Worker, error) {
	log := logging.New(cfg.LogLevel)
	catalog, err := locale.NewCatalog()
	if err != nil {
		return nil, err
	}
	db, err := database.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return nil, err
	}
	if err := database.RequireWAL(ctx, db); err != nil {
		_ = db.Close()
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
	client := jobs.NewClient(db, log, mailer, sender)
	return &Worker{client: client, db: db, log: log}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	defer func() { _ = w.db.Close() }()
	w.log.Info("worker starting")
	err := w.client.Run(ctx)
	w.log.Info("worker stopped")
	return err
}

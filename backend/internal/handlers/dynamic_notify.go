package handlers

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/notify"
)

// NewDynamicEmailNotifier resolves SMTP settings from the database for each
// notification and falls back to env config when the settings row is empty.
func NewDynamicEmailNotifier(db DBPool, fallback notify.EmailConfig, logger *zap.Logger) notify.Notifier {
	return &dynamicEmailNotifier{db: db, fallback: fallback, logger: logger}
}

type dynamicEmailNotifier struct {
	db       DBPool
	fallback notify.EmailConfig
	logger   *zap.Logger
}

func (d *dynamicEmailNotifier) Name() string { return "email" }

func (d *dynamicEmailNotifier) Notify(ctx context.Context, event notify.Event) error {
	cfg := d.config(ctx)
	if cfg.Host == "" || cfg.From == "" || len(cfg.To) == 0 {
		return nil
	}
	if cfg.Port == "" {
		cfg.Port = "587"
	}
	return notify.NewEmailNotifier(cfg).Notify(ctx, event)
}

func (d *dynamicEmailNotifier) config(ctx context.Context) notify.EmailConfig {
	cfg := d.fallback
	if d.db == nil {
		return cfg
	}

	var host, port, username, fromAddress string
	var passwordEnc *string
	err := d.db.QueryRow(ctx,
		`SELECT host, port, username, from_address, password_enc FROM smtp_config WHERE id = 1`,
	).Scan(&host, &port, &username, &fromAddress, &passwordEnc)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			d.logger.Warn("smtp config: load failed; using fallback config", zap.Error(err))
		}
		return cfg
	}
	if strings.TrimSpace(host) == "" {
		return cfg
	}

	cfg.Host = strings.TrimSpace(host)
	cfg.Port = strings.TrimSpace(port)
	cfg.Username = strings.TrimSpace(username)
	cfg.From = strings.TrimSpace(fromAddress)
	cfg.Password = ""
	if passwordEnc != nil && *passwordEnc != "" {
		password, decErr := decryptProvisioningToken(*passwordEnc)
		if decErr != nil {
			d.logger.Warn("smtp config: decrypt password failed", zap.Error(decErr))
		} else {
			cfg.Password = password
		}
	}
	return cfg
}

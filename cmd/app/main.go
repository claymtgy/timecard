// Command app serves the timesheet / project management API and web UI.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // time zones on hosts without a zoneinfo database (e.g. Windows)

	"github.com/claym/crm/internal/app"
	"github.com/claym/crm/internal/config"
	"github.com/claym/crm/internal/database"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	if err := config.LoadDotEnv(".env"); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.Connect(ctx, config.String("DATABASE_URL", "postgres://crm:crm@localhost:5432/crm?sslmode=disable"), "app")
	if err != nil {
		return err
	}
	defer pool.Close()
	migrations, err := fs.Sub(app.Migrations(), app.MigrationsDir)
	if err != nil {
		return err
	}
	if err := database.Migrate(ctx, pool, "app", migrations); err != nil {
		return err
	}

	appURL := config.String("APP_URL", "http://localhost:5173")
	webDist := config.String("APP_WEB_DIST", "web/dist")
	if _, err := os.Stat(webDist); err != nil {
		slog.Warn("web UI not built; serving API only (run `npm run build` in web/ or use the Vite dev server)", "dir", webDist)
		webDist = ""
	}
	loc, err := time.LoadLocation(config.String("APP_TIMEZONE", "America/Denver"))
	if err != nil {
		return fmt.Errorf("APP_TIMEZONE: %w", err)
	}
	srv := app.New(app.Config{
		AppURL:           appURL,
		SecureCookies:    config.Bool("APP_SECURE_COOKIES", strings.HasPrefix(appURL, "https://")),
		WebDist:          webDist,
		OIDCIssuer:       config.String("OIDC_ISSUER", "http://localhost:8081"),
		OIDCClientID:     config.String("OIDC_CLIENT_ID", "crm-web"),
		OIDCClientSecret: config.String("OIDC_CLIENT_SECRET", ""),
		OIDCInternalURL:  config.String("OIDC_INTERNAL_URL", ""),
		TimeZone:         loc,
		Mail: app.MailConfig{
			Host:     config.String("SMTP_HOST", ""),
			Port:     config.String("SMTP_PORT", "587"),
			Username: config.String("SMTP_USERNAME", ""),
			Password: config.String("SMTP_PASSWORD", ""),
			From:     config.String("SMTP_FROM", ""),
		},
	}, pool)
	go srv.Cleanup(ctx, time.Hour)

	httpSrv := &http.Server{
		Addr:              config.String("APP_ADDR", ":8080"),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		httpSrv.Shutdown(shutdownCtx)
	}()
	slog.Info("app listening", "addr", httpSrv.Addr, "public_url", appURL)
	if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

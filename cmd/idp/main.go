// Command idp runs the OpenID Connect provider and manages its user accounts.
//
//	idp                      start the server (same as "idp serve")
//	idp user add -email a@b.c -name "Ada Lovelace"   (password read from stdin)
//	idp user passwd -email a@b.c                    (password read from stdin)
//	idp user disable|enable -email a@b.c
//	idp user list
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/claym/crm/internal/config"
	"github.com/claym/crm/internal/database"
	"github.com/claym/crm/internal/idp"
)

func main() {
	if err := config.LoadDotEnv(".env"); err != nil {
		fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	args := os.Args[1:]
	if len(args) == 0 || args[0] == "serve" {
		fatal(serve(ctx))
		return
	}
	if args[0] == "user" && len(args) >= 2 {
		fatal(userCmd(ctx, args[1], args[2:]))
		return
	}
	fmt.Fprintln(os.Stderr, "usage: idp [serve] | idp user add|passwd|disable|enable|list [flags]")
	os.Exit(2)
}

func fatal(err error) {
	if err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func openDB(ctx context.Context) (*database.Pool, error) {
	pool, err := database.Connect(ctx, config.String("DATABASE_URL", "postgres://crm:crm@localhost:5432/crm?sslmode=disable"), "idp")
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(idp.Migrations(), idp.MigrationsDir)
	if err != nil {
		return nil, err
	}
	if err := database.Migrate(ctx, pool, "idp", sub); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func serve(ctx context.Context) error {
	pool, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	issuer := strings.TrimRight(config.String("IDP_ISSUER", "http://localhost:8081"), "/")
	cryptoKey := config.String("IDP_CRYPTO_KEY", "")
	if strings.HasPrefix(cryptoKey, "dev-only") {
		slog.Warn("IDP_CRYPTO_KEY is the development default; set a random value in production")
	}
	cfg := idp.Config{
		Issuer:        issuer,
		CryptoKey:     cryptoKey,
		AllowSignup:   config.Bool("IDP_ALLOW_SIGNUP", false),
		SecureCookies: config.Bool("IDP_SECURE_COOKIES", strings.HasPrefix(issuer, "https://")),
		TrustProxy:    config.Bool("IDP_TRUST_PROXY", false),
	}
	if id := config.String("IDP_APP_CLIENT_ID", ""); id != "" {
		cfg.Clients = append(cfg.Clients, idp.ClientConfig{
			ID:                     id,
			Name:                   "CRM web app",
			Secret:                 config.String("IDP_APP_CLIENT_SECRET", ""),
			RedirectURIs:           config.List("IDP_APP_REDIRECT_URIS", nil),
			PostLogoutRedirectURIs: config.List("IDP_APP_POST_LOGOUT_URIS", nil),
			DevMode:                strings.HasPrefix(issuer, "http://"),
		})
	}
	handler, storage, err := idp.NewHandler(ctx, pool, cfg)
	if err != nil {
		return err
	}
	go storage.Cleanup(ctx, time.Hour)

	srv := &http.Server{
		Addr:              config.String("IDP_ADDR", ":8081"),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()
	slog.Info("identity provider listening", "addr", srv.Addr, "issuer", issuer)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func userCmd(ctx context.Context, sub string, args []string) error {
	fset := flag.NewFlagSet("user "+sub, flag.ExitOnError)
	email := fset.String("email", "", "account email")
	name := fset.String("name", "", "full name (add only)")
	fset.Parse(args)

	pool, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	users := idp.NewUsers(pool)

	if sub == "list" {
		list, err := users.List(ctx)
		if err != nil {
			return err
		}
		for _, u := range list {
			status := "active"
			if u.Disabled {
				status = "disabled"
			}
			fmt.Printf("%s  %-30s  %-24s  %s\n", u.ID, u.Email, u.Name, status)
		}
		return nil
	}
	if *email == "" {
		return errors.New("-email is required")
	}
	switch sub {
	case "add":
		pw, err := readPassword()
		if err != nil {
			return err
		}
		u, err := users.Create(ctx, *email, *name, pw)
		if err != nil {
			return err
		}
		fmt.Println("created user", u.ID)
	case "passwd":
		u, err := users.ByEmail(ctx, *email)
		if err != nil {
			return err
		}
		pw, err := readPassword()
		if err != nil {
			return err
		}
		if err := users.SetPassword(ctx, u.ID, pw); err != nil {
			return err
		}
		fmt.Println("password updated")
	case "disable", "enable":
		u, err := users.ByEmail(ctx, *email)
		if err != nil {
			return err
		}
		if err := users.SetDisabled(ctx, u.ID, sub == "disable"); err != nil {
			return err
		}
		fmt.Println("user", sub+"d")
	default:
		return fmt.Errorf("unknown user command %q", sub)
	}
	return nil
}

// readPassword reads one line from stdin (IDP_PASSWORD env var takes precedence, for scripts).
func readPassword() (string, error) {
	if pw := os.Getenv("IDP_PASSWORD"); pw != "" {
		return pw, nil
	}
	fmt.Fprint(os.Stderr, "Password (min 10 chars): ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

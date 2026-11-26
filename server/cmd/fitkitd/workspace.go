package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"fitkit/server/internal/config"
	"fitkit/server/internal/workspace/domain"
	"fitkit/server/internal/workspace/httpapi"
	"fitkit/server/internal/workspace/integrations"
	"fitkit/server/internal/workspace/seed"
	"fitkit/server/internal/workspace/service"
	wsstore "fitkit/server/internal/workspace/store"
)

// workspace is the employee side of Fitkit (desktop app): orders bought from the
// app, buyers' work on them, parcels and support tickets.
type workspace struct {
	svc     *service.Service
	handler http.Handler
}

func newWorkspace(ctx context.Context, cfg config.Config, st *wsstore.Store, log *slog.Logger) (*workspace, error) {
	if err := st.Migrate(ctx); err != nil {
		return nil, err
	}
	if err := bootstrapAdmin(ctx, cfg, st, log); err != nil {
		return nil, err
	}
	_, ints := integrations.NewFakes(log)
	hub := httpapi.NewHub()
	svc := &service.Service{Store: st, Int: ints, Events: hub, Log: log}
	if cfg.ClientAppWebhookSecret == "" || cfg.TrackerWebhookSecret == "" {
		log.Warn("a workspace webhook secret is not set: the matching /webhooks/* endpoints answer 503 until it is")
	}
	h := httpapi.New(httpapi.Config{
		Service: svc, Hub: hub, Log: log,
		ClientAppSecret: cfg.ClientAppWebhookSecret, TrackerSecret: cfg.TrackerWebhookSecret,
		AllowOrigin: cfg.AllowOrigin, SecureCookie: cfg.SecureCookie, WebDir: cfg.WebDir,
	})
	return &workspace{svc: svc, handler: h}, nil
}

// routes sends the mobile API and media to the app server and everything else
// (/api, /webhooks and the desktop front-end) to the workspace.
func routes(app, ws http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/v1/", app)
	mux.Handle("/media/", app)
	mux.Handle("/healthz", app)
	mux.Handle("/", ws)
	return mux
}

// bootstrapAdmin creates the first employee (a shift lead) while there are none,
// so a fresh deployment needs no shell access.
func bootstrapAdmin(ctx context.Context, cfg config.Config, st *wsstore.Store, log *slog.Logger) error {
	if cfg.BootstrapAdminEmail == "" || cfg.BootstrapAdminPassword == "" {
		return nil
	}
	n, err := st.Q().CountEmployees(ctx)
	if err != nil || n > 0 {
		return err
	}
	if len(cfg.BootstrapAdminPassword) < 10 {
		return errors.New("FITKIT_BOOTSTRAP_ADMIN_PASSWORD must be at least 10 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.BootstrapAdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	e, err := st.Q().CreateEmployee(ctx, cfg.BootstrapAdminName, cfg.BootstrapAdminEmail, domain.RoleLead, string(hash))
	if err == nil {
		log.Info("first employee created from FITKIT_BOOTSTRAP_ADMIN_*", "email", e.Email)
	}
	return err
}

// workspaceCommand runs a one-off workspace command and reports whether args named one.
//
//	fitkitd create-employee -name … -email … -role buyer|lead|warehouse|support -password …
//	fitkitd seed-workspace          wipes workspace data and loads the demo (needs ALLOW_SEED=true)
func workspaceCommand(ctx context.Context, args []string, st *wsstore.Store, log *slog.Logger) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "create-employee":
		fs := flag.NewFlagSet(args[0], flag.ExitOnError)
		name := fs.String("name", "", "full name")
		email := fs.String("email", "", "login e-mail")
		role := fs.String("role", "buyer", "buyer | lead | warehouse | support")
		pass := fs.String("password", "", "password (10+ characters)")
		_ = fs.Parse(args[1:])
		if *name == "" || *email == "" || len(*pass) < 10 {
			return true, errors.New("create-employee: -name, -email and -password (10+ characters) are required")
		}
		switch domain.Role(*role) {
		case domain.RoleBuyer, domain.RoleLead, domain.RoleWarehouse, domain.RoleSupport:
		default:
			return true, fmt.Errorf("unknown role %q", *role)
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*pass), bcrypt.DefaultCost)
		if err != nil {
			return true, err
		}
		e, err := st.Q().CreateEmployee(ctx, *name, strings.TrimSpace(*email), domain.Role(*role), string(hash))
		if err == nil {
			log.Info("employee created", "email", e.Email, "role", e.Role)
		}
		return true, err
	case "seed-workspace":
		if os.Getenv("ALLOW_SEED") != "true" {
			return true, errors.New("seed-workspace WIPES all workspace data (orders, employees, tickets) and loads demo data; set ALLOW_SEED=true to confirm")
		}
		if err := seed.Load(ctx, st); err != nil {
			return true, err
		}
		log.Info("workspace demo data loaded", "login", "anna@fitkit.space", "password", seed.DemoPassword)
		return true, nil
	}
	return false, nil
}

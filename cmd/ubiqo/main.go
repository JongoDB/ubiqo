// Command ubiqo is both the server (serve/migrate/admin) and the client-side
// CLI the bootstrap plugin ships (login/init/hook). One binary, one version.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jongodb/ubiqo/internal/config"
	"github.com/jongodb/ubiqo/internal/core"
	"github.com/jongodb/ubiqo/internal/gitstore"
	"github.com/jongodb/ubiqo/internal/httpapi"
	"github.com/jongodb/ubiqo/internal/store"
)

// Version is stamped by the release build (-ldflags "-X main.Version=v0.1.0").
var Version = "0.1.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "serve":
		err = cmdServe(args)
	case "migrate":
		err = withService(func(ctx context.Context, svc *core.Service) error { return nil }) // Migrate runs in withService
	case "setup":
		err = cmdSetup(args)
	case "seed":
		err = cmdSeed(args)
	case "org", "project", "user", "device":
		err = cmdAdmin(cmd, args)
	case "backup":
		err = cmdBackup(args)
	case "fsck":
		err = cmdFsck(args)
	case "login":
		err = cmdLogin(args)
	case "init":
		err = cmdInit(args)
	case "hook":
		err = cmdHook(args)
	case "context":
		err = cmdContext(args)
	case "version":
		fmt.Println("ubiqo", Version)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ubiqo:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `ubiqo — self-hosted context fabric for AI clients

Server:
  serve [--local]                 run the server (env: UBIQO_DATABASE_URL, UBIQO_DATA_DIR, UBIQO_PUBLIC_URL)
  migrate                         apply database migrations and exit
  setup --org SLUG --user NAME [--project SLUG]   bootstrap org+owner+token in one shot
  seed                            load the demo "acme" fixture
  org set-instructions --org SLUG --file F
  project create --org SLUG --slug S --name N [--team]
  project add-member --org SLUG --project S --user U --role maintainer|contributor|viewer
  project set-instructions --org SLUG --project S --file F
  user create --username U [--display D]
  device create --org SLUG --user U --label L     mint a device token (printed once)
  device list --org SLUG | device revoke --id ID
  backup --out DIR                pg_dump + git bundles + manifest
  fsck                            rebuild release projections from git tags

Client (used by the Claude Code plugin):
  login --server URL --token TOK  store credentials in ~/.ubiqo/config.json
  init --project SLUG             bind the current directory to a project (ubiqo.yaml)
  context pull [--project SLUG]   print the compiled context (what hooks inject)
  hook session-start|session-end  Claude Code hook entrypoints (fail-open)
  version
`)
}

// withService wires config → store(+migrate) → gitstore → core.Service.
func withService(f func(ctx context.Context, svc *core.Service) error) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	svc := &core.Service{DB: db, Git: gitstore.New(cfg.DataDir), Version: Version}
	return f(ctx, svc)
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	local := fs.Bool("local", false, "localhost eval mode: no UBIQO_PUBLIC_URL required")
	_ = fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.Local = *local
	if err := cfg.Validate(); err != nil {
		return err
	}
	level := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	ctx := context.Background()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	svc := &core.Service{DB: db, Git: gitstore.New(cfg.DataDir), Version: Version}
	api := &httpapi.Server{Svc: svc, Log: log, Public: cfg.PublicURL}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Info("ubiqo serving", "version", Version, "config", cfg.Redacted())

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-errCh:
		return err
	case <-stop:
		log.Info("shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	}
}

func cmdSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	org := fs.String("org", "", "org slug")
	orgName := fs.String("org-name", "", "org display name (default: slug)")
	user := fs.String("user", "", "owner username")
	display := fs.String("display", "", "owner display name")
	project := fs.String("project", "", "optional first project slug (solo mode)")
	label := fs.String("label", "first-device", "device token label")
	_ = fs.Parse(args)
	if *org == "" || *user == "" {
		return fmt.Errorf("setup requires --org and --user")
	}
	if *orgName == "" {
		*orgName = *org
	}
	return withService(func(ctx context.Context, svc *core.Service) error {
		o, err := svc.DB.CreateOrg(ctx, *org, *orgName)
		if err != nil {
			return fmt.Errorf("create org: %w", err)
		}
		u, err := svc.DB.CreateUser(ctx, *user, *display)
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		if err := svc.DB.AddOrgMember(ctx, o.ID, u.ID, "owner"); err != nil {
			return err
		}
		token, err := svc.DB.NewToken(ctx, o.ID, u.ID, *label, "cli")
		if err != nil {
			return err
		}
		fmt.Printf("org %q created; owner %q\n", *org, *user)
		if *project != "" {
			p, err := svc.DB.CreateProject(ctx, o.ID, *project, *project, true)
			if err != nil {
				return err
			}
			if err := svc.DB.AddProjectMember(ctx, p, u.ID, "maintainer"); err != nil {
				return err
			}
			if err := svc.Git.EnsureRepo(ctx, o.Slug, p.Slug); err != nil {
				return err
			}
			fmt.Printf("project %q created (solo mode)\n", *project)
		}
		fmt.Printf(`
device token (shown ONCE — store it now):

  %s

Connect Claude Code:
  claude mcp add --transport http ubiqo <server-url>/mcp --header "Authorization: Bearer %s"

Or for hooks/CLI on this machine:
  ubiqo login --server <server-url> --token %s
`, token, token, token)
		return nil
	})
}

func cmdAdmin(noun string, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("%s needs a verb", noun)
	}
	verb, rest := args[0], args[1:]
	fs := flag.NewFlagSet(noun+" "+verb, flag.ExitOnError)
	org := fs.String("org", "", "org slug")
	slug := fs.String("slug", "", "slug")
	name := fs.String("name", "", "display name")
	team := fs.Bool("team", false, "team mode (default is solo)")
	project := fs.String("project", "", "project slug")
	user := fs.String("user", "", "username")
	display := fs.String("display", "", "display name")
	role := fs.String("role", "contributor", "role")
	file := fs.String("file", "", "file with instructions markdown ('-' for stdin)")
	label := fs.String("label", "", "device label")
	id := fs.String("id", "", "id")
	_ = fs.Parse(rest)

	return withService(func(ctx context.Context, svc *core.Service) error {
		getOrg := func() (*store.Org, error) {
			if *org == "" {
				return svc.DB.FirstOrg(ctx)
			}
			return svc.DB.GetOrgBySlug(ctx, *org)
		}
		switch noun + " " + verb {
		case "user create":
			if *user == "" {
				return fmt.Errorf("--user required")
			}
			u, err := svc.DB.CreateUser(ctx, *user, *display)
			if err != nil {
				return err
			}
			o, err := getOrg()
			if err == nil {
				_ = svc.DB.AddOrgMember(ctx, o.ID, u.ID, "member")
			}
			fmt.Println("user created:", u.Username)
			return nil
		case "project create":
			o, err := getOrg()
			if err != nil {
				return err
			}
			if *slug == "" {
				return fmt.Errorf("--slug required")
			}
			if *name == "" {
				*name = *slug
			}
			p, err := svc.DB.CreateProject(ctx, o.ID, *slug, *name, !*team)
			if err != nil {
				return err
			}
			if err := svc.Git.EnsureRepo(ctx, o.Slug, p.Slug); err != nil {
				return err
			}
			mode := "solo"
			if *team {
				mode = "team"
			}
			fmt.Printf("project %q created (%s mode)\n", *slug, mode)
			return nil
		case "project add-member":
			o, err := getOrg()
			if err != nil {
				return err
			}
			p, err := svc.DB.GetProject(ctx, o.ID, *project)
			if err != nil {
				return err
			}
			u, err := svc.DB.GetUserByUsername(ctx, *user)
			if err != nil {
				return err
			}
			if err := svc.DB.AddProjectMember(ctx, p, u.ID, *role); err != nil {
				return err
			}
			fmt.Printf("%s added to %s as %s\n", *user, *project, *role)
			return nil
		case "project set-instructions", "org set-instructions":
			o, err := getOrg()
			if err != nil {
				return err
			}
			text, err := readFileOrStdin(*file)
			if err != nil {
				return err
			}
			if noun == "org" {
				return svc.DB.SetOrgInstructions(ctx, o.ID, text)
			}
			p, err := svc.DB.GetProject(ctx, o.ID, *project)
			if err != nil {
				return err
			}
			return svc.DB.SetProjectInstructions(ctx, p.ID, text)
		case "device create":
			o, err := getOrg()
			if err != nil {
				return err
			}
			u, err := svc.DB.GetUserByUsername(ctx, *user)
			if err != nil {
				return err
			}
			if *label == "" {
				*label = *user + "-device"
			}
			token, err := svc.DB.NewToken(ctx, o.ID, u.ID, *label, "")
			if err != nil {
				return err
			}
			fmt.Println("device token (shown ONCE):")
			fmt.Println(" ", token)
			return nil
		case "device list":
			o, err := getOrg()
			if err != nil {
				return err
			}
			rows, err := svc.DB.ListTokens(ctx, o.ID)
			if err != nil {
				return err
			}
			for _, r := range rows {
				fmt.Printf("%s  %-12s %-20s %-8s last_seen=%s  %s\n", r["id"], r["user"], r["label"], r["state"], r["last_seen"], r["client"])
			}
			return nil
		case "device revoke":
			uid, err := parseUUID(*id)
			if err != nil {
				return err
			}
			if err := svc.DB.RevokeToken(ctx, uid); err != nil {
				return err
			}
			fmt.Println("revoked", *id)
			return nil
		}
		return fmt.Errorf("unknown command %s %s", noun, verb)
	})
}

func readFileOrStdin(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("--file required ('-' for stdin)")
	}
	if path == "-" {
		b, err := readAll(os.Stdin)
		return string(b), err
	}
	b, err := os.ReadFile(path)
	return strings.TrimSpace(string(b)), err
}

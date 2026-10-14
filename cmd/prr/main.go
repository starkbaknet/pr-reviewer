package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"prr/internal/config"
	"prr/internal/gitea"
	"prr/internal/review"
	"prr/internal/store"
	"prr/internal/web"
)

const version = "0.3.0"

const usage = `usage: prr <command> [options]

Review Gitea pull requests with Claude over MCP, with a web dashboard.

commands:
  serve [--addr HOST:PORT]  dashboard at http://ADDR/ and MCP endpoint at /mcp
  mcp                       MCP server on stdin/stdout (for "claude mcp add")
  setup [--global]          ask for the Gitea URL and token, write .env
  check                     verify the Gitea connection and show the config in use
  version
  help

config (.env in the project, ~/.config/prr/config.env, or the environment):
  GITEA_URL, GITEA_TOKEN    required
  PRR_ADDR                  listen address, default 127.0.0.1:8787
  PRR_DATA                  session files, default ./data next to .env
  PRR_DIFF_LIMIT            inline the diff up to this many bytes, default 120000
  PRR_ENV                   path of an extra env file to read

See prr(1) for details.
`

func main() {
	log.SetFlags(log.Ltime)
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "mcp":
		err = stdio()
	case "setup":
		err = setup(os.Args[2:])
	case "check":
		err = check()
	case "version", "--version", "-v":
		fmt.Println("prr", version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "prr: unknown command %q\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "prr:", err)
		os.Exit(1)
	}
}

func build() (config.Config, *review.Service, *store.Store, *mcp.Server, error) {
	cfg, err := config.Load()
	if err != nil {
		return cfg, nil, nil, nil, err
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return cfg, nil, nil, nil, err
	}
	svc := review.New(cfg, gitea.New(cfg.GiteaURL, cfg.GiteaToken), st)
	srv := mcp.NewServer(&mcp.Implementation{Name: "prr", Version: version},
		&mcp.ServerOptions{Instructions: review.Instructions})
	svc.Register(srv)
	return cfg, svc, st, srv, nil
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "", "listen address (default $PRR_ADDR or 127.0.0.1:8787)")
	fs.Parse(args)

	cfg, svc, st, srv, err := build()
	if err != nil {
		return err
	}
	if *addr != "" {
		cfg.Addr = *addr
	}

	hs := &http.Server{Addr: cfg.Addr, Handler: web.New(svc, st, srv).Handler()}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		hs.Shutdown(context.Background())
	}()

	log.Printf("dashboard http://%s/", cfg.Addr)
	log.Printf("mcp       http://%s/mcp", cfg.Addr)
	log.Printf("sessions  %s", cfg.DataDir)
	if err := hs.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func stdio() error {
	/* stdout is the protocol channel; everything else goes to stderr */
	log.SetOutput(os.Stderr)
	_, _, _, srv, err := build()
	if err != nil {
		return err
	}
	return srv.Run(context.Background(), &mcp.StdioTransport{})
}

func check() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	g := gitea.New(cfg.GiteaURL, cfg.GiteaToken)
	ctx := context.Background()
	v, err := g.Version(ctx)
	if err != nil {
		return err
	}
	me, err := g.Me(ctx)
	if err != nil {
		return err
	}
	repos, err := g.Repos(ctx)
	if err != nil {
		return err
	}
	src := cfg.Source
	if src == "" {
		src = "environment"
	}
	fmt.Printf("gitea %s at %s, user %s, %d repos\nconfig %s\ndata   %s\n",
		v, cfg.GiteaURL, me.Login, len(repos), src, cfg.DataDir)
	return nil
}

func setup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	global := fs.Bool("global", false, "write ~/.config/prr/config.env instead of ./.env")
	fs.Parse(args)

	path := config.ProjectPath()
	if *global {
		path = config.GlobalPath()
	}
	cur, _ := config.Load()

	in := bufio.NewReader(os.Stdin)
	ask := func(prompt, def string) string {
		if def != "" {
			fmt.Printf("%s [%s]: ", prompt, def)
		} else {
			fmt.Printf("%s: ", prompt)
		}
		s, _ := in.ReadString('\n')
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
		return def
	}

	fmt.Printf("writing %s\n\n", path)
	url := strings.TrimRight(ask("Gitea URL", cur.GiteaURL), "/")
	fmt.Printf("  token: %s/user/settings/applications\n", url)
	fmt.Println("  scopes: user read, repository read+write, issue read+write")
	token := ask("Gitea token", cur.GiteaToken)
	addr := ask("listen address", cur.Addr)

	if err := config.Save(path, map[string]string{"GITEA_URL": url, "GITEA_TOKEN": token, "PRR_ADDR": addr}); err != nil {
		return err
	}
	if me, err := gitea.New(url, token).Me(context.Background()); err != nil {
		fmt.Printf("\nsaved, but Gitea rejected the token: %v\n", err)
	} else {
		fmt.Printf("\nsaved; authenticated as %s\n", me.Login)
	}
	fmt.Printf("\nnext:\n  prr serve\n  claude mcp add --transport http prr http://%s/mcp\n", addr)
	return nil
}

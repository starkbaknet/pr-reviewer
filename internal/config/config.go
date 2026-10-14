package config

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	GiteaURL   string
	GiteaToken string
	DataDir    string
	Addr       string
	DiffLimit  int
	Source     string /* file that supplied the token, "" if env */
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func GlobalPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "prr", "config.env")
	}
	return filepath.Join(home(), ".config", "prr", "config.env")
}

/*
 * The project .env: nearest one walking up from the cwd, or ./.env if
 * there is none yet. Walking up means "prr serve" works from any
 * subdirectory of the checkout.
 */
func ProjectPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ".env"
	}
	for d := cwd; ; d = filepath.Dir(d) {
		p := filepath.Join(d, ".env")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		if filepath.Dir(d) == d {
			return filepath.Join(cwd, ".env")
		}
	}
}

func exeEnv() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Join(filepath.Dir(exe), ".env")
}

func readEnv(path string, into map[string]string, from map[string]string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if v == "" {
			continue
		}
		k = strings.TrimSpace(k)
		into[k] = v
		from[k] = path
	}
}

/*
 * Lowest to highest priority: global file, .env next to the binary,
 * project .env, $PRR_ENV, then the environment itself.
 */
func Load() (Config, error) {
	vals := map[string]string{}
	from := map[string]string{}
	for _, p := range []string{GlobalPath(), exeEnv(), ProjectPath(), os.Getenv("PRR_ENV")} {
		if p != "" {
			readEnv(p, vals, from)
		}
	}
	get := func(k, def string) string {
		if v := os.Getenv(k); v != "" {
			from[k] = ""
			return v
		}
		if v, ok := vals[k]; ok {
			return v
		}
		return def
	}

	var c Config
	c.GiteaURL = strings.TrimRight(get("GITEA_URL", ""), "/")
	c.GiteaToken = get("GITEA_TOKEN", "")
	c.Addr = get("PRR_ADDR", "127.0.0.1:8787")
	c.Source = from["GITEA_TOKEN"]

	/* sessions live next to the .env that holds the token */
	data := filepath.Join(home(), ".local", "share", "prr")
	if c.Source != "" {
		data = filepath.Join(filepath.Dir(c.Source), "data")
	}
	c.DataDir = get("PRR_DATA", data)
	if !filepath.IsAbs(c.DataDir) {
		base := filepath.Dir(c.Source)
		if c.Source == "" {
			base, _ = os.Getwd()
		}
		c.DataDir = filepath.Join(base, c.DataDir)
	}

	c.DiffLimit, _ = strconv.Atoi(get("PRR_DIFF_LIMIT", "120000"))
	if c.DiffLimit <= 0 {
		c.DiffLimit = 120000
	}

	var missing []string
	if c.GiteaURL == "" {
		missing = append(missing, "GITEA_URL")
	}
	if c.GiteaToken == "" {
		missing = append(missing, "GITEA_TOKEN")
	}
	if len(missing) > 0 {
		return c, errors.New("missing " + strings.Join(missing, ", ") +
			"; run 'prr setup' or edit " + ProjectPath())
	}
	return c, nil
}

func Save(path string, kv map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# prr config. Contains a token: keep it out of git.\n")
	for _, k := range []string{"GITEA_URL", "GITEA_TOKEN", "PRR_ADDR", "PRR_DATA", "PRR_DIFF_LIMIT"} {
		if v := kv[k]; v != "" {
			b.WriteString(k + "=" + v + "\n")
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

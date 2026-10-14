pr-reviewer (prr)
=================

Review Gitea pull requests from Claude Code.

    https://github.com/starkbaknet/pr-reviewer

prr is an MCP server. Claude calls it to list repositories and pull
requests, read the diff and the surrounding code, and post the review
with inline comments. A small web dashboard shows what Claude did on
each pull request and what the author has done about it since: which
comments are resolved, what was pushed, CI, merge state.

    Claude Code  --mcp-->  prr  --rest-->  Gitea
                            |
                            +-->  http://127.0.0.1:8787  (dashboard)

No Anthropic API key is involved; Claude Code is the reviewer.


Install
-------

    git clone git@github.com:starkbaknet/pr-reviewer.git
    cd pr-reviewer
    ./install.sh --service

This builds the binary with Go, installs `prr` and its man page under
~/.local, adds ~/.local/bin to PATH if needed, installs the /review-pr
skill and the MCP server into Claude Code, asks for the Gitea URL and
token, and (with --service) starts `prr serve` as a systemd user
service. `./install.sh --help` lists the options; `--uninstall` undoes
it all except .env and data/.

Needs Go 1.22 or newer.

Gitea token: GITEA_URL/user/settings/applications, with the scopes
user (read), repository (read+write) and issue (read+write).


Use
---

    prr serve        # if not running as a service
    prr check        # verify the token and show the config in use
    man prr          # the manual

Restart Claude Code once after installing so it picks up the MCP
server, then:

    /prr baktash/nest-dokploy-demo#1
    post it
    what did the author fix since my review?

The dashboard at http://127.0.0.1:8787 shows the session as soon as
Claude loads the pull request.


Comments
--------

Every inline comment is rendered by prr, not by Claude, so they all
read the same way:

    **[major] Issue:** one or two plain sentences: what is wrong and
    what goes wrong because of it.

    **Fix:**
    ```suggestion
        const template = Object.hasOwn(TEMPLATES, lang) ? TEMPLATES[lang] : undefined;
    ```

    **Why:** why we do it this way.

    **Why not the alternative:** the tempting other option and why not.

A fix that replaces exactly the commented line becomes a Gitea
suggestion the author can apply with one click. Comment positions are
checked against the diff before anything is posted.


Config
------

prr reads, in increasing priority, ~/.config/prr/config.env, a .env
next to the binary, the nearest .env in the working directory or a
parent, the file in $PRR_ENV, and the environment.

    GITEA_URL       required
    GITEA_TOKEN     required
    PRR_ADDR        listen address, default 127.0.0.1:8787
    PRR_DATA        session files, default ./data next to .env
    PRR_DIFF_LIMIT  inline the diff up to this many bytes, default 120000
    PRR_ENV         one more env file to read

Run `prr serve` from the checkout so it finds ./.env. The stdio
transport (`claude mcp add prr -- prr mcp`) is started by Claude Code
from wherever you are working, so pass the file explicitly:

    claude mcp add prr -e PRR_ENV=$HOME/dev/pr-reviewer/.env -- prr mcp


Layout
------

    cmd/prr           the command
    internal/gitea    Gitea API client
    internal/diff     unified diff parser and OLD/NEW annotation
    internal/review   MCP tools, comment rendering, follow-up status
    internal/store    one JSON file per reviewed pull request
    internal/web      dashboard and the HTTP MCP endpoint
    skill/review-pr   the Claude Code skill
    doc/prr.1         manual page
    install.sh        installer

    go test ./...

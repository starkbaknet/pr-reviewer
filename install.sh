#!/bin/sh
# Build prr from this checkout and install it. See --help.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
prefix=${PREFIX:-$HOME/.local}
claude=1
setup=1
service=0
uninstall=0

usage() {
	cat <<'EOT'
usage: ./install.sh [--prefix DIR] [--service] [--no-claude] [--no-setup]
       ./install.sh --uninstall

Builds prr with Go and installs:
  PREFIX/bin/prr                      the command        (PREFIX defaults to ~/.local)
  PREFIX/share/man/man1/prr.1         the manual         (man prr)
  ~/.claude/skills/review-pr          the /review-pr skill for Claude Code
  MCP server "prr" in Claude Code     http://127.0.0.1:8787/mcp, user scope

Then runs "prr setup" to ask for the Gitea URL and token, unless ./.env
already exists.

  --prefix DIR   install under DIR instead of ~/.local
  GO=/path/to/go use that Go binary (when it is not on PATH)
  --service      also install and start a systemd user service that keeps
                 "prr serve" running (dashboard + MCP endpoint)
  --no-claude    skip the skill and the MCP registration
  --no-setup     skip the interactive configuration
  --uninstall    remove all of the above; .env and data/ are kept
  -h, --help     this text
EOT
}

die() {
	echo "install.sh: $*" >&2
	exit 1
}

while [ $# -gt 0 ]; do
	case $1 in
	--prefix) prefix=$2; shift ;;
	--prefix=*) prefix=${1#*=} ;;
	--service) service=1 ;;
	--no-claude) claude=0 ;;
	--no-setup) setup=0 ;;
	--uninstall) uninstall=1 ;;
	-h|--help) usage; exit 0 ;;
	*) die "unknown option $1" ;;
	esac
	shift
done

bin=$prefix/bin
man=$prefix/share/man/man1
skill=$HOME/.claude/skills/review-pr
unit=$HOME/.config/systemd/user/prr.service

if [ $uninstall = 1 ]; then
	if [ -f "$unit" ] && command -v systemctl >/dev/null; then
		systemctl --user disable --now prr.service 2>/dev/null || true
		rm -f "$unit"
		systemctl --user daemon-reload 2>/dev/null || true
		echo "removed service"
	fi
	if [ $claude = 1 ]; then
		command -v claude >/dev/null && claude mcp remove --scope user prr >/dev/null 2>&1 && echo "removed MCP server"
		[ -d "$skill" ] && rm -rf "$skill" && echo "removed $skill"
	fi
	rm -f "$bin/prr" "$man/prr.1"
	echo "removed $bin/prr and $man/prr.1"
	echo "kept $here/.env and $here/data"
	exit 0
fi

# Find go even when it is not on PATH: a tarball under /usr/local, the
# Debian/Fedora packages, snap, a per-user install, or sudo's trimmed PATH.
find_go() {
	[ -n "${GO:-}" ] && [ -x "$GO" ] && { echo "$GO"; return; }
	command -v go 2>/dev/null && return
	home=${SUDO_USER:+$(getent passwd "$SUDO_USER" | cut -d: -f6)}
	for d in /usr/local/go/bin /usr/lib/go/bin /usr/lib/golang/bin /usr/lib/go-*/bin \
		/snap/bin /opt/go/bin "${GOROOT:-/nonexistent}/bin" \
		"$HOME/go/bin" "$HOME/.go/bin" "$HOME/sdk"/go*/bin \
		"${home:-/nonexistent}/go/bin" "${home:-/nonexistent}/.go/bin"; do
		[ -x "$d/go" ] && { echo "$d/go"; return; }
	done
	return 1
}

go=$(find_go) || die "cannot find go. Install it (Debian/Ubuntu: sudo apt install golang-go; Fedora/RHEL: sudo dnf install golang; other: https://go.dev/dl) or point me at it: GO=/path/to/go ./install.sh"
echo "go $("$go" env GOVERSION | sed s/^go//) ($go)"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
(cd "$here" && "$go" build -trimpath -ldflags='-s -w' -o "$tmp/prr" ./cmd/prr)

mkdir -p "$bin" "$man"
install -m 755 "$tmp/prr" "$bin/prr"
install -m 644 "$here/doc/prr.1" "$man/prr.1"
echo "installed $bin/prr ($("$bin/prr" version)) and $man/prr.1"

# Make sure the shell finds it. man(1) derives its own path from PATH, so
# PREFIX/bin on PATH also makes "man prr" work.
case ":$PATH:" in
*":$bin:"*) ;;
*)
	rc=
	case ${SHELL:-} in
	*/zsh) rc=$HOME/.zshrc ;;
	*/bash) rc=$HOME/.bashrc ;;
	esac
	line="export PATH=\"$bin:\$PATH\""
	if [ -n "$rc" ]; then
		grep -qF "$line" "$rc" 2>/dev/null || printf '\n# prr\n%s\n' "$line" >>"$rc"
		echo "added $bin to PATH in $rc; open a new shell or run: $line"
	else
		echo "add $bin to your PATH: $line"
	fi
	PATH=$bin:$PATH
	;;
esac

if [ $claude = 1 ]; then
	if command -v claude >/dev/null; then
		mkdir -p "$(dirname "$skill")"
		rm -rf "$skill"
		cp -r "$here/skill/review-pr" "$skill"
		echo "installed skill $skill (/review-pr)"
		addr=$(sed -n 's/^PRR_ADDR=//p' "$here/.env" 2>/dev/null | tr -d '"' || true)
		addr=${addr:-127.0.0.1:8787}
		claude mcp remove --scope user prr >/dev/null 2>&1 || true
		if claude mcp add --transport http --scope user prr "http://$addr/mcp" >/dev/null 2>&1; then
			echo "registered MCP server prr at http://$addr/mcp"
		else
			echo "could not register the MCP server; run: claude mcp add --transport http --scope user prr http://$addr/mcp"
		fi
	else
		echo "claude not found, skipping skill and MCP registration"
	fi
fi

if [ $setup = 1 ]; then
	if [ -f "$here/.env" ]; then
		echo "config $here/.env"
	elif [ -t 0 ]; then
		(cd "$here" && "$bin/prr" setup) || echo "setup did not finish; run 'prr setup' in $here"
	else
		echo "no terminal; run 'prr setup' in $here, or copy .env.example to .env"
	fi
fi

if [ $service = 1 ]; then
	command -v systemctl >/dev/null || die "no systemd; run 'prr serve' yourself"
	mkdir -p "$(dirname "$unit")"
	cat >"$unit" <<EOT
[Unit]
Description=prr dashboard and MCP endpoint
After=network.target

[Service]
WorkingDirectory=$here
ExecStart=$bin/prr serve
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
EOT
	systemctl --user daemon-reload
	systemctl --user enable --now prr.service
	echo "service prr.service running; logs: journalctl --user -u prr -f"
fi

echo
echo "done. next:"
[ -f "$here/.env" ] || echo "  cd $here && prr setup"
echo "  prr check"
[ $service = 1 ] || echo "  cd $here && prr serve"
echo "  man prr"
[ $claude = 1 ] && command -v claude >/dev/null && echo "  restart Claude Code, then: /review-pr owner/repo#1"
exit 0

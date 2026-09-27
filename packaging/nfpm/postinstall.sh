#!/bin/sh
# gwatch-agent package: after install or upgrade.
#
# Called as `postinst configure <old-version>` by dpkg and as `%post <n>` by
# rpm (1 on a first install, 2 on an upgrade); both are handled the same way.
#
# The unit is always enabled, so that a paired machine reports after a reboot.
# It is only (re)started when there is something to report with — a token
# from pairing, or one in /etc/gwatch-agent/agent.env — which is also what
# makes an upgrade take effect: the running agent is restarted onto the new
# binary here, by the package manager that installed it, rather than by the
# agent itself. An unpaired machine is left stopped, and told how to pair.
set -e

unit=gwatch-agent.service
datadir=/var/lib/gwatch-agent
envfile=/etc/gwatch-agent/agent.env

mkdir -p "$datadir"
chmod 0700 "$datadir"

configured=no
if [ -s "$datadir/agent-token" ] || grep -Eq '^[[:space:]]*GWATCH_AGENT_TOKEN=[^[:space:]]' "$envfile" 2>/dev/null; then
	configured=yes
fi

# No systemd (a container, a chroot, an image being built): the unit is
# installed and will be picked up when the machine boots with systemd.
if [ -d /run/systemd/system ]; then
	systemctl daemon-reload >/dev/null 2>&1 || true
	systemctl enable "$unit" >/dev/null 2>&1 || true
	if [ "$configured" = yes ]; then
		systemctl restart "$unit" || echo "gwatch-agent: the service did not start; see: journalctl -u gwatch-agent" >&2
	fi
fi

if [ "$configured" = no ]; then
	cat <<'EOF'

gwatch-agent is installed. Pair this machine with GWatch to start reporting
(GWatch: Nodes > Pair a machine gives you the code):

  sudo gwatch-agent pair --server https://gwatch.lan:7230 --code XXXX-XXXX

That keeps the token in /var/lib/gwatch-agent and starts the gwatch-agent
service. This package keeps the agent up to date; it does not update itself.

EOF
fi
exit 0

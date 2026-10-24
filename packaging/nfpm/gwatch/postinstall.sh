#!/bin/sh
# gwatch package: after install or upgrade.
#
# Called as `postinst configure <old-version>` by dpkg (no old version on a
# first install) and as `%post <n>` by rpm (1 on a first install, 2 on an
# upgrade).
#
# GWatch needs no configuration to start, so unlike the agent's package this
# one enables and (re)starts the service every time: on a first install that
# starts it, and on an upgrade it moves the running service onto the new
# binary — done here, by the package manager that installed it, rather than by
# GWatch replacing itself.
set -e

unit=gwatch.service
datadir=/var/lib/gwatch

first=no
case "$1" in
configure) [ -n "${2:-}" ] || first=yes ;;
1) first=yes ;;
esac

# The service user. systemd-sysusers reads the package's sysusers.d entry;
# useradd is for a system without it (an image being built, a minimal
# container). Either way an existing gwatch user is left as it is.
if ! getent passwd gwatch >/dev/null 2>&1; then
	if command -v systemd-sysusers >/dev/null 2>&1; then
		systemd-sysusers /usr/lib/sysusers.d/gwatch.conf || true
	fi
	if ! getent passwd gwatch >/dev/null 2>&1; then
		nologin=/usr/sbin/nologin
		[ -x "$nologin" ] || nologin=/sbin/nologin
		useradd --system --user-group --home-dir "$datadir" --no-create-home \
			--shell "$nologin" --comment "GWatch network monitor" gwatch
	fi
fi

# The unit's StateDirectory=; systemd creates it too, and takes over the
# ownership of one that already exists (a data directory left by an earlier
# `gwatch install`, which ran as root), so this is for a machine without
# systemd running.
if [ ! -d "$datadir" ]; then
	install -d -m 0700 -o gwatch -g gwatch "$datadir"
fi
chmod 0700 /etc/gwatch 2>/dev/null || true

started=no
# No systemd (a container, a chroot, an image being built): the unit is
# installed and will be picked up when the machine boots with systemd.
if [ -d /run/systemd/system ]; then
	systemctl daemon-reload >/dev/null 2>&1 || true
	systemctl enable "$unit" >/dev/null 2>&1 || true
	if systemctl restart "$unit"; then
		started=yes
	else
		echo "gwatch: the service did not start; see: journalctl -u gwatch" >&2
	fi
fi

if [ "$first" = yes ]; then
	if [ "$started" = yes ]; then
		echo
		echo "GWatch is running: http://127.0.0.1:7230 (this machine only)."
	else
		echo
		echo "GWatch is installed; start it with: sudo systemctl enable --now gwatch"
	fi
	cat <<'EOF'

From another computer, open an SSH tunnel first (ssh -L 7230:127.0.0.1:7230
this-server) and browse to the same address. Create the first account -- it
is always an administrator -- in Settings > Users & access, or from a shell here:

  curl -fsS -H 'Content-Type: application/json' \
    --data '{"username":"admin","password":"choose-a-good-one","role":"admin"}' \
    http://127.0.0.1:7230/api/users

To serve the whole network, set GWATCH_LISTEN=0.0.0.0:7230 in
/etc/gwatch/gwatch.env and run: sudo systemctl restart gwatch
This package keeps GWatch up to date; GWatch does not update itself.

EOF
fi
exit 0

#!/bin/sh
# gwatch package: after removal.
#
# A plain `apt remove`, and an rpm erase, keep everything GWatch knows — the
# database, its encryption key, logs and backups in /var/lib/gwatch, and the
# gwatch user that owns them — so reinstalling picks up where it left off.
# `apt purge` is the explicit request to forget: it deletes /var/lib/gwatch
# and the gwatch user too (dpkg itself deletes /etc/gwatch/gwatch.env). Export
# a backup first (Settings > Backups) if any of it is wanted.
#
# On rpm there is no purge; delete /var/lib/gwatch, /etc/gwatch and the user
# by hand (sudo userdel gwatch) to remove every trace.
set -e

case "$1" in
purge)
	rm -rf /var/lib/gwatch
	rmdir /etc/gwatch 2>/dev/null || true
	if getent passwd gwatch >/dev/null 2>&1; then
		userdel gwatch >/dev/null 2>&1 || true
	fi
	if getent group gwatch >/dev/null 2>&1; then
		groupdel gwatch >/dev/null 2>&1 || true
	fi
	;;
esac

case "$1" in
remove | purge | 0)
	if [ -d /run/systemd/system ]; then
		systemctl daemon-reload >/dev/null 2>&1 || true
	fi
	;;
esac
exit 0

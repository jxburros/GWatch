#!/bin/sh
# gwatch-agent package: after removal.
#
# `apt purge` (dpkg's `postrm purge`) also deletes the token and the stored
# server address, which is what purging is for. A plain remove, and an rpm
# erase, leave them in /var/lib/gwatch-agent, so reinstalling does not mean
# pairing again; delete the directory to forget the machine's credential, and
# revoke the machine in GWatch (Settings > Hardware) to make it worthless.
set -e

case "$1" in
purge)
	rm -rf /var/lib/gwatch-agent
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

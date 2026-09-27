#!/bin/sh
# gwatch-agent package: before removal.
#
# dpkg calls `prerm remove` (or `upgrade <new-version>`); rpm calls `%preun 0`
# on an erase and `%preun 1` when the package is being replaced by an upgrade.
# Only a real removal stops the service — an upgrade leaves it running, and the
# new version's postinstall restarts it onto the new binary.
set -e

case "$1" in
remove | deconfigure | 0)
	if [ -d /run/systemd/system ]; then
		systemctl disable --now gwatch-agent.service >/dev/null 2>&1 || true
	fi
	;;
esac
exit 0

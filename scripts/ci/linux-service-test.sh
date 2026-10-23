#!/bin/sh
# Takes the GWatch server through its whole service lifecycle under systemd:
# install, the sandbox it runs in, first account, a ping check, upgrade,
# uninstall — and checks that nothing is left behind (#135).
#
#   scripts/ci/linux-service-test.sh binary  <gwatch-binary>
#   scripts/ci/linux-service-test.sh package <gwatch_*.deb> [<newer gwatch_*.deb>]
#   scripts/ci/linux-service-test.sh exercise            (against a running GWatch)
#
# `binary` is `gwatch install` / `gwatch uninstall --purge --yes`; `package`
# is apt install / apt install of a newer .deb / apt purge
# (packaging/nfpm/gwatch.yaml, #126). Both expect the same service: the unit
# gwatch.service, run as the system user gwatch, with its data in
# /var/lib/gwatch and CAP_NET_RAW as its only capability (#125). It runs on a
# disposable machine with systemd and passwordless sudo — a CI runner — and
# changes that machine's systemd, users and /etc; do not run it on yours.
# `exercise` only talks to the API, so it can be pointed at a GWatch run by
# hand: GWATCH_URL=http://127.0.0.1:7231 scripts/ci/linux-service-test.sh exercise
#
# Environment:
#   GWATCH_URL            where the service answers (http://127.0.0.1:7230)
#   GWATCH_MAX_EXPOSURE   the highest `systemd-analyze security` exposure
#                         accepted (4.5). The packaged unit scores 1.7 on
#                         systemd 255; CI holds it to its own, lower bar.
set -eu

url=${GWATCH_URL:-http://127.0.0.1:7230}
max_exposure=${GWATCH_MAX_EXPOSURE:-4.5}
unit=gwatch.service
datadir=/var/lib/gwatch
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

say() { printf '\n=== %s\n' "$*"; }
fail() {
	echo "::error::$*" >&2
	if command -v journalctl >/dev/null 2>&1; then
		sudo journalctl -u "$unit" --no-pager -n 60 >&2 || true
	fi
	exit 1
}

# json <file> <python expression over d> — prints the value.
json() {
	python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))' "$1" "$2"
}

wait_health() {
	i=0
	while [ $i -lt 60 ]; do
		if curl -fsS -o "$work/health.json" "$url/api/health" 2>/dev/null; then
			return 0
		fi
		i=$((i + 1))
		sleep 1
	done
	fail "$url/api/health did not answer within 60s"
}

# The running service: who it runs as, where its data is, what it may do.
check_service() {
	say "service"
	systemctl is-active --quiet "$unit" || fail "$unit is not active"
	test "$(systemctl is-enabled "$unit")" = enabled || fail "$unit is not enabled"
	fragment=$(systemctl show -p FragmentPath --value "$unit")
	echo "unit file: $fragment"
	systemctl cat "$unit"
	sudo systemd-analyze verify "$fragment" || fail "systemd-analyze verify $fragment"

	test "$(systemctl show -p User --value "$unit")" = gwatch || fail "the unit does not set User=gwatch"
	pid=$(systemctl show -p MainPID --value "$unit")
	[ "$pid" -gt 0 ] || fail "$unit has no main process"
	runs_as=$(ps -o user= -p "$pid" | tr -d ' ')
	test "$runs_as" = gwatch || fail "the service runs as '$runs_as', not gwatch"

	# CAP_NET_RAW is capability 13: the raw ICMP socket for ping. Nothing else.
	capeff=$(sudo awk '/^CapEff:/ {print $2}' "/proc/$pid/status")
	echo "CapEff: $capeff"
	[ $((0x$capeff & 0x2000)) -ne 0 ] || fail "the service lacks CAP_NET_RAW (CapEff $capeff)"
	[ $((0x$capeff & ~0x2000)) -eq 0 ] || fail "the service holds capabilities beyond CAP_NET_RAW (CapEff $capeff)"

	sudo test -d "$datadir" || fail "$datadir is missing"
	owner=$(sudo stat -c '%U %a' "$datadir")
	test "$owner" = "gwatch 700" || fail "$datadir is '$owner', want 'gwatch 700'"

	wait_health
	curl -fsS -o "$work/health.json" "$url/api/health"
	got=$(json "$work/health.json" 'd.get("dataDir","")')
	test "$got" = "$datadir" || fail "GWatch reports its data directory as '$got', want $datadir"
	sudo test -s "$datadir/gwatch.db" || fail "no database in $datadir"

	say "systemd-analyze security"
	sudo systemd-analyze security --no-pager "$unit" | tee "$work/security.txt" | tail -n 1
	exposure=$(awk '/Overall exposure level/ {for (i = 1; i <= NF; i++) if ($i ~ /^[0-9]+\.[0-9]+$/) print $i}' "$work/security.txt" | tail -n 1)
	[ -n "$exposure" ] || fail "could not read the exposure level"
	echo "exposure: $exposure (at most $max_exposure)"
	if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
		echo "systemd-analyze security $unit: **$exposure** (threshold $max_exposure)" >>"$GITHUB_STEP_SUMMARY"
	fi
	awk -v e="$exposure" -v m="$max_exposure" 'BEGIN { exit !(e + 0 <= m + 0) }' ||
		fail "exposure $exposure is above $max_exposure: the unit has lost some of its hardening"
}

# post <path> <json> <outfile> [curl args...] — prints the HTTP status.
post() {
	path=$1 body=$2 out=$3
	shift 3
	curl -sS -o "$out" -w '%{http_code}' -H 'Content-Type: application/json' --data "$body" "$@" "$url$path"
}

# The first account, a sign-in, a ping check and a result from it.
exercise() {
	say "first administrator"
	code=$(post /api/users '{"username":"ci-admin","password":"ci-password-123","role":"admin"}' "$work/user.json")
	test "$code" = 201 || fail "creating the first account answered $code: $(cat "$work/user.json")"
	test "$(json "$work/user.json" 'd["role"]')" = admin || fail "the first account is not an administrator"
	sign_in

	say "ping check"
	code=$(post /api/nodes '{"name":"CI loopback","host":"127.0.0.1","checks":[{"type":"ping","name":"Ping","enabled":true,"intervalSeconds":30,"timeoutSeconds":2}]}' "$work/node.json" -b "$work/cookies")
	case "$code" in 200 | 201) ;; *) fail "creating the node answered $code: $(cat "$work/node.json")" ;; esac
	check=$(json "$work/node.json" 'd["checks"][0]["id"]')
	echo "check $check"
	echo "$check" >"$work/check-id"
	code=$(post "/api/checks/$check/run" '{}' "$work/run.json" -b "$work/cookies")
	test "$code" = 200 || fail "running the check answered $code: $(cat "$work/run.json")"
	cat "$work/run.json"
	echo
	test "$(json "$work/run.json" 'd["success"]')" = True || fail "pinging 127.0.0.1 failed: $(cat "$work/run.json")"
	curl -fsS -b "$work/cookies" -o "$work/results.json" "$url/api/checks/$check/results?limit=5"
	[ "$(json "$work/results.json" 'len(d)')" -ge 1 ] || fail "the check's result was not recorded"
}

sign_in() {
	code=$(post /api/auth/login '{"username":"ci-admin","password":"ci-password-123"}' "$work/login.json" -c "$work/cookies")
	test "$code" = 200 || fail "signing in answered $code: $(cat "$work/login.json")"
	grep -q gwatch_session "$work/cookies" || fail "signing in set no session cookie"
}

# Nothing of GWatch may outlive an uninstall or a purge.
check_gone() {
	say "nothing left"
	if systemctl cat "$unit" >/dev/null 2>&1; then fail "$unit outlived the uninstall"; fi
	if systemctl is-active --quiet "$unit"; then fail "$unit is still running"; fi
	for path in /etc/systemd/system/gwatch.service /usr/lib/systemd/system/gwatch.service /lib/systemd/system/gwatch.service \
		/etc/systemd/system/multi-user.target.wants/gwatch.service "$datadir" /etc/gwatch /usr/local/libexec/gwatch /usr/local/bin/gwatch "$@"; do
		if sudo test -e "$path"; then fail "$path is still there"; fi
	done
	if getent passwd gwatch >/dev/null; then fail "the gwatch user is still there"; fi
	if curl -fsS -o /dev/null "$url/api/health" 2>/dev/null; then fail "something still answers on $url"; fi
	if pgrep -x gwatch >/dev/null; then fail "a gwatch process is still running"; fi
	echo "clean"
}

# gwatch status, stop, start and restart go through internal/svc's systemd
# code (#46) rather than the install path, so they get a round of their own.
control() {
	say "gwatch status / stop / start / restart"
	sudo "$1" status | grep -qx 'Service status: running' || fail "gwatch status did not say running"
	sudo "$1" stop
	systemctl is-active --quiet gwatch && fail "gwatch stop left the service running"
	sudo "$1" status | grep -qx 'Service status: stopped' || fail "gwatch status did not say stopped"
	sudo "$1" start
	wait_health
	sudo "$1" restart
	systemctl is-active --quiet gwatch || fail "gwatch restart left the service stopped"
	wait_health
	echo "controlled"
}

version_of() {
	curl -fsS -o "$work/version.json" "$url/api/version"
	json "$work/version.json" 'd["version"]'
}

case "${1:-}" in
binary)
	[ $# -eq 2 ] || { echo "usage: $0 binary <gwatch-binary>" >&2; exit 2; }
	bin=$2
	say "gwatch install"
	# GitHub's runners leave /usr/local/bin writable by the runner user, and
	# gwatch install refuses a directory anyone but root can write to.
	sudo chown root:root /usr/local /usr/local/bin
	sudo chmod go-w /usr/local /usr/local/bin
	sudo "$bin" install --listen "${url#http://}"
	check_service
	exercise
	control "$bin"
	# Self-update to a locally signed build is not exercised here. The
	# updater only talks to api.github.com (update.Client.APIBase cannot be
	# set on a running service) and trusts the keys built into the binary,
	# so it would take a test build with -X .../update.releaseKeys pinned to a
	# throwaway key, a fake GitHub API behind a locally trusted certificate
	# and api.github.com redirected to it in /etc/hosts — and the hardened
	# unit (ProtectSystem=strict) is free to make the executable read-only to
	# the service anyway. The download, signature and swap are covered by
	# internal/update's tests; the package variant checks the refusal.
	say "gwatch uninstall --purge --yes"
	sudo "$bin" uninstall --purge --yes
	check_gone
	;;
package)
	[ $# -ge 2 ] || { echo "usage: $0 package <deb> [<newer deb>]" >&2; exit 2; }
	deb=$2
	newer=${3:-}
	say "apt install $deb"
	sudo apt-get install -y "$deb"
	test -x /usr/bin/gwatch || fail "/usr/bin/gwatch is missing"
	sudo test "$(sudo stat -c '%a' /etc/gwatch/gwatch.env)" = 600 || fail "/etc/gwatch/gwatch.env is not 0600"
	check_service
	exercise

	say "the package manager owns this copy"
	out=$(sudo /usr/bin/gwatch install 2>&1 || true)
	echo "$out"
	echo "$out" | grep -q 'systemctl' || fail "a packaged gwatch install did not defer to the package's unit"
	if systemctl cat GWatch >/dev/null 2>&1; then fail "a packaged gwatch install registered a second service"; fi
	out=$(sudo /usr/bin/gwatch update --rollback 2>&1 || true)
	echo "$out" | grep -q 'package manager' || fail "a packaged gwatch rolled itself back: $out"
	code=$(post /api/update/apply '{}' "$work/apply.json" -b "$work/cookies")
	cat "$work/apply.json"
	echo
	grep -q 'apt install' "$work/apply.json" || fail "Settings > Updates offered to replace a packaged executable (answered $code)"
	curl -fsS -b "$work/cookies" -o "$work/update.json" "$url/api/update/status"
	test "$(json "$work/update.json" 'd["status"]["packagedBy"]')" = deb || fail "the update status does not say apt owns this copy"
	test "$(json "$work/update.json" 'd["status"]["canApply"]')" = False || fail "the update status offers to install"

	if [ -n "$newer" ]; then
		before=$(version_of)
		pid_before=$(systemctl show -p MainPID --value "$unit")
		say "apt install $newer (upgrade from $before)"
		sudo apt-get install -y "$newer"
		check_service
		after=$(version_of)
		echo "$before -> $after"
		test "$after" != "$before" || fail "the upgrade left $before running"
		test "$(systemctl show -p MainPID --value "$unit")" != "$pid_before" || fail "the upgrade did not restart the service"
		# The data survived: the account still signs in, the check is there.
		sign_in
		check=$(cat "$work/check-id")
		curl -fsS -b "$work/cookies" -o "$work/results.json" "$url/api/checks/$check/results?limit=5"
		[ "$(json "$work/results.json" 'len(d)')" -ge 1 ] || fail "the upgrade lost the check's results"
	fi

	say "apt remove keeps the data"
	sudo apt-get remove -y gwatch
	if systemctl is-active --quiet "$unit"; then fail "$unit outlived apt remove"; fi
	sudo test -s "$datadir/gwatch.db" || fail "apt remove deleted the database"
	say "apt purge"
	sudo apt-get purge -y gwatch
	check_gone /usr/bin/gwatch /usr/lib/sysusers.d/gwatch.conf
	;;
exercise)
	wait_health
	exercise
	;;
*)
	echo "usage: $0 binary <gwatch-binary> | package <deb> [<newer deb>] | exercise" >&2
	exit 2
	;;
esac
echo
echo "linux-service-test: ${1} passed"

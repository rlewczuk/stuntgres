#!/usr/bin/env bash
# Run untrusted external-project code for grind testing inside a locked-down
# container. See docs/GRIND.md, "Sandbox".
#
#   sandbox.sh net up|down SLUG
#   sandbox.sh pg up|down SLUG [-u USER] [-w PASSWORD] [-d DB] [-a ALIAS]
#   sandbox.sh deps SLUG CLONE
#   sandbox.sh gonet SLUG CLONE GOARGS...
#   sandbox.sh exec SLUG CLONE [-n none|internal] [-e K=V]... -- CMD [ARG]...
#   sandbox.sh clean SLUG
#
# exec runs CMD as the calling user with every capability dropped, no Docker
# socket, no host credentials, the stuntgres checkout read-only (its .cswd, .omp
# and .git hidden) and, by default, no network. "internal" joins the run's
# internal network, where `pg up` starts the PostgreSQL 18.6 oracle. Only `deps`
# and `gonet` reach the internet, and they run no project code: `deps` downloads
# modules and compiles tests with `-exec /bin/true`; `gonet` allows only go
# subcommands that execute nothing they build (mod, get, list, env, version, work).
#
# Environment: GRIND_SANDBOX_IMAGE (a Debian-based image with gcc and git; the
# host Go toolchain is mounted over it), GRIND_ORACLE_IMAGE, GRIND_MEMORY,
# GRIND_CPUS, GRIND_PIDS, STUNTGRES_ROOT (default: this checkout).
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ROOT=${STUNTGRES_ROOT:-$(git -C "$HERE" rev-parse --path-format=absolute --git-common-dir | sed 's#/\.git$##')}
IMAGE=${GRIND_SANDBOX_IMAGE:-golang:1.25-trixie}
ORACLE=${GRIND_ORACLE_IMAGE:-$(sed -n 's/^const referenceImage = "\(.*\)"$/\1/p' "$ROOT/reference/fixture/fixture.go")}
MEMORY=${GRIND_MEMORY:-16g}
CPUS=${GRIND_CPUS:-$(nproc)}
PIDS=${GRIND_PIDS:-8192}
GOROOT_HOST=$(go env GOROOT)

die() { echo "sandbox: $*" >&2; exit 2; }
slug() { [[ $1 =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] || die "invalid slug '$1'"; }
net_name() { echo "grind-$1"; }
tmp_dir() { echo "$ROOT/.cswd/tmp/GRIND-$1/sandbox"; }
label() { echo "stuntgres-grind=$1"; }

cmd_net() {
	local action=$1 s=$2
	slug "$s"
	case $action in
	up) docker network inspect "$(net_name "$s")" >/dev/null 2>&1 ||
		docker network create --internal --label "$(label "$s")" "$(net_name "$s")" >/dev/null ;;
	down) docker network rm "$(net_name "$s")" >/dev/null 2>&1 || true ;;
	*) die "net up|down" ;;
	esac
}

cmd_pg() {
	local action=$1 s=$2
	shift 2
	slug "$s"
	local user=postgres password=postgres db="" alias=postgres
	while getopts "u:w:d:a:" o; do
		case $o in
		u) user=$OPTARG ;; w) password=$OPTARG ;; d) db=$OPTARG ;; a) alias=$OPTARG ;;
		*) die "pg: bad option" ;;
		esac
	done
	[[ $alias =~ ^[a-z0-9-]+$ ]] || die "invalid alias '$alias'"
	local name="grind-$s-$alias"
	case $action in
	up)
		[[ -n $ORACLE ]] || die "oracle image not found in reference/fixture/fixture.go"
		cmd_net up "$s"
		docker rm -f "$name" >/dev/null 2>&1 || true
		docker run -d --name "$name" --label "$(label "$s")" \
			--network "$(net_name "$s")" --network-alias "$alias" \
			-e POSTGRES_USER="$user" -e POSTGRES_PASSWORD="$password" \
			-e POSTGRES_DB="${db:-$user}" -e POSTGRES_INITDB_ARGS="--locale=C --encoding=UTF8" \
			"$ORACLE" -c max_prepared_transactions=100 -c fsync=off >/dev/null
		local i
		for i in $(seq 1 120); do
			if docker exec "$name" pg_isready -q -U "$user" -d "${db:-$user}" -h 127.0.0.1 2>/dev/null &&
				[[ $(docker logs "$name" 2>&1 | grep -c 'database system is ready to accept connections') -ge 2 ]]; then
				echo "postgres://$user:$password@$alias:5432/${db:-$user}?sslmode=disable"
				return 0
			fi
			sleep 0.5
		done
		docker logs "$name" >&2 || true
		die "oracle $name did not become ready"
		;;
	down) docker rm -f "$name" >/dev/null 2>&1 || true ;;
	*) die "pg up|down" ;;
	esac
}

# run_box NETWORK CLONE [docker args...] -- CMD...
run_box() {
	local network=$1 s=$2 clone=$3
	shift 3
	local t
	t=$(tmp_dir "$s")
	mkdir -p "$t/gomod" "$t/gocache" "$t/home"
	[[ -d $clone ]] || die "clone '$clone' is not a directory"
	clone=$(cd "$clone" && pwd)
	local extra=()
	while [[ $# -gt 0 && $1 != -- ]]; do extra+=("$1"); shift; done
	[[ ${1:-} == -- ]] || die "missing -- before the command"
	shift
	[[ $# -gt 0 ]] || die "missing command"
	exec docker run --rm -i --init \
		--label "$(label "$s")" \
		--network "$network" \
		--user "$(id -u):$(id -g)" \
		--cap-drop ALL --security-opt no-new-privileges \
		--memory "$MEMORY" --cpus "$CPUS" --pids-limit "$PIDS" \
		--tmpfs /tmp:exec,size=8g \
		-v "$GOROOT_HOST:$GOROOT_HOST:ro" \
		-v "$ROOT:$ROOT:ro" \
		--mount "type=tmpfs,dst=$ROOT/.cswd" \
		--mount "type=tmpfs,dst=$ROOT/.omp" \
		--mount "type=tmpfs,dst=$ROOT/.git" \
		-v "$t:$t" \
		-v "$clone:$clone" \
		-w "$clone" \
		-e HOME="$t/home" \
		-e PATH="$GOROOT_HOST/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" \
		-e GOROOT="$GOROOT_HOST" -e GOTOOLCHAIN=local \
		-e GOMODCACHE="$t/gomod" -e GOCACHE="$t/gocache" \
		-e GOFLAGS="${GOFLAGS_SANDBOX:--mod=readonly}" \
		-e TESTCONTAINERS_RYUK_DISABLED=true \
		"${extra[@]}" \
		"$IMAGE" "$@"
}

cmd_deps() {
	local s=$1 clone=$2
	slug "$s"
	GOFLAGS_SANDBOX=-mod=mod run_box bridge "$s" "$clone" -- sh -c '
		go mod download all || exit 1
		go test -count=1 -exec /bin/true ./... >/dev/null ||
			echo "sandbox: some test packages do not compile; the test runs report them" >&2
		exit 0'
}

cmd_gonet() {
	local s=$1 clone=$2
	shift 2
	slug "$s"
	case ${1:-} in
	mod | get | list | env | version | work) ;;
	*) die "gonet allows go mod, get, list, env, version and work only" ;;
	esac
	GOFLAGS_SANDBOX=-mod=mod run_box bridge "$s" "$clone" -- go "$@"
}

cmd_exec() {
	local s=$1 clone=$2
	shift 2
	slug "$s"
	local network=none envs=()
	while [[ $# -gt 0 && $1 != -- ]]; do
		case $1 in
		-n) network=$2; shift 2 ;;
		-e) envs+=(-e "$2"); shift 2 ;;
		*) die "exec: unknown option '$1'" ;;
		esac
	done
	case $network in
	none) ;;
	internal) cmd_net up "$s"; network=$(net_name "$s") ;;
	*) die "exec: network must be none or internal" ;;
	esac
	run_box "$network" "$s" "$clone" -e GOPROXY=off "${envs[@]}" "$@"
}

cmd_clean() {
	local s=$1
	slug "$s"
	docker ps -aq --filter "label=$(label "$s")" | xargs -r docker rm -f >/dev/null
	cmd_net down "$s"
}

case ${1:-} in
net) shift; cmd_net "$@" ;;
pg) shift; cmd_pg "$@" ;;
deps) shift; cmd_deps "$@" ;;
gonet) shift; cmd_gonet "$@" ;;
exec) shift; cmd_exec "$@" ;;
clean) shift; cmd_clean "$@" ;;
*) sed -n '2,22p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 2 ;;
esac

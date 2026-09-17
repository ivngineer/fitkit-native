#!/bin/sh
# Wipes the server's data directory: the SQLite database and every cached
# image. Accounts, pins, import jobs and analyses all go with it, so this is
# for starting a demo from scratch, not for fixing a bad row.
#
# Usage: ./reset-data.sh [--yes] [--keep-media]
set -e
cd "$(dirname "$0")"

if [ -f .env ]; then
	set -a
	. ./.env
	set +a
fi

data_dir=${FITKIT_DATA_DIR:-./data}
keep_media=0
assume_yes=0
for arg in "$@"; do
	case "$arg" in
	--yes | -y) assume_yes=1 ;;
	--keep-media) keep_media=1 ;;
	*)
		echo "unknown option: $arg" >&2
		echo "usage: $0 [--yes] [--keep-media]" >&2
		exit 2
		;;
	esac
done

if [ ! -d "$data_dir" ]; then
	echo "nothing to purge: $data_dir does not exist"
	exit 0
fi

# A running server holds the database open and would write its cached state
# back over the fresh one, so make it stop first.
port=$(printf '%s' "${FITKIT_ADDR:-:8080}" | sed 's/.*://')
if command -v lsof >/dev/null 2>&1 && lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
	echo "a server is still listening on port $port - stop it first" >&2
	exit 1
fi

if [ "$assume_yes" -ne 1 ]; then
	printf 'Delete every account, pin and cached image under %s? [y/N] ' "$data_dir"
	read -r reply
	case "$reply" in
	y | Y | yes | YES) ;;
	*)
		echo "cancelled"
		exit 1
		;;
	esac
fi

rm -f "$data_dir"/fitkit.db "$data_dir"/fitkit.db-shm "$data_dir"/fitkit.db-wal
[ "$keep_media" -eq 1 ] || rm -rf "$data_dir"/media
echo "purged $data_dir - the next server start creates an empty database"

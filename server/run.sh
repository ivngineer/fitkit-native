#!/bin/sh
# Starts the Fitkit server with settings from .env.
set -e
cd "$(dirname "$0")"
if [ -f .env ]; then
	set -a
	. ./.env
	set +a
fi
exec go run ./cmd/fitkitd

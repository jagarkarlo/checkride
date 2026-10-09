#!/bin/sh
set -eu
directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
printf '%s\n' 'Nostekon opens at http://127.0.0.1:8080 unless --addr overrides it. Stop with Ctrl+C.'
exec "$directory/nostekon-api" "$@"
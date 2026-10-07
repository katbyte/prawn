#!/bin/sh

# the cron refresh: ask the server to sync and rebuild, as its refresh button does, so the page shows it
# running (with its log) and reloads when it ends, and it never runs beside another refresh. When the
# server is not answering, sync and rewrite the page directly instead.

# shellcheck disable=SC1091 # written by entry.sh at container start
. /app/env.sh
cd /data || exit 1

echo
if wget -q -O /dev/null --header "X-Prawn: 1" --header "X-Forwarded-User: cron" --post-data "" "http://127.0.0.1:${PRAWN_PORT:-8765}/refresh"; then
	echo "refresh asked of the server: $(date)"
else
	echo "the server did not answer, refreshing directly: $(date)"
	prawn explore
	echo "refresh finished: $(date)"
fi

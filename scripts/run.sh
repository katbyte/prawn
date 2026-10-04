#!/bin/sh

# the cron refresh: sync what moved since the last run and rewrite the page the server is handing out

# shellcheck disable=SC1091 # written by entry.sh at container start
. /app/env.sh
cd /data || exit 1

echo
echo "refresh started: $(date)"
prawn explore
echo "refresh finished: $(date)"

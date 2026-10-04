#!/bin/sh
set -e

# the image ships a prebuilt /usr/bin/prawn, so this only wires up the cron refresh and runs the server

# cron starts its jobs with a bare environment: keep ours, quoted, for run.sh to source
export -p > /app/env.sh
chmod 600 /app/env.sh

# the refresh job writes to the container log through pid 1, which is prawn once exec'd below.
# the file lives outside /etc/cron.d, which dcron also reads: a crontab there is installed twice
# and the job runs twice at once
echo "${EXPLORE_CRON:-30 */3 * * *} /app/scripts/run.sh >> /proc/1/fd/1 2>&1" > /app/crontab
crontab /app/crontab
/usr/sbin/crond -b

# fetch what moved, write the page, serve it; a fresh volume first walks the whole repo, which takes a while
exec prawn explore --serve "${PORT:-8765}"

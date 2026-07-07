#!/bin/sh
# Fixed-window counter vs sliding-window sorted set, by hand.
set -e
. /scripts/common.sh

say "fixed window: one counter per clock window"
run DEL rl:fixed
run INCR rl:fixed
run INCR rl:fixed
run INCR rl:fixed
run EXPIRE rl:fixed 10
run GET rl:fixed

say "sliding window: timestamps in a sorted set"
run DEL rl:sliding
# BusyBox date has no millisecond format; derive ms from seconds.
NOW=$(($(date +%s) * 1000))
run ZADD rl:sliding "$NOW" "req-1"
run ZADD rl:sliding "$((NOW + 20))" "req-2"
run ZADD rl:sliding "$((NOW + 40))" "req-3"

say "count only requests inside the last 10s (the rolling window)"
run ZREMRANGEBYSCORE rl:sliding 0 "$((NOW - 10000))"
run ZCARD rl:sliding
run ZRANGE rl:sliding 0 -1 WITHSCORES

say "cleanup"
run DEL rl:fixed rl:sliding

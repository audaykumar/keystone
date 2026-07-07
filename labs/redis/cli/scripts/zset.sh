#!/bin/sh
# Sorted sets: leaderboards, ranges, and time-indexed data.
set -e
. /scripts/common.sh

say "leaderboard: score = spend, member = account"
run DEL spenders
run ZADD spenders 1200 acct-1 4500 acct-2 890 acct-3 4500 acct-4
run ZREVRANGE spenders 0 2 WITHSCORES

say "ties (acct-2, acct-4) break lexically by member; rank is deterministic"
run ZREVRANK spenders acct-2
run ZREVRANK spenders acct-4

say "increment a score atomically (a purchase happens)"
run ZINCRBY spenders 400 acct-3
run ZSCORE spenders acct-3

say "range by score: everyone who spent 1000..5000"
run ZRANGEBYSCORE spenders 1000 5000 WITHSCORES

say "time index: score = epoch ms, member = event id (same shape as the sliding-window limiter)"
run DEL recent-logins
run ZADD recent-logins 1700000000000 login-1 1700000060000 login-2 1700000120000 login-3
run ZRANGEBYSCORE recent-logins 1700000050000 +inf

say "cleanup"
run DEL spenders recent-logins

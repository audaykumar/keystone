#!/bin/sh
# Distributed lock: ownership token, TTL, and safe release via Lua.
set -e
. /scripts/common.sh

say "acquire: SET NX EX with a unique owner token"
run DEL lock:acct-1
run SET lock:acct-1 owner-aaa NX EX 10
run SET lock:acct-1 owner-bbb NX EX 10

say "second acquirer got nil: the lock is taken"
run GET lock:acct-1

say "UNSAFE release: blind DEL can free someone else's lock after expiry"
say "SAFE release: compare owner token and delete atomically in Lua"
run EVAL "if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) else return 0 end" 1 lock:acct-1 owner-bbb
run EVAL "if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) else return 0 end" 1 lock:acct-1 owner-aaa

say "wrong owner released nothing (0); right owner released (1)"
run GET lock:acct-1

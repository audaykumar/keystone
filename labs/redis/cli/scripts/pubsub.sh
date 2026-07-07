#!/bin/sh
# Pub/Sub: fire-and-forget fan-out. The return value of PUBLISH is the lesson.
set -e
. /scripts/common.sh

say "publish with no subscribers: return value 0 = nobody heard it, gone forever"
run PUBLISH alerts "transfer-failed acct-1"

say "start a subscriber in the background, give it a moment"
(redis-cli SUBSCRIBE alerts &) >/tmp/sub.log 2>&1
sleep 1

say "publish again: return value counts receivers at this instant"
run PUBLISH alerts "transfer-failed acct-2"

say "there is no replay, no offset, no pending list. A subscriber that"
say "connects late missed everything. Durable delivery needs Streams."

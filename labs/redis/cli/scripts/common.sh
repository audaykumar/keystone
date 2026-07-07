# Shared helper: print each command before running it, so the transcript
# reads as a lesson, not just output.
run() {
  printf '\n> redis-cli %s\n' "$*"
  redis-cli "$@"
}

say() {
  printf '\n## %s\n' "$*"
}

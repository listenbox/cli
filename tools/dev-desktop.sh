#!/bin/sh
set -u

# Watchexec owns the process group and signals every descendant. Keep this
# runner alive until its child exits, so a restart waits for the app to drain.
stopping=false
interrupted=false
trap 'stopping=true; interrupted=true' INT TERM

run_and_wait() {
  "$@" &
  child=$!
  while :; do
    interrupted=false
    wait "$child"
    status=$?
    if ! "$interrupted"; then
      return "$status"
    fi
  done
}

run_and_wait moon run desktop:build
status=$?
if "$stopping"; then exit 0; fi
if [ "$status" -ne 0 ]; then
  # The build task has reported its failure. The dev session remains healthy
  # while waiting for a fix; don't retain this build's exit code when it quits.
  printf 'Desktop build failed (%s); watching for the next edit.\n' "$status" >&2
  exit 0
fi

# This is the debug build's declared Moon output, available on cache hits too.
run_and_wait crates/desktop/dist/listenbox-desktop --config config/dev.yaml
status=$?
if "$stopping"; then exit 0; fi

if [ "$status" -eq 0 ]; then
  # A user-initiated Quit ends the watch session. Restarts and failed builds do not.
  kill -TERM "${LISTENBOX_DEV_WATCHER_PID:?missing dev watcher PID}"
else
  printf 'Desktop exited (%s); watching for the next edit.\n' "$status" >&2
fi
exit 0

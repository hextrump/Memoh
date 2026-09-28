#!/bin/sh
set -eu

prefix='__MEMOH_DISPLAY_PROGRESS__'
progress() {
  percent="$1"
  step="$2"
  shift 2
  message="$*"
  printf '%s{"type":"progress","percent":%s,"step":"%s","message":"%s"}\n' "$prefix" "$percent" "$step" "$message"
}
emit_complete() {
  printf '%s{"type":"complete","percent":100,"step":"complete","message":"Display is ready"}\n' "$prefix"
}
has_cmd() {
  command -v "$1" >/dev/null 2>&1
}
find_xvnc() {
  for candidate in /opt/memoh/toolkit/display/bin/Xvnc /usr/bin/Xvnc /usr/local/bin/Xvnc Xvnc; do
    if echo "$candidate" | grep -q /; then
      [ -x "$candidate" ] && { printf '%s\n' "$candidate"; return 0; }
    elif has_cmd "$candidate"; then
      command -v "$candidate"
      return 0
    fi
  done
  return 1
}
find_browser() {
  [ -x /opt/memoh/toolkit/browser/bin/ant-chrome ] && { printf '%s\n' /opt/memoh/toolkit/browser/bin/ant-chrome; return 0; }
  return 1
}
has_desktop() {
  has_cmd startxfce4 || has_cmd xfce4-session || has_cmd xfwm4 || [ -x /opt/memoh/toolkit/display/bin/twm ]
}
RFB_PORT=5999
XVNC_GEOMETRY="${MEMOH_DISPLAY_GEOMETRY:-1280x960}"
X_SOCKET=/tmp/.X11-unix/X99
X_LOCK=/tmp/.X99-lock
xvnc_pids() {
  for proc_dir in /proc/[0-9]*; do
    [ -d "$proc_dir" ] || continue
    pid="${proc_dir#/proc/}"
    cmdline="$(tr '\000' '\n' <"$proc_dir/cmdline" 2>/dev/null || true)"
    printf '%s\n' "$cmdline" | grep -Eq '(^|/)Xvnc$' || continue
    printf '%s\n' "$cmdline" | grep -Fxq ':99' || continue
    printf '%s\n' "$pid"
  done
  return 0
}
xvnc_running() {
  [ -n "$(xvnc_pids)" ]
}
stop_xvnc() {
  pids="$(xvnc_pids)"
  [ -n "$pids" ] || return 0
  for pid in $pids; do
    kill "$pid" 2>/dev/null || true
  done
  sleep 1
  pids="$(xvnc_pids)"
  for pid in $pids; do
    kill -9 "$pid" 2>/dev/null || true
  done
}
process_pids_by_name() {
  for proc_dir in /proc/[0-9]*; do
    [ -d "$proc_dir" ] || continue
    pid="${proc_dir#/proc/}"
    cmdline="$(tr '\000' '\n' <"$proc_dir/cmdline" 2>/dev/null || true)"
    found=0
    old_ifs="$IFS"
    IFS='
'
    for arg in $cmdline; do
      base="${arg##*/}"
      for target in "$@"; do
        [ "$base" = "$target" ] && found=1
      done
    done
    IFS="$old_ifs"
    [ "$found" = 1 ] && printf '%s\n' "$pid"
  done
  return 0
}
xfce_session_pids() {
  process_pids_by_name startxfce4 xfce4-session xfdesktop
}
xfwm4_pids() {
  process_pids_by_name xfwm4
}
fallback_wm_pids() {
  process_pids_by_name twm
}
stop_fallback_wm() {
  pids="$(fallback_wm_pids)"
  [ -n "$pids" ] || return 0
  for pid in $pids; do
    kill "$pid" 2>/dev/null || true
  done
  sleep 1
  pids="$(fallback_wm_pids)"
  for pid in $pids; do
    kill -9 "$pid" 2>/dev/null || true
  done
}
start_xfwm4() {
  has_cmd xfwm4 || return 1
  [ -n "$(xfwm4_pids)" ] && return 0
  stop_fallback_wm
  nohup xfwm4 --replace >/tmp/memoh-xfwm4.log 2>&1 &
  return 0
}
start_desktop_session() {
  if has_cmd startxfce4; then
    if [ -n "$(xfce_session_pids)" ]; then
      start_xfwm4
      return 0
    fi
    stop_fallback_wm
    nohup startxfce4 >/tmp/memoh-xfce.log 2>&1 &
  elif has_cmd xfce4-session; then
    if [ -n "$(xfce_session_pids)" ]; then
      start_xfwm4
      return 0
    fi
    stop_fallback_wm
    nohup xfce4-session >/tmp/memoh-xfce.log 2>&1 &
  elif has_cmd xfwm4; then
    start_xfwm4
  elif [ -n "$(fallback_wm_pids)" ]; then
    return 0
  elif [ -x /opt/memoh/toolkit/display/bin/twm ]; then
    nohup /opt/memoh/toolkit/display/bin/twm >/tmp/memoh-twm.log 2>&1 &
  fi
}
display_socket_ready() {
  xvnc_running && [ -S "$X_SOCKET" ] && awk -v port="$(printf '%04X' "$RFB_PORT")" 'toupper($2) ~ ":" port "$" && $4 == "0A" { found = 1 } END { exit found ? 0 : 1 }' /proc/net/tcp /proc/net/tcp6 2>/dev/null
}
desktop_style_current() {
  /bin/sh /opt/memoh/scripts/display-apply-style.sh --check >/dev/null 2>&1
}
display_ready() {
  display_socket_ready && find_browser >/dev/null 2>&1 && has_desktop && desktop_style_current
}

prepare_lock=/tmp/memoh-display-prepare.lock
if mkdir "$prepare_lock" 2>/dev/null; then
  trap 'rmdir "$prepare_lock" 2>/dev/null || true' EXIT INT TERM
else
  progress 12 checking "Waiting for another desktop preparation"
  wait_i=0
  while [ -d "$prepare_lock" ] && [ "$wait_i" -lt 180 ]; do
    if display_ready; then
      emit_complete
      exit 0
    fi
    sleep 1
    wait_i=$((wait_i + 1))
  done
  if display_ready; then
    emit_complete
    exit 0
  fi
  echo "Another desktop preparation is still running." >&2
  exit 1
fi

progress 10 checking "Checking display runtime"

XVNC="$(find_xvnc || true)"
BROWSER="$(find_browser || true)"
[ -n "$XVNC" ] || { echo "Workspace image contract violation: Xvnc is unavailable." >&2; exit 1; }
[ -n "$BROWSER" ] || { echo "Workspace image contract violation: ant-chrome is unavailable." >&2; exit 1; }
has_desktop || { echo "Workspace image contract violation: desktop session runtime is unavailable." >&2; exit 1; }
progress 18 checking "Display runtime is available"

export DISPLAY=:99
mkdir -p /run/memoh /tmp/.X11-unix
chmod 1777 /tmp/.X11-unix 2>/dev/null || true

wait_for_socket() {
  path="$1"
  seconds="$2"
  i=0
  while [ "$i" -lt "$seconds" ]; do
    [ -S "$path" ] && return 0
    sleep 1
    i=$((i + 1))
  done
  return 1
}

cleanup_stale_display() {
  xvnc_running && return 0
  rm -f "$X_SOCKET" "$X_LOCK"
}

if ! display_socket_ready; then
  progress 78 starting "Starting VNC display"
  if xvnc_running; then
    wait_for_socket "$X_SOCKET" 10 || true
  fi
  if ! display_socket_ready; then
    stop_xvnc
    cleanup_stale_display
    nohup "$XVNC" :99 -geometry "$XVNC_GEOMETRY" -depth 24 -SecurityTypes None -localhost -rfbport "$RFB_PORT" >/tmp/memoh-xvnc.log 2>&1 &
    wait_i=0
    while [ "$wait_i" -lt 25 ]; do
      display_socket_ready && break
      sleep 1
      wait_i=$((wait_i + 1))
    done
    display_socket_ready || { cat /tmp/memoh-xvnc.log >&2 2>/dev/null || true; exit 1; }
  fi
fi

progress 88 desktop "Starting desktop session"
run_quick() {
  if command -v timeout >/dev/null 2>&1; then
    timeout 5 "$@" >/dev/null 2>&1 || true
  else
    "$@" >/dev/null 2>&1 &
  fi
}
if command -v fc-cache >/dev/null 2>&1; then
  nohup fc-cache -f >/tmp/memoh-fc-cache.log 2>&1 &
fi
if [ -S "$X_SOCKET" ]; then
  if command -v xsetroot >/dev/null 2>&1; then
    run_quick xsetroot -solid "${MEMOH_DISPLAY_DESKTOP_COLOR:-#1f2329}"
    run_quick xsetroot -cursor_name left_ptr
  elif [ -x /opt/memoh/toolkit/display/bin/xsetroot ]; then
    run_quick /opt/memoh/toolkit/display/bin/xsetroot -solid "${MEMOH_DISPLAY_DESKTOP_COLOR:-#1f2329}"
    run_quick /opt/memoh/toolkit/display/bin/xsetroot -cursor_name left_ptr
  fi
fi
start_desktop_session

progress 90 styling "Applying desktop style"
/bin/sh /opt/memoh/scripts/display-apply-style.sh --ensure

progress 94 browser "Launching browser"
/opt/memoh/bridge browser ensure >/tmp/memoh-browser.log 2>&1 || { cat /tmp/memoh-browser.log >&2 2>/dev/null || true; exit 1; }

emit_complete
exit 0

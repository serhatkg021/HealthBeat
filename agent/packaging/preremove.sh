#!/bin/sh
# Paket kaldırılmadan önce (deb: prerm, rpm: %preun). Yükseltmede ("upgrade", rpm'de 1) servise dokunulmaz.
set -e
case "$1" in
  remove | 0)
    if [ -d /run/systemd/system ]; then
      systemctl stop healthbeat-agent.service 2>/dev/null || true
      systemctl disable healthbeat-agent.service 2>/dev/null || true
    fi
    ;;
esac
exit 0

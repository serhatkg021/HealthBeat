#!/bin/sh
# Paket kaldırıldıktan sonra (deb: postrm, rpm: %postun). Yapılandırma yalnızca `apt purge`'de silinir;
# rpm'de "purge" kavramı yoktur, yapılandırma bilerek bırakılır (kimlik bilgisi içerir; elle silinir).
set -e
case "$1" in
  purge)
    rm -rf /etc/healthbeat /etc/systemd/system/healthbeat-agent.service.d
    if getent passwd healthbeat >/dev/null 2>&1; then userdel healthbeat 2>/dev/null || true; fi
    if getent group healthbeat >/dev/null 2>&1; then groupdel healthbeat 2>/dev/null || true; fi
    ;;
esac
if [ -d /run/systemd/system ]; then systemctl daemon-reload || true; fi
exit 0

#!/bin/sh
# Dosyalar açıldıktan sonra çalışır (deb: postinst, rpm: %post).
set -e

# Eski bir tarball/install.sh kurulumundan kalanlar paketle çakışır: /etc/systemd/system'daki unit,
# paketin unit'ini gölgeler ve /usr/local/bin'deki eski binary'yi çalıştırmaya devam eder.
if [ -e /usr/local/bin/healthbeat-agent ] || [ -e /etc/systemd/system/healthbeat-agent.service ]; then
  echo "healthbeat-agent: WARNING: a tarball (install.sh) installation is still present" >&2
  echo "  (/usr/local/bin/healthbeat-agent and/or /etc/systemd/system/healthbeat-agent.service)." >&2
  echo "  It shadows this package. Remove it once the packaged agent works:" >&2
  echo "    sudo systemctl stop healthbeat-agent; sudo rm -f /usr/local/bin/healthbeat-agent* /etc/systemd/system/healthbeat-agent.service; sudo systemctl daemon-reload" >&2
fi

# systemd yoksa (konteyner, chroot) servis işlemleri atlanır.
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
  # Zaten yapılandırılmış ve etkin bir agent'ın yükseltmesinde yeni binary'yi kullanmak için yeniden başlat.
  if [ -f /etc/healthbeat/agent.json ] && systemctl is-enabled --quiet healthbeat-agent.service 2>/dev/null; then
    systemctl restart healthbeat-agent.service || \
      echo "healthbeat-agent: WARNING: could not restart the service after the upgrade; check: journalctl -u healthbeat-agent" >&2
  fi
fi

if [ ! -f /etc/healthbeat/agent.json ]; then
  echo "healthbeat-agent: installed but NOT configured yet. Configure and start it with:" >&2
  echo "    sudo healthbeat-agent-setup" >&2
  echo "  (asks step by step; or pass options, see: healthbeat-agent-setup --help)" >&2
fi
exit 0

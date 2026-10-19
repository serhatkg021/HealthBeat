#!/bin/sh
# Dosyalar açıldıktan sonra çalışır (deb: postinst, rpm: %post).
set -e
# Aşağıdaki karakter sınıfı regex'leri her locale'de düz ASCII anlamına gelmeli (tr_TR'de
# GNU sed, [a-z] içinde 'i'yi reddeder: düz token uyarısı hiç basılmazdı).
export LC_ALL=C

# Eski bir tarball/install.sh kurulumundan kalanlar paketle çakışır: /etc/systemd/system'daki unit,
# paketin unit'ini gölgeler ve /usr/local/bin'deki eski binary'yi çalıştırmaya devam eder.
if [ -e /usr/local/bin/healthbeat-agent ] || [ -e /etc/systemd/system/healthbeat-agent.service ]; then
  echo "healthbeat-agent: UYARI: eski bir tarball (install.sh) kurulumu hâlâ duruyor" >&2
  echo "  (/usr/local/bin/healthbeat-agent ve/veya /etc/systemd/system/healthbeat-agent.service)." >&2
  echo "  Bu paketi gölgeler. Paketle kurulan agent çalışınca kaldırın:" >&2
  echo "    sudo systemctl stop healthbeat-agent; sudo rm -f /usr/local/bin/healthbeat-agent* /etc/systemd/system/healthbeat-agent.service; sudo systemctl daemon-reload" >&2
fi

# systemd yoksa (konteyner, chroot) servis işlemleri atlanır.
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
  # Zaten yapılandırılmış ve etkin bir agent'ın yükseltmesinde yeni binary'yi kullanmak için yeniden başlat.
  if [ -f /etc/healthbeat/agent.json ] && systemctl is-enabled --quiet healthbeat-agent.service 2>/dev/null; then
    systemctl restart healthbeat-agent.service || \
      echo "healthbeat-agent: UYARI: yükseltmeden sonra servis yeniden başlatılamadı; bkz. journalctl -u healthbeat-agent" >&2
  fi
fi

# Eski kurulumlarda token/secret düz durur: agent'ın kendi okuma kuralıyla denetlenir, nasıl şifreleneceği söylenir.
# Config'e dokunulmaz (şifreleme yöneticinin çalıştırdığı komutla yapılır).
if [ -f /etc/healthbeat/agent.json ]; then
  field="$(/usr/bin/healthbeat-agent -config /etc/healthbeat/agent.json -check-config 2>&1 | sed -n 's/.*warning: \([a-z_]*\) is stored in plain text.*/\1/p' | head -n1)"
  if [ -n "$field" ]; then
    echo "healthbeat-agent: UYARI: /etc/healthbeat/agent.json içindeki $field düz metin olarak duruyor." >&2
    echo "  Bu makine için şifreleyin (değer aynı kalır; servis yeniden başlar):" >&2
    echo "    sudo healthbeat-agent-setup encrypt" >&2
  fi
fi

if [ ! -f /etc/healthbeat/agent.json ]; then
  echo "healthbeat-agent: kuruldu ama HENÜZ yapılandırılmadı. Yapılandırıp başlatmak için:" >&2
  echo "    sudo healthbeat-agent-setup" >&2
  echo "  (adım adım sorar; seçeneklerle de verilebilir: healthbeat-agent-setup --help)" >&2
fi
exit 0

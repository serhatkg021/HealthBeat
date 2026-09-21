#!/bin/sh
# Paket kurulmadan önce çalışır (deb: preinst, rpm: %pre). Dosyalar açılmadan ÖNCE olduğu için
# /etc/healthbeat'in "healthbeat" grubuna ait olabilmesi için kullanıcıyı burada oluştururuz.
set -e

getent group healthbeat >/dev/null 2>&1 || groupadd --system healthbeat
getent passwd healthbeat >/dev/null 2>&1 || \
  useradd --system --gid healthbeat --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin healthbeat
exit 0

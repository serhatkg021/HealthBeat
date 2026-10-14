# Hazırlık adımı (docker compose `init` servisi): server başlamadan önce kalıcı dizinleri hazırlar ve çıkar —
# sertifika çiftini üretir ya da var olanı denetleyip sahipliğini düzeltir, log dizinini server kullanıcısına verir
# (bkz. init.sh). Root olarak çalışır; yalnızca kendisine bağlanan dizinlere dokunur.
FROM alpine:3.24
RUN apk add --no-cache openssl
COPY init.sh /usr/local/bin/init.sh
RUN chmod 755 /usr/local/bin/init.sh
ENTRYPOINT ["/usr/local/bin/init.sh"]

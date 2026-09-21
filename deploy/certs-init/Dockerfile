# Tek seferlik sertifika üretici: /certs volume'ünde cert.pem + key.pem yoksa kendinden imzalı bir çift
# yazar (geliştirme / ilk kurulum). Varsa hiçbir şeye dokunmaz — gerçek sertifikanı volume'e koymak yeterli.
FROM alpine:3.21
RUN apk add --no-cache openssl
COPY init-certs.sh /usr/local/bin/init-certs.sh
RUN chmod 755 /usr/local/bin/init-certs.sh
ENTRYPOINT ["/usr/local/bin/init-certs.sh"]

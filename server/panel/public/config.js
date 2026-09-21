// Çalışma zamanı yapılandırması, uygulamadan önce yüklenir (bkz. index.html). Düz bir dosyadır,
// bundle'ın parçası değildir; bu yüzden tek bir build her yere dağıtılabilir: static host'un
// entrypoint'i onu API_BASE_URL ortam değişkeninden yeniden yazar (bkz.
// deploy/docker-entrypoint.d/15-healthbeat-config.sh).
//
// apiBaseUrl: HealthBeat API'sinin origin'i, ör. "https://api.example.com".
// API aynı origin'den sunuluyorsa (dev proxy ya da /api/v1'i server'a yönlendiren bir
// reverse proxy) boş bırakın.
window.HEALTHBEAT_CONFIG = { apiBaseUrl: '' }

package httpapi

import "net/http"

// WithCORS, API'den farklı bir origin'den sunulan tarayıcı panelinin onu çağırmasına izin
// verir. allowed, config.ParseAllowedOrigins'ten gelir. Yalnızca içindeki origin'ler CORS
// başlığı alır ve onlardan gelen OPTIONS preflight'ları router'a ulaşmadan burada yanıtlanır.
// İzin listesi boşsa handler değiştirilmeden döndürülür (aynı origin / reverse proxy
// dağıtımları ve tarayıcı olmayan push/pull agent'ları hiçbir şeye ihtiyaç duymaz).
//
// Access-Control-Allow-Credentials yok: kimlik doğrulama çerez değil Authorization başlığıdır;
// bu yüzden kimlik bilgili cross-origin modu ne gerekli ne de istenir.
func WithCORS(next http.Handler, allowed []string) http.Handler {
	if len(allowed) == 0 {
		return next
	}
	set := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		set[o] = struct{}{}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Yanıt Origin'e bağlıdır; bu yüzden önbellekler onu anahtar almalı — izin verilmeyen
		// origin'ler için de, yoksa önbelleğe alınmış başlıksız bir yanıt izinli birine sunulabilir.
		w.Header().Add("Vary", "Origin")

		if _, ok := set[origin]; !ok {
			next.ServeHTTP(w, r) // CORS başlığı yok: tarayıcı yanıtı engeller
			return
		}

		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Expose-Headers", "Retry-After, X-Total-Count, "+HeaderRequestID)

		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Add("Vary", "Access-Control-Request-Method")
			h.Add("Vary", "Access-Control-Request-Headers")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

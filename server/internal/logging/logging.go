// Package logging, server'ın log/slog kurulumudur: seviye ve biçim (LOG_LEVEL, LOG_FORMAT),
// istek bağlamındaki kimliklerin (request_id, user_id, host_id, ip) her log satırına otomatik
// eklenmesi ve goroutine'lerde panic kurtarma.
//
// Log metinleri, operatörlerin log araçlarında aranabilir kalsın diye İngilizcedir.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
)

// Biçimler: text insan okur (logfmt), json log toplayıcılar (Loki, ELK vb.) içindir.
const (
	FormatText = "text"
	FormatJSON = "json"
)

// ParseLevel, LOG_LEVEL değerini çözer; boş değer info'dur.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("must be one of debug, info, warn, error")
}

// ParseFormat, LOG_FORMAT değerini çözer; boş değer text'tir.
func ParseFormat(s string) (string, error) {
	switch f := strings.ToLower(strings.TrimSpace(s)); f {
	case "":
		return FormatText, nil
	case FormatText, FormatJSON:
		return f, nil
	}
	return "", fmt.Errorf("must be text or json")
}

// New, w'ye yazan ve bağlamdaki istek kimliklerini ekleyen bir logger döndürür.
func New(w io.Writer, level slog.Level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if format == FormatJSON {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(contextHandler{h})
}

// Setup, New'in logger'ını varsayılan yapar. slog.SetDefault standart log paketini de ona
// yönlendirir: standart kütüphanenin kendi logları (ör. net/http'nin TLS el sıkışma hataları) aynı biçimde, INFO
// seviyesinde çıkar.
func Setup(w io.Writer, level slog.Level, format string) {
	slog.SetDefault(New(w, level, format))
}

// RequestInfo, bir isteğin log satırlarına eklenen kimlikleridir. İstek girişinde context'e bir
// kez konur ve sonradan doldurulur: kullanıcı ya da host kimliği, isteği loglayan middleware'in
// içinde çalışan kimlik doğrulamada belli olur; paylaşılan işaretçi sayesinde dıştaki istek logu
// da onu görür. Nil alıcıyla çağrılan metotlar bir şey yapmaz.
type RequestInfo struct {
	ID string

	mu     sync.Mutex
	userID string
	hostID string
	ip     string
}

type requestInfoKey struct{}

// WithRequestInfo, info'yu ctx'e koyar.
func WithRequestInfo(ctx context.Context, info *RequestInfo) context.Context {
	return context.WithValue(ctx, requestInfoKey{}, info)
}

// RequestInfoFrom, ctx'teki RequestInfo'yu döndürür; yoksa nil.
func RequestInfoFrom(ctx context.Context) *RequestInfo {
	info, _ := ctx.Value(requestInfoKey{}).(*RequestInfo)
	return info
}

// RequestID, ctx'teki istek kimliğidir; yoksa boş.
func RequestID(ctx context.Context) string {
	if info := RequestInfoFrom(ctx); info != nil {
		return info.ID
	}
	return ""
}

func (i *RequestInfo) SetUser(id string) { i.set(func() { i.userID = id }) }
func (i *RequestInfo) SetHost(id string) { i.set(func() { i.hostID = id }) }
func (i *RequestInfo) SetIP(ip string)   { i.set(func() { i.ip = ip }) }

// set, nil alıcıda hiçbir şey yapmaz (alan adresi nil kontrolünden önce alınmamalı).
func (i *RequestInfo) set(assign func()) {
	if i == nil {
		return
	}
	i.mu.Lock()
	assign()
	i.mu.Unlock()
}

func (i *RequestInfo) attrs() []slog.Attr {
	i.mu.Lock()
	defer i.mu.Unlock()
	attrs := []slog.Attr{slog.String("request_id", i.ID)}
	if i.userID != "" {
		attrs = append(attrs, slog.String("user_id", i.userID))
	}
	if i.hostID != "" {
		attrs = append(attrs, slog.String("host_id", i.hostID))
	}
	if i.ip != "" {
		attrs = append(attrs, slog.String("ip", i.ip))
	}
	return attrs
}

// contextHandler, kaydı iç handler'a vermeden önce ctx'teki RequestInfo alanlarını ekler. Kayıt aynı anahtarı zaten
// taşıyorsa (ör. alert motoru host_id'yi kendisi yazar) bağlamdaki eklenmez: JSON'da bir anahtar iki kez yazılmaz.
type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if info := RequestInfoFrom(ctx); info != nil {
		present := make(map[string]bool, r.NumAttrs())
		r.Attrs(func(a slog.Attr) bool {
			present[a.Key] = true
			return true
		})
		for _, a := range info.attrs() {
			if !present[a.Key] {
				r.AddAttrs(a)
			}
		}
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}

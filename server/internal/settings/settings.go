// Package settings, panelden değiştirilebilen çalışma zamanı ayarlarını (app_settings) yönetir: açılışta veritabanından
// okur, bellekte tutar (her okuma kilitsizdir) ve değişiklikleri doğrulayıp yazar. Değişiklik yazılınca abonelere
// (rate limiter, log seviyesi…) haber verilir; yeniden başlatma gerekmez.
//
// Server tek kopya çalışır: bir kopyanın yaptığı değişiklik başka kopyalara duyurulmaz.
package settings

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"healthbeat-server/internal/model"
	"healthbeat-server/internal/store"
)

// ErrNothingToChange, ne değer ne de sıfırlanacak alan verilmediğinde döner.
var ErrNothingToChange = errors.New("no settings given")

// Change, bir ayarın değişiklikten önceki ve sonraki değeridir (Values biçiminde).
type Change struct {
	Old any `json:"old"`
	New any `json:"new"`
}

// Changes, gerçekten değişen ayarlardır; denetim kaydına bu yazılır. Boşsa hiçbir şey yazılmamıştır.
type Changes map[Field]Change

// Service, ayarların bellekteki kopyasını tutar ve değişiklikleri yazar.
type Service struct {
	store    *store.Settings
	defaults model.AppSettings

	cur atomic.Pointer[model.AppSettings]

	// mu, yazmaları sıraya koyar: veritabanı yazması, bellekteki kopyanın değişmesi ve abonelerin çağrılması bir
	// değişiklik için tamamlanmadan sonraki başlamaz (abonelere sırayla ulaşır).
	mu   sync.Mutex
	subs []func(old, updated model.AppSettings)
}

// New, ayarları ve varsayılanlarını veritabanından okur.
func New(ctx context.Context, st *store.Settings) (*Service, error) {
	cur, err := st.Get(ctx)
	if err != nil {
		return nil, err
	}
	defaults, err := st.Defaults(ctx)
	if err != nil {
		return nil, err
	}
	s := &Service{store: st, defaults: defaults}
	s.cur.Store(&cur)
	return s, nil
}

// Current, güncel ayarlardır (kopya; kilitsiz).
func (s *Service) Current() model.AppSettings { return *s.cur.Load() }

// Defaults, veritabanındaki varsayılanlardır.
func (s *Service) Defaults() model.AppSettings { return s.defaults }

// Changed, değeri varsayılanından farklı olan ayarlardır (panelin "değiştirildi" işareti).
func (s *Service) Changed() []Field {
	cur, def := Values(s.Current()), Values(s.defaults)
	var out []Field
	for _, f := range Fields {
		if cur[f] != def[f] {
			out = append(out, f)
		}
	}
	return out
}

// OnChange, her başarılı değişiklikten sonra (commit edilmiş olarak) çağrılacak bir abone ekler. Abone kısa sürmeli ve
// Update/Reset çağırmamalıdır (yazma kilidi altında çalışır). Açılışta, yazmalar başlamadan eklenir.
func (s *Service) OnChange(fn func(old, updated model.AppSettings)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs = append(s.subs, fn)
}

// Update, p'de verilen ayarları doğrular ve yazar. Geçersiz bir değer *FieldError döndürür ve hiçbir şey yazılmaz.
// Güncel değeriyle aynı olan alanlar yok sayılır; hiçbir alan değişmiyorsa veritabanına yazılmaz.
func (s *Service) Update(ctx context.Context, actor *uuid.UUID, p Patch) (Changes, error) {
	given := p.values()
	if len(given) == 0 {
		return nil, ErrNothingToChange
	}
	values, err := normalize(given)
	if err != nil {
		return nil, err
	}
	return s.apply(ctx, actor, values, nil)
}

// Reset, verilen ayarları varsayılanlarına döndürür.
func (s *Service) Reset(ctx context.Context, actor *uuid.UUID, fields []Field) (Changes, error) {
	if len(fields) == 0 {
		return nil, ErrNothingToChange
	}
	for _, f := range fields {
		if !slices.Contains(Fields, f) {
			return nil, &FieldError{f, "bilinmeyen ayar"}
		}
	}
	return s.apply(ctx, actor, nil, fields)
}

// apply, set'teki değerleri yazar ve reset'teki alanları varsayılana döndürür.
func (s *Service) apply(ctx context.Context, actor *uuid.UUID, set map[Field]any, reset []Field) (Changes, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, def := Values(s.Current()), Values(s.defaults)
	merged := maps.Clone(cur)
	dbSet := map[string]any{}
	var dbReset []string
	for f, v := range set {
		if v != cur[f] {
			merged[f] = v
			dbSet[string(f)] = dbValue(f, v)
		}
	}
	for _, f := range reset {
		if cur[f] != def[f] {
			merged[f] = def[f]
			dbReset = append(dbReset, string(f))
		}
	}
	if len(dbSet) == 0 && len(dbReset) == 0 {
		return Changes{}, nil
	}
	if err := validateAll(merged); err != nil {
		return nil, err
	}

	old, updated, err := s.store.Apply(ctx, actor, dbSet, dbReset)
	if err != nil {
		return nil, err
	}
	s.cur.Store(&updated)
	for _, fn := range s.subs {
		fn(old, updated)
	}

	before, after := Values(old), Values(updated)
	changes := Changes{}
	for _, f := range Fields {
		if before[f] != after[f] {
			changes[f] = Change{Old: before[f], New: after[f]}
			slog.InfoContext(ctx, "settings: changed", "setting", string(f), "old", before[f], "new", after[f])
		}
	}
	return changes, nil
}

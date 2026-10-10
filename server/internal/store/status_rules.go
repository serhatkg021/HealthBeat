package store

import (
	"context"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"healthbeat-server/internal/model"
)

// Durum kuralları (status_alert_rules): eşiği olmayan alert'lerin seviyesi ve süresi. Kapsam ve yetki eşiklerle
// aynıdır (genel → organizasyon zinciri → sunucu; en özel olan geçerli), bu yüzden Thresholds'tadır. Hiç satır yoksa
// kural kapalıdır.

const statusRuleColumns = `organization_id, host_id, rule, level, duration_seconds, updated_at`

func scanStatusRule(row interface{ Scan(...any) error }) (model.StatusRuleConfig, error) {
	var r model.StatusRuleConfig
	err := row.Scan(&r.OrganizationID, &r.HostID, &r.Rule, &r.Level, &r.DurationSeconds, &r.UpdatedAt)
	return r, err
}

// ListStatusRules, genel ve organizasyon kurallarını döndürür (sunucu kuralları hariç). orgIDs nil ise hepsi
// (super_admin), değilse genel kurallar ve yalnızca bu organizasyonlarınki.
func (s *Thresholds) ListStatusRules(ctx context.Context, orgIDs []uuid.UUID) ([]model.StatusRuleConfig, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+statusRuleColumns+` FROM status_alert_rules
		 WHERE host_id IS NULL AND ($1::uuid[] IS NULL OR organization_id IS NULL OR organization_id = ANY($1))
		 ORDER BY organization_id NULLS FIRST, rule`, orgIDs)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanStatusRule)
}

// SetStatusRules, bir kapsamın (orgID nil = genel) kurallarını değiştirir: ayar yazılır, nil o kapsamdaki satırı siler
// (üst kapsamınkini izler); listede olmayan kurallara dokunulmaz. Organizasyon yoksa ErrOrganizationMissing.
func (s *Thresholds) SetStatusRules(ctx context.Context, orgID *uuid.UUID, changes model.StatusRuleChanges) error {
	return s.setStatusRules(ctx, orgID, nil, changes)
}

// SetHostStatusRules, sunucunun kendi kurallarını değiştirir (SetStatusRules gibi). Sunucu yoksa ErrHostMissing.
func (s *Thresholds) SetHostStatusRules(ctx context.Context, hostID uuid.UUID, changes model.StatusRuleChanges) error {
	return s.setStatusRules(ctx, nil, &hostID, changes)
}

func (s *Thresholds) setStatusRules(ctx context.Context, orgID, hostID *uuid.UUID, changes model.StatusRuleChanges) error {
	rules := make([]string, 0, len(changes))
	for rule := range changes {
		rules = append(rules, rule)
	}
	sort.Strings(rules)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, rule := range rules {
		if err := applyStatusRule(ctx, tx, orgID, hostID, rule, changes[rule]); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func applyStatusRule(ctx context.Context, tx pgx.Tx, orgID, hostID *uuid.UUID, rule string, setting *model.StatusRuleSetting) error {
	if setting == nil {
		_, err := tx.Exec(ctx,
			`DELETE FROM status_alert_rules
			 WHERE organization_id IS NOT DISTINCT FROM $1 AND host_id IS NOT DISTINCT FROM $2 AND rule = $3`, orgID, hostID, rule)
		return err
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO status_alert_rules (organization_id, host_id, rule, level, duration_seconds)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT ON CONSTRAINT status_alert_rules_key
		 DO UPDATE SET level = EXCLUDED.level, duration_seconds = EXCLUDED.duration_seconds, updated_at = now()`,
		orgID, hostID, rule, setting.Level, setting.DurationSeconds)
	if err != nil {
		switch pgErrorCode(err) {
		case pgForeignKeyViolation:
			if hostID != nil {
				return ErrHostMissing
			}
			return ErrOrganizationMissing
		case pgCheckViolation:
			return ErrStatusRuleInvalid
		}
		return err
	}
	return nil
}

// HostStatusRules, sunucunun kendi (özel) kurallarıdır.
func (s *Thresholds) HostStatusRules(ctx context.Context, hostID uuid.UUID) (model.StatusRuleSet, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+statusRuleColumns+` FROM status_alert_rules WHERE host_id = $1`, hostID)
	if err != nil {
		return nil, err
	}
	all, err := collect(rows, scanStatusRule)
	if err != nil {
		return nil, err
	}
	out := model.StatusRuleSet{}
	for _, r := range all {
		out[r.Rule] = r.StatusRuleSetting
	}
	return out, nil
}

// StatusRuleDefaultsFor, kendi kuralı olmayan bir orgID sunucusunun alacağı kurallardır: organizasyon zincirindeki en
// yakın kural, yoksa genel olan. İkisi de olmayan kural yoktur (kapalı).
func (s *Thresholds) StatusRuleDefaultsFor(ctx context.Context, orgID uuid.UUID) (model.StatusRuleSet, error) {
	rows, err := s.pool.Query(ctx,
		orgChainCTE(1)+`
		 SELECT DISTINCT ON (r.rule) r.rule, r.level, r.duration_seconds
		 FROM status_alert_rules r LEFT JOIN chain c ON c.id = r.organization_id
		 WHERE r.host_id IS NULL AND (r.organization_id IS NULL OR c.id IS NOT NULL)
		 ORDER BY r.rule, (r.organization_id IS NULL), c.depth`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := model.StatusRuleSet{}
	for rows.Next() {
		var rule string
		var st model.StatusRuleSetting
		if err := rows.Scan(&rule, &st.Level, &st.DurationSeconds); err != nil {
			return nil, err
		}
		out[rule] = st
	}
	return out, rows.Err()
}

// ResolveStatusRules, sunucuya uygulanan kurallardır: sunucunun kendi kuralı, yoksa organizasyon zincirindeki en yakın,
// yoksa genel. Alert motoru rapor başına bir kez çağırır.
func (s *Thresholds) ResolveStatusRules(ctx context.Context, hostID, orgID uuid.UUID) (model.StatusRuleSet, error) {
	rows, err := s.pool.Query(ctx,
		orgChainCTE(1)+`
		 SELECT DISTINCT ON (r.rule) r.rule, r.level, r.duration_seconds
		 FROM status_alert_rules r LEFT JOIN chain c ON c.id = r.organization_id
		 WHERE r.host_id = $2 OR (r.host_id IS NULL AND (r.organization_id IS NULL OR c.id IS NOT NULL))
		 ORDER BY r.rule, (r.host_id IS NULL), (r.organization_id IS NULL), c.depth`, orgID, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := model.StatusRuleSet{}
	for rows.Next() {
		var rule string
		var st model.StatusRuleSetting
		if err := rows.Scan(&rule, &st.Level, &st.DurationSeconds); err != nil {
			return nil, err
		}
		out[rule] = st
	}
	return out, rows.Err()
}

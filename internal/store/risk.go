package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type MerchantService struct {
	ID                         string `db:"id" json:"id"`
	MerchantAccountID          string `db:"merchant_account_id" json:"merchant_account_id"`
	ServiceCode                string `db:"service_code" json:"service_code"`
	Name                       string `db:"name" json:"name"`
	Description                string `db:"description" json:"description"`
	InterfaceSet               JSON   `db:"interface_set" json:"interface_set"`
	Status                     string `db:"status" json:"status"`
	MaxTransactionMicrocredits int64  `db:"max_transaction_microcredits" json:"max_transaction_microcredits"`
	DailyLimitMicrocredits     int64  `db:"daily_limit_microcredits" json:"daily_limit_microcredits"`
	CreatedAt                  string `db:"created_at" json:"created_at"`
	UpdatedAt                  string `db:"updated_at" json:"updated_at"`
}

type RiskDecision struct {
	ID                string `db:"id" json:"id"`
	MerchantAccountID string `db:"merchant_account_id" json:"merchant_account_id"`
	ServiceID         string `db:"service_id" json:"service_id"`
	ProviderReference string `db:"provider_reference" json:"-"`
	Decision          string `db:"decision" json:"decision"`
	KYCStatus         string `db:"kyc_status" json:"kyc_status"`
	KYBStatus         string `db:"kyb_status" json:"kyb_status"`
	SanctionsStatus   string `db:"sanctions_status" json:"sanctions_status"`
	AnomalyScore      int    `db:"anomaly_score" json:"anomaly_score"`
	Reason            string `db:"reason" json:"reason"`
	CreatedAt         string `db:"created_at" json:"created_at"`
}

// AuthorizeMerchantServiceCreation performs the ownership check before the
// service calls the external risk provider. CreateMerchantService repeats this
// check inside its serializable transaction: this preflight prevents an
// unauthorized caller from causing an external side effect, while the
// transactional check protects the database from a time-of-check/time-of-use
// race.
func (s *Store) AuthorizeMerchantServiceCreation(ctx context.Context, userID, merchantAccountID string) error {
	var owned int
	if err := s.db.GetContext(ctx, &owned, `SELECT COUNT(*) FROM merchant_accounts WHERE account_id=? AND owner_user_id=? AND merchant_status IN ('pending','approved')`, merchantAccountID, userID); err != nil {
		return err
	}
	if owned == 0 {
		return ErrNotFound
	}
	return nil
}

// LookupMerchantServiceCommand resolves completed idempotent work before the
// service contacts the external risk provider. The final Create method repeats
// the same lookup transactionally to close the concurrent/ambiguous window.
func (s *Store) LookupMerchantServiceCommand(ctx context.Context, userID, merchantAccountID, idempotencyKey string, payloadHash []byte) (MerchantService, RiskDecision, bool, error) {
	if err := s.AuthorizeMerchantServiceCreation(ctx, userID, merchantAccountID); err != nil {
		return MerchantService{}, RiskDecision{}, false, err
	}
	var service MerchantService
	var storedHash []byte
	err := s.db.GetContext(ctx, &service, `SELECT id,merchant_account_id,service_code,name,description,interface_set,status,max_transaction_microcredits,daily_limit_microcredits,created_at,updated_at FROM merchant_services WHERE merchant_account_id=? AND idempotency_key=?`, merchantAccountID, idempotencyKey)
	if errors.Is(err, sql.ErrNoRows) {
		return MerchantService{}, RiskDecision{}, false, nil
	}
	if err != nil {
		return MerchantService{}, RiskDecision{}, false, err
	}
	if err := s.db.GetContext(ctx, &storedHash, `SELECT payload_hash FROM merchant_services WHERE id=?`, service.ID); err != nil {
		return MerchantService{}, RiskDecision{}, false, err
	}
	if !bytes.Equal(storedHash, payloadHash) {
		return MerchantService{}, RiskDecision{}, false, ErrIdempotencyConflict
	}
	var decision RiskDecision
	if err := s.db.GetContext(ctx, &decision, `SELECT id,merchant_account_id,service_id,provider_reference,decision,kyc_status,kyb_status,sanctions_status,anomaly_score,reason,created_at FROM risk_decisions WHERE service_id=? ORDER BY created_at DESC LIMIT 1`, service.ID); err != nil {
		return MerchantService{}, RiskDecision{}, false, err
	}
	return service, decision, true, nil
}

func (s *Store) CreateMerchantService(ctx context.Context, userID, idempotencyKey string, payloadHash []byte, service MerchantService, risk RiskDecision) (MerchantService, RiskDecision, bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return service, risk, false, err
	}
	defer tx.Rollback()
	var existing struct {
		ID   string `db:"id"`
		Hash []byte `db:"payload_hash"`
	}
	err = tx.GetContext(ctx, &existing, `SELECT id,payload_hash FROM merchant_services WHERE merchant_account_id=? AND idempotency_key=?`, service.MerchantAccountID, idempotencyKey)
	if err == nil {
		if !bytes.Equal(existing.Hash, payloadHash) {
			return service, risk, false, ErrIdempotencyConflict
		}
		if err := tx.GetContext(ctx, &service, `SELECT id,merchant_account_id,service_code,name,description,interface_set,status,max_transaction_microcredits,daily_limit_microcredits,created_at,updated_at FROM merchant_services WHERE id=?`, existing.ID); err != nil {
			return service, risk, false, err
		}
		if err := tx.GetContext(ctx, &risk, `SELECT id,merchant_account_id,service_id,provider_reference,decision,kyc_status,kyb_status,sanctions_status,anomaly_score,reason,created_at FROM risk_decisions WHERE service_id=? ORDER BY created_at DESC LIMIT 1`, existing.ID); err != nil {
			return service, risk, false, err
		}
		return service, risk, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return service, risk, false, err
	}
	var owned int
	if err := tx.GetContext(ctx, &owned, `SELECT COUNT(*) FROM merchant_accounts WHERE account_id=? AND owner_user_id=? AND merchant_status IN ('pending','approved')`, service.MerchantAccountID, userID); err != nil {
		return service, risk, false, err
	}
	if owned == 0 {
		return service, risk, false, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO merchant_services(id,merchant_account_id,service_code,name,description,interface_set,status,max_transaction_microcredits,daily_limit_microcredits,idempotency_key,payload_hash,created_at,updated_at) VALUES (?,?,?,?,?,?,'pending',?,?,?,?,?,?)`, service.ID, service.MerchantAccountID, service.ServiceCode, service.Name, service.Description, string(service.InterfaceSet), service.MaxTransactionMicrocredits, service.DailyLimitMicrocredits, idempotencyKey, payloadHash, service.CreatedAt, service.UpdatedAt); err != nil {
		return service, risk, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO risk_decisions(id,merchant_account_id,service_id,provider_reference,decision,kyc_status,kyb_status,sanctions_status,anomaly_score,reason,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, risk.ID, risk.MerchantAccountID, risk.ServiceID, risk.ProviderReference, risk.Decision, risk.KYCStatus, risk.KYBStatus, risk.SanctionsStatus, risk.AnomalyScore, risk.Reason, risk.CreatedAt); err != nil {
		return service, risk, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor_type,actor_id,action,resource_type,resource_id,reason,request_id,metadata,created_at) VALUES (?,'user',?,'merchant_service.screened','merchant_service',?,?,?,?,?)`, uuid.NewString(), userID, service.ID, risk.Reason, auditRequestID(ctx), `{"risk_decision":"`+risk.Decision+`"}`, service.CreatedAt); err != nil {
		return service, risk, false, err
	}
	if err := tx.Commit(); err != nil {
		return service, risk, false, err
	}
	return service, risk, false, nil
}

func (s *Store) ListMerchantServices(ctx context.Context, userID, merchantAccountID string) ([]MerchantService, error) {
	var owned int
	if err := s.db.GetContext(ctx, &owned, `SELECT COUNT(*) FROM merchant_accounts WHERE account_id=? AND owner_user_id=?`, merchantAccountID, userID); err != nil {
		return nil, err
	}
	if owned == 0 {
		return nil, ErrNotFound
	}
	var services []MerchantService
	err := s.db.SelectContext(ctx, &services, `SELECT id,merchant_account_id,service_code,name,description,interface_set,status,max_transaction_microcredits,daily_limit_microcredits,created_at,updated_at FROM merchant_services WHERE merchant_account_id=? ORDER BY created_at,id`, merchantAccountID)
	return services, err
}

func (s *Store) DecideMerchantService(ctx context.Context, actorID, serviceID, decision, reason, at string) (MerchantService, error) {
	status := map[string]string{"approve": "approved", "reject": "rejected", "suspend": "suspended", "reactivate": "approved"}[decision]
	if status == "" {
		return MerchantService{}, errors.New("invalid merchant service decision")
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return MerchantService{}, err
	}
	defer tx.Rollback()
	if status == "approved" {
		var riskDecision, sanctions string
		err := scanCommandRow(ctx, tx.QueryRowxContext(ctx, `SELECT decision,sanctions_status FROM risk_decisions WHERE service_id=? ORDER BY created_at DESC LIMIT 1`, serviceID), &riskDecision, &sanctions)
		if errors.Is(err, sql.ErrNoRows) || riskDecision != "allow" || sanctions != "clear" {
			return MerchantService{}, ErrRiskDenied
		}
		if err != nil {
			return MerchantService{}, err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE merchant_services SET status=?,updated_at=? WHERE id=?`, status, at, serviceID)
	if err != nil {
		return MerchantService{}, err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return MerchantService{}, ErrNotFound
	}
	if err := insertAudit(ctx, tx, actorID, "merchant_service.decision", "merchant_service", serviceID, reason, at); err != nil {
		return MerchantService{}, err
	}
	var service MerchantService
	if err := tx.GetContext(ctx, &service, `SELECT id,merchant_account_id,service_code,name,description,interface_set,status,max_transaction_microcredits,daily_limit_microcredits,created_at,updated_at FROM merchant_services WHERE id=?`, serviceID); err != nil {
		return MerchantService{}, fmt.Errorf("read merchant service: %w", err)
	}
	return service, tx.Commit()
}

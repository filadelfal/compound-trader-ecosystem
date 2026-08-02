package main

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	errTradingDisabled  = errors.New("trading is disabled by the account kill switch")
	errMaxOpenOrders    = errors.New("maximum open orders reached")
	errMaxGrossExposure = errors.New("maximum gross exposure would be exceeded")
	errMaxDailyLoss     = errors.New("daily realized loss limit reached")
)

type RiskProfile struct {
	UserID           string    `json:"userId"`
	TradingEnabled   bool      `json:"tradingEnabled"`
	MaxOpenOrders    int       `json:"maxOpenOrders"`
	MaxGrossExposure string    `json:"maxGrossExposure"`
	MaxDailyLoss     string    `json:"maxDailyLoss"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type RiskStatus struct {
	Profile       RiskProfile `json:"profile"`
	OpenOrders    int         `json:"openOrders"`
	GrossExposure string      `json:"grossExposure"`
	RealizedToday string      `json:"realizedToday"`
}

type UpdateRiskProfileInput struct {
	TradingEnabled   *bool
	MaxOpenOrders    *int
	MaxGrossExposure *string
	MaxDailyLoss     *string
}

type RiskProfileRepository interface {
	GetRiskStatus(context.Context, string) (RiskStatus, error)
	UpdateRiskProfile(context.Context, string, UpdateRiskProfileInput) (RiskStatus, error)
}

type RiskProfileService struct{ repository RiskProfileRepository }

func NewRiskProfileService(repository RiskProfileRepository) *RiskProfileService {
	return &RiskProfileService{repository: repository}
}

func (service *RiskProfileService) Get(ctx context.Context, userID string) (RiskStatus, error) {
	userID = strings.ToLower(strings.TrimSpace(userID))
	if !uuidPattern.MatchString(userID) {
		return RiskStatus{}, errors.New("user id must be a canonical UUID")
	}
	return service.repository.GetRiskStatus(ctx, userID)
}

func (service *RiskProfileService) Update(ctx context.Context, userID string, input UpdateRiskProfileInput) (RiskStatus, error) {
	userID = strings.ToLower(strings.TrimSpace(userID))
	if !uuidPattern.MatchString(userID) {
		return RiskStatus{}, errors.New("user id must be a canonical UUID")
	}
	if input.MaxOpenOrders != nil && (*input.MaxOpenOrders < 1 || *input.MaxOpenOrders > 100) {
		return RiskStatus{}, errors.New("maxOpenOrders must be between 1 and 100")
	}
	if err := validateRiskDecimal("maxGrossExposure", input.MaxGrossExposure, "10000000"); err != nil {
		return RiskStatus{}, err
	}
	if err := validateRiskDecimal("maxDailyLoss", input.MaxDailyLoss, "1000000"); err != nil {
		return RiskStatus{}, err
	}
	return service.repository.UpdateRiskProfile(ctx, userID, input)
}

func validateRiskDecimal(name string, value *string, ceiling string) error {
	if value == nil {
		return nil
	}
	*value = strings.TrimSpace(*value)
	if !positiveDecimal(*value) {
		return errors.New(name + " must be a positive decimal")
	}
	parsed, _ := decimalRat(*value)
	maximum, _ := decimalRat(ceiling)
	if parsed.Cmp(maximum) > 0 {
		return errors.New(name + " exceeds the platform maximum")
	}
	return nil
}

type riskRowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (repository *PostgresOrderRepository) GetRiskStatus(ctx context.Context, userID string) (RiskStatus, error) {
	if _, err := repository.db.Exec(ctx, `INSERT INTO trading_risk_profiles(user_id) VALUES($1::uuid) ON CONFLICT DO NOTHING`, userID); err != nil {
		return RiskStatus{}, err
	}
	return scanRiskStatus(ctx, repository.db, userID)
}

func scanRiskStatus(ctx context.Context, query riskRowQuerier, userID string) (RiskStatus, error) {
	var status RiskStatus
	err := query.QueryRow(ctx, `
		SELECT profile.user_id::text, profile.trading_enabled, profile.max_open_orders,
		       profile.max_gross_exposure::text, profile.max_daily_loss::text, profile.updated_at,
		       (SELECT COUNT(*) FROM trading_orders WHERE user_id=profile.user_id AND status IN ('pending','partially_filled'))::int,
		       (
		         COALESCE((SELECT SUM(ABS(position_cost)) FROM (
		           SELECT SUM(cost_delta) AS position_cost FROM portfolio_ledger_entries
		           WHERE user_id=profile.user_id AND leg_type='security' GROUP BY asset
		         ) positions),0)
		         + COALESCE((SELECT SUM(remaining_amount) FROM trading_order_reservations
		           WHERE user_id=profile.user_id AND reservation_type='cash' AND status='active'),0)
		       )::text,
		       COALESCE((SELECT SUM(realized_pnl) FROM portfolio_ledger_entries
		         WHERE user_id=profile.user_id AND leg_type='security'
		           AND created_at >= date_trunc('day', NOW() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),0)::text
		FROM trading_risk_profiles profile WHERE profile.user_id=$1::uuid
	`, userID).Scan(&status.Profile.UserID, &status.Profile.TradingEnabled,
		&status.Profile.MaxOpenOrders, &status.Profile.MaxGrossExposure,
		&status.Profile.MaxDailyLoss, &status.Profile.UpdatedAt,
		&status.OpenOrders, &status.GrossExposure, &status.RealizedToday)
	return status, err
}

func (repository *PostgresOrderRepository) UpdateRiskProfile(ctx context.Context, userID string, input UpdateRiskProfileInput) (RiskStatus, error) {
	tx, err := repository.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RiskStatus{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, userID); err != nil {
		return RiskStatus{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO trading_risk_profiles(user_id) VALUES($1::uuid) ON CONFLICT DO NOTHING`, userID); err != nil {
		return RiskStatus{}, err
	}
	_, err = tx.Exec(ctx, `
		UPDATE trading_risk_profiles SET
		  trading_enabled=COALESCE($2,trading_enabled), max_open_orders=COALESCE($3,max_open_orders),
		  max_gross_exposure=COALESCE($4::numeric,max_gross_exposure),
		  max_daily_loss=COALESCE($5::numeric,max_daily_loss), updated_at=NOW()
		WHERE user_id=$1::uuid
	`, userID, input.TradingEnabled, input.MaxOpenOrders, input.MaxGrossExposure, input.MaxDailyLoss)
	if err != nil {
		return RiskStatus{}, err
	}
	if input.TradingEnabled != nil && !*input.TradingEnabled {
		if _, err = tx.Exec(ctx, `UPDATE trading_orders SET status='canceled',canceled_at=NOW(),updated_at=NOW() WHERE user_id=$1::uuid AND status IN ('pending','partially_filled')`, userID); err != nil {
			return RiskStatus{}, err
		}
	}
	status, err := scanRiskStatus(ctx, tx, userID)
	if err != nil {
		return RiskStatus{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return RiskStatus{}, err
	}
	return status, nil
}

func (repository *PostgresOrderRepository) enforceAccountRisk(ctx context.Context, tx pgx.Tx, input CreateOrderInput) error {
	if _, err := tx.Exec(ctx, `INSERT INTO trading_risk_profiles(user_id) VALUES($1::uuid) ON CONFLICT DO NOTHING`, input.UserID); err != nil {
		return err
	}
	status, err := scanRiskStatus(ctx, tx, input.UserID)
	if err != nil {
		return err
	}
	if !status.Profile.TradingEnabled {
		return errTradingDisabled
	}
	if status.OpenOrders >= status.Profile.MaxOpenOrders {
		return errMaxOpenOrders
	}
	if negatedAtLeast(status.RealizedToday, status.Profile.MaxDailyLoss) {
		return errMaxDailyLoss
	}
	if input.ReservationType == "cash" {
		gross, _ := signedOrZeroDecimalRat(status.GrossExposure)
		newExposure, _ := decimalRat(input.ReservationAmount)
		limit, _ := decimalRat(status.Profile.MaxGrossExposure)
		if new(big.Rat).Add(gross, newExposure).Cmp(limit) > 0 {
			return errMaxGrossExposure
		}
	}
	return nil
}

func negatedAtLeast(value, limit string) bool {
	current, _ := signedOrZeroDecimalRat(value)
	threshold, _ := decimalRat(limit)
	return current.Sign() < 0 && new(big.Rat).Neg(current).Cmp(threshold) >= 0
}

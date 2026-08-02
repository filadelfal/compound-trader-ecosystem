package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var errPaperAccountNotFound = errors.New("paper account not activated")

type PaperAccount struct {
	ID           string    `json:"id"`
	UserID       string    `json:"userId"`
	Currency     string    `json:"currency"`
	StartingCash string    `json:"startingCash"`
	Status       string    `json:"status"`
	ActivatedAt  time.Time `json:"activatedAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type PaperAccountResult struct {
	Account   PaperAccount `json:"account"`
	Portfolio Portfolio    `json:"portfolio"`
	Created   bool         `json:"created"`
}

type PaperAccountRepository interface {
	ActivatePaperAccount(context.Context, string, string) (PaperAccount, bool, error)
	GetPaperAccount(context.Context, string) (PaperAccount, error)
}

type PaperAccountService struct {
	repository   PaperAccountRepository
	portfolios   OrderRepository
	startingCash string
}

func NewPaperAccountService(repository PaperAccountRepository, portfolios OrderRepository, startingCash string) (*PaperAccountService, error) {
	startingCash = strings.TrimSpace(startingCash)
	if !positiveDecimal(startingCash) {
		return nil, errors.New("paper starting cash must be a positive decimal")
	}
	return &PaperAccountService{repository: repository, portfolios: portfolios, startingCash: startingCash}, nil
}

func (service *PaperAccountService) Activate(ctx context.Context, userID string) (PaperAccountResult, error) {
	userID = strings.ToLower(strings.TrimSpace(userID))
	if !uuidPattern.MatchString(userID) {
		return PaperAccountResult{}, errors.New("user id must be a canonical UUID")
	}
	account, created, err := service.repository.ActivatePaperAccount(ctx, userID, service.startingCash)
	if err != nil {
		return PaperAccountResult{}, err
	}
	portfolio, err := service.portfolios.GetPortfolio(ctx, userID)
	if err != nil {
		return PaperAccountResult{}, err
	}
	return PaperAccountResult{Account: account, Portfolio: portfolio, Created: created}, nil
}

func (service *PaperAccountService) Get(ctx context.Context, userID string) (PaperAccountResult, error) {
	userID = strings.ToLower(strings.TrimSpace(userID))
	if !uuidPattern.MatchString(userID) {
		return PaperAccountResult{}, errors.New("user id must be a canonical UUID")
	}
	account, err := service.repository.GetPaperAccount(ctx, userID)
	if err != nil {
		return PaperAccountResult{}, err
	}
	portfolio, err := service.portfolios.GetPortfolio(ctx, userID)
	if err != nil {
		return PaperAccountResult{}, err
	}
	return PaperAccountResult{Account: account, Portfolio: portfolio, Created: false}, nil
}

func (repository *PostgresOrderRepository) ActivatePaperAccount(ctx context.Context, userID, startingCash string) (PaperAccount, bool, error) {
	tx, err := repository.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return PaperAccount{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, userID); err != nil {
		return PaperAccount{}, false, err
	}
	account, err := scanPaperAccount(tx.QueryRow(ctx, `SELECT id::text,user_id::text,currency,starting_cash::text,status,activated_at,updated_at FROM trading_paper_accounts WHERE user_id=$1::uuid`, userID))
	if err == nil {
		if err = tx.Commit(ctx); err != nil {
			return PaperAccount{}, false, err
		}
		return account, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PaperAccount{}, false, err
	}
	account, err = scanPaperAccount(tx.QueryRow(ctx, `INSERT INTO trading_paper_accounts(user_id,starting_cash) VALUES($1::uuid,$2::numeric) RETURNING id::text,user_id::text,currency,starting_cash::text,status,activated_at,updated_at`, userID, startingCash))
	if err != nil {
		return PaperAccount{}, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO trading_risk_profiles(user_id) VALUES($1::uuid) ON CONFLICT DO NOTHING`, userID); err != nil {
		return PaperAccount{}, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO portfolio_ledger_entries(user_id,source_type,source_id,leg_type,asset,quantity_delta,value_delta,description) VALUES ($1::uuid,'paper_account',$2,'cash','USD',$3::numeric,$3::numeric,'Initial paper trading capital'),($1::uuid,'paper_account',$2,'external','USD',-$3::numeric,-$3::numeric,'Initial paper trading capital')`, userID, account.ID, startingCash); err != nil {
		return PaperAccount{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PaperAccount{}, false, err
	}
	return account, true, nil
}

func (repository *PostgresOrderRepository) GetPaperAccount(ctx context.Context, userID string) (PaperAccount, error) {
	account, err := scanPaperAccount(repository.db.QueryRow(ctx, `SELECT id::text,user_id::text,currency,starting_cash::text,status,activated_at,updated_at FROM trading_paper_accounts WHERE user_id=$1::uuid`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return PaperAccount{}, errPaperAccountNotFound
	}
	return account, err
}

type paperAccountScanner interface{ Scan(...any) error }

func scanPaperAccount(scanner paperAccountScanner) (PaperAccount, error) {
	var account PaperAccount
	err := scanner.Scan(&account.ID, &account.UserID, &account.Currency, &account.StartingCash, &account.Status, &account.ActivatedAt, &account.UpdatedAt)
	return account, err
}

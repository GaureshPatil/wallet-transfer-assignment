package domain

import (
	"fmt"
	"math"
	"time"
)

const maxWalletIDLen = 64

// Wallet holds a balance in minor units (e.g. paise or cents).
type Wallet struct {
	ID             string
	Balance        int64
	OpeningBalance int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NewWallet validates and builds a wallet opened with openingBalance.
func NewWallet(id string, openingBalance int64, now time.Time) (*Wallet, error) {
	if id == "" || len(id) > maxWalletIDLen {
		return nil, fmt.Errorf("%w: wallet id must be 1-%d characters", ErrInvalidRequest, maxWalletIDLen)
	}
	if openingBalance < 0 {
		return nil, fmt.Errorf("%w: opening balance must not be negative", ErrInvalidRequest)
	}
	return &Wallet{ID: id, Balance: openingBalance, OpeningBalance: openingBalance, CreatedAt: now, UpdatedAt: now}, nil
}

// Debit removes amount from the balance. It never lets the balance go negative.
func (w *Wallet) Debit(amount int64) error {
	if amount <= 0 {
		return fmt.Errorf("%w: amount must be positive", ErrInvalidRequest)
	}
	if w.Balance < amount {
		return ErrInsufficientFunds
	}
	w.Balance -= amount
	return nil
}

// Credit adds amount to the balance, refusing to overflow int64.
func (w *Wallet) Credit(amount int64) error {
	if amount <= 0 {
		return fmt.Errorf("%w: amount must be positive", ErrInvalidRequest)
	}
	if w.Balance > math.MaxInt64-amount {
		return ErrBalanceOverflow
	}
	w.Balance += amount
	return nil
}

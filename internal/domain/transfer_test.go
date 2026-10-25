package domain_test

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GaureshPatil/wallet-transfer-assignment/internal/domain"
)

var now = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

func TestNewTransfer_Validation(t *testing.T) {
	tests := []struct {
		name          string
		key, from, to string
		amount        int64
		wantInvalid   bool
	}{
		{"valid", "k1", "a", "b", 100, false},
		{"empty key", "", "a", "b", 100, true},
		{"empty source", "k1", "", "b", 100, true},
		{"empty destination", "k1", "a", "", 100, true},
		{"same wallet", "k1", "a", "a", 100, true},
		{"zero amount", "k1", "a", "b", 0, true},
		{"negative amount", "k1", "a", "b", -5, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domain.NewTransfer(uuid.New(), tc.key, tc.from, tc.to, tc.amount, now)
			if tc.wantInvalid {
				require.ErrorIs(t, err, domain.ErrInvalidRequest)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, domain.TransferPending, got.Status)
		})
	}
}

func TestTransfer_StateMachine(t *testing.T) {
	newPending := func(t *testing.T) *domain.Transfer {
		tr, err := domain.NewTransfer(uuid.New(), "k", "a", "b", 10, now)
		require.NoError(t, err)
		return tr
	}

	t.Run("pending to processed", func(t *testing.T) {
		tr := newPending(t)
		require.NoError(t, tr.MarkProcessed(now))
		assert.Equal(t, domain.TransferProcessed, tr.Status)
	})

	t.Run("pending to failed records the reason", func(t *testing.T) {
		tr := newPending(t)
		require.NoError(t, tr.MarkFailed(domain.FailureInsufficientFunds, now))
		assert.Equal(t, domain.TransferFailed, tr.Status)
		assert.Equal(t, domain.FailureInsufficientFunds, tr.FailureReason)
	})

	t.Run("terminal states cannot change again", func(t *testing.T) {
		processed := newPending(t)
		require.NoError(t, processed.MarkProcessed(now))
		require.ErrorIs(t, processed.MarkProcessed(now), domain.ErrInvalidTransition)
		require.ErrorIs(t, processed.MarkFailed(domain.FailureWalletNotFound, now), domain.ErrInvalidTransition)

		failed := newPending(t)
		require.NoError(t, failed.MarkFailed(domain.FailureWalletNotFound, now))
		require.ErrorIs(t, failed.MarkProcessed(now), domain.ErrInvalidTransition)
		require.ErrorIs(t, failed.MarkFailed(domain.FailureInsufficientFunds, now), domain.ErrInvalidTransition)
		assert.Equal(t, domain.FailureWalletNotFound, failed.FailureReason, "failure reason must not be overwritten")
	})
}

func TestWallet_DebitCredit(t *testing.T) {
	w, err := domain.NewWallet("a", 100, now)
	require.NoError(t, err)

	require.NoError(t, w.Debit(40))
	assert.EqualValues(t, 60, w.Balance)

	require.ErrorIs(t, w.Debit(61), domain.ErrInsufficientFunds)
	assert.EqualValues(t, 60, w.Balance, "failed debit must not change the balance")

	require.NoError(t, w.Debit(60), "spending the exact balance is allowed")
	assert.EqualValues(t, 0, w.Balance)

	require.NoError(t, w.Credit(25))
	assert.EqualValues(t, 25, w.Balance)

	require.ErrorIs(t, w.Debit(0), domain.ErrInvalidRequest)
	require.ErrorIs(t, w.Credit(-1), domain.ErrInvalidRequest)
}

func TestWallet_CreditOverflow(t *testing.T) {
	w, err := domain.NewWallet("a", math.MaxInt64-5, now)
	require.NoError(t, err)
	require.ErrorIs(t, w.Credit(6), domain.ErrBalanceOverflow)
	assert.EqualValues(t, math.MaxInt64-5, w.Balance)
	require.NoError(t, w.Credit(5))
}

func TestNewWallet_Validation(t *testing.T) {
	_, err := domain.NewWallet("", 0, now)
	require.ErrorIs(t, err, domain.ErrInvalidRequest)
	_, err = domain.NewWallet("a", -1, now)
	require.ErrorIs(t, err, domain.ErrInvalidRequest)
}

func TestLedgerEntriesFor_IsBalancedDoubleEntry(t *testing.T) {
	tr, err := domain.NewTransfer(uuid.New(), "k", "a", "b", 100, now)
	require.NoError(t, err)

	entries := domain.LedgerEntriesFor(tr, now)

	require.Len(t, entries, 2)
	assert.Equal(t, domain.EntryDebit, entries[0].Type)
	assert.Equal(t, "a", entries[0].WalletID)
	assert.Equal(t, domain.EntryCredit, entries[1].Type)
	assert.Equal(t, "b", entries[1].WalletID)
	assert.Equal(t, entries[0].Amount, entries[1].Amount)
	assert.Equal(t, tr.ID, entries[0].TransferID)
	assert.Equal(t, tr.ID, entries[1].TransferID)
}

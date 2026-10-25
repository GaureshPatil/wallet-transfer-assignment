package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GaureshPatil/wallet-transfer-assignment/internal/handler"
)

// The API is exercised over real HTTP against real PostgreSQL: this is the
// "exactly-once semantics at the API level" contract end to end.

type apiResponse struct {
	Status int
	Header http.Header
	Body   map[string]any
}

func newAPI(t *testing.T) (*env, *httptest.Server) {
	t.Helper()
	e := newEnv(t)
	srv := httptest.NewServer(handler.New(e.transfers, e.wallets, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(srv.Close)
	return e, srv
}

func call(t *testing.T, method, url, body string) apiResponse {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	out := apiResponse{Status: resp.StatusCode, Header: resp.Header}
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &out.Body), string(raw))
	return out
}

func transferJSON(key, from, to string, amount int) string {
	return fmt.Sprintf(`{"idempotencyKey":%q,"fromWalletId":%q,"toWalletId":%q,"amount":%d}`, key, from, to, amount)
}

func TestAPI_DuplicateRequestReturnsOriginalResultWithoutDuplicateTransfer(t *testing.T) {
	e, srv := newAPI(t)
	e.wallet(t, "wallet_1", 500)
	e.wallet(t, "wallet_2", 0)
	body := transferJSON("abc123", "wallet_1", "wallet_2", 100)

	first := call(t, http.MethodPost, srv.URL+"/transfers", body)
	second := call(t, http.MethodPost, srv.URL+"/transfers", body)

	assert.Equal(t, http.StatusCreated, first.Status)
	assert.Equal(t, http.StatusOK, second.Status)
	assert.Equal(t, "true", second.Header.Get(handler.ReplayedHeader))
	assert.Equal(t, first.Body["id"], second.Body["id"])
	assert.Equal(t, "PROCESSED", second.Body["status"])
	assert.EqualValues(t, 400, e.balance(t, "wallet_1"))
	assert.EqualValues(t, 100, e.balance(t, "wallet_2"))
	assert.Equal(t, 1, e.count(t, "transfers"))
	assert.Equal(t, 2, e.count(t, "ledger_entries"))
}

func TestAPI_FailureIsReplayedWithTheSameStatus(t *testing.T) {
	e, srv := newAPI(t)
	e.wallet(t, "a", 10)
	e.wallet(t, "b", 0)
	body := transferJSON("k", "a", "b", 100)

	first := call(t, http.MethodPost, srv.URL+"/transfers", body)
	second := call(t, http.MethodPost, srv.URL+"/transfers", body)

	assert.Equal(t, http.StatusUnprocessableEntity, first.Status)
	assert.Equal(t, http.StatusUnprocessableEntity, second.Status)
	assert.Equal(t, "FAILED", second.Body["status"])
	assert.Equal(t, first.Body["id"], second.Body["id"])
	assert.EqualValues(t, 10, e.balance(t, "a"))
}

func TestAPI_ReusingAKeyWithADifferentPayloadIsRejected(t *testing.T) {
	e, srv := newAPI(t)
	e.wallet(t, "a", 500)
	e.wallet(t, "b", 0)
	call(t, http.MethodPost, srv.URL+"/transfers", transferJSON("k", "a", "b", 100))

	resp := call(t, http.MethodPost, srv.URL+"/transfers", transferJSON("k", "a", "b", 200))

	assert.Equal(t, http.StatusUnprocessableEntity, resp.Status)
	assert.EqualValues(t, 400, e.balance(t, "a"))
}

func TestAPI_ConcurrentDuplicatesExecuteOnce(t *testing.T) {
	e, srv := newAPI(t)
	e.wallet(t, "a", 1000)
	e.wallet(t, "b", 0)
	body := transferJSON("same", "a", "b", 100)

	const n = 20
	statuses := make([]int, n)
	runConcurrently(t, n, time.Minute, func(i int) {
		resp, err := http.Post(srv.URL+"/transfers", "application/json", bytes.NewBufferString(body))
		if assert.NoError(t, err) {
			statuses[i] = resp.StatusCode
			_ = resp.Body.Close()
		}
	})

	created := 0
	for _, s := range statuses {
		if s == http.StatusCreated {
			created++
		} else {
			assert.Equal(t, http.StatusOK, s)
		}
	}
	assert.Equal(t, 1, created, "exactly one request executed the transfer")
	assert.EqualValues(t, 900, e.balance(t, "a"))
	assert.Equal(t, 1, e.count(t, "transfers"))
	e.assertLedgerReconciles(t)
}

func TestAPI_WalletsAndTransferLookup(t *testing.T) {
	_, srv := newAPI(t)
	assert.Equal(t, http.StatusCreated, call(t, http.MethodPost, srv.URL+"/wallets", `{"id":"a","balance":300}`).Status)
	assert.Equal(t, http.StatusCreated, call(t, http.MethodPost, srv.URL+"/wallets", `{"id":"b","balance":0}`).Status)
	assert.Equal(t, http.StatusConflict, call(t, http.MethodPost, srv.URL+"/wallets", `{"id":"a","balance":1}`).Status)

	created := call(t, http.MethodPost, srv.URL+"/transfers", transferJSON("k", "a", "b", 120))
	require.Equal(t, http.StatusCreated, created.Status)

	wallet := call(t, http.MethodGet, srv.URL+"/wallets/a", "")
	assert.EqualValues(t, 180, wallet.Body["balance"])

	got := call(t, http.MethodGet, srv.URL+"/transfers/"+created.Body["id"].(string), "")
	require.Equal(t, http.StatusOK, got.Status)
	entries, ok := got.Body["ledgerEntries"].([]any)
	require.True(t, ok)
	assert.Len(t, entries, 2)

	assert.Equal(t, http.StatusNotFound, call(t, http.MethodPost, srv.URL+"/transfers", transferJSON("k2", "a", "ghost", 1)).Status)
}

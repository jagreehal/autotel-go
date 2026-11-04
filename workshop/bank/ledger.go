package bank

import (
	"fmt"
	"sync/atomic"
)

var transactionCounter atomic.Uint32

// Receipt is what a successful debit returns.
type Receipt struct {
	TransactionID string `json:"transactionId"`
	BalancePence  int64  `json:"balancePence"`
}

// GetAccount returns the account that belongs to a customer, as it stands right
// now. It panics when there is none: every user has an account, so a miss is a
// bug and not a refusal.
func GetAccount(userID string) Account {
	Sleep(10)
	accountsMu.Lock()
	defer accountsMu.Unlock()
	for _, account := range accounts {
		if account.UserID == userID {
			return account
		}
	}
	panic("no account for user " + userID)
}

// Debit takes money out of an account.
//
// The legacy-core shard answers correctly and slowly. Nothing in this file says
// so out loud, and nothing in the ATM's code knows the shard exists. That is the
// ordinary situation: the field that explains your latency is a property of the
// data, not of the request.
func Debit(account Account, amountPence int64) (Receipt, error) {
	if account.Shard == "legacy-core" {
		Sleep(320)
	} else {
		Sleep(40)
	}

	accountsMu.Lock()
	defer accountsMu.Unlock()

	var stored *Account
	for index := range accounts {
		if accounts[index].ID == account.ID {
			stored = &accounts[index]
		}
	}

	if amountPence > stored.BalancePence {
		return Receipt{}, &Error{InsufficientFunds, "Insufficient funds", map[string]any{
			"account.id":              stored.ID,
			"withdrawal.amount_pence": amountPence,
		}}
	}
	if stored.WithdrawnTodayPence+amountPence > stored.DailyLimitPence {
		return Receipt{}, &Error{DailyLimitExceeded, "Daily limit exceeded", map[string]any{
			"account.id":                    stored.ID,
			"account.daily_limit_pence":     stored.DailyLimitPence,
			"account.withdrawn_today_pence": stored.WithdrawnTodayPence,
		}}
	}

	stored.BalancePence -= amountPence
	stored.WithdrawnTodayPence += amountPence
	return Receipt{
		TransactionID: fmt.Sprintf("txn_%06d", transactionCounter.Add(1)),
		BalancePence:  stored.BalancePence,
	}, nil
}

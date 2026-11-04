// Package bank is the workshop's domain: users, cards, accounts and a ledger,
// in memory.
//
// Nothing here uses autotel. The domain is the constant across all thirteen
// parts and the observability is the variable. Every part instruments these
// same four operations from the outside, so when a part looks different from
// the one before it, the difference is the lesson and not a change of subject.
package bank

import (
	"fmt"
	"math"
	"slices"
	"sync"
	"time"
)

// User is a customer.
type User struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CardNumber string `json:"cardNumber"`
	PinHash    string `json:"pinHash"`
}

// Account is a customer's account as it stands.
type Account struct {
	ID                  string `json:"id"`
	UserID              string `json:"userId"`
	BalancePence        int64  `json:"balancePence"`
	DailyLimitPence     int64  `json:"dailyLimitPence"`
	WithdrawnTodayPence int64  `json:"withdrawnTodayPence"`
	// Shard is the ledger cluster that holds this account. Accounts migrated
	// from the 1998 core sit on legacy-core, which answers the same questions
	// correctly and roughly eight times slower. Two accounts of twenty-five sit
	// on it.
	//
	// Part 10 is about finding this field without being told it exists.
	Shard string `json:"shard"`
}

// Not a hash. A workshop. Never do this.
func pinHashOf(pin string) string { return "pin:" + pin }

var names = []string{
	"Ada Okafor", "Bruno Salas", "Chen Wei", "Dara Nolan", "Emeka Bello",
	"Farah Haddad", "Greta Lind", "Hugo Marchetti", "Ines Duarte", "Jonas Vetter",
	"Kira Blomqvist", "Luca Ferrari", "Maya Sundaram", "Nils Aasen", "Omar Riaz",
	"Petra Novak", "Quentin Roy", "Rosa Iglesias", "Sami Ahmed", "Tove Karlsen",
	"Ugo Bianchi", "Vera Pavlova", "Wes Duncan", "Xenia Adamou", "Yusuf Demir",
}

var users = func() []User {
	out := make([]User, len(names))
	for index, name := range names {
		out[index] = User{
			ID:         fmt.Sprintf("u_%03d", index+1),
			Name:       name,
			CardNumber: fmt.Sprintf("4000%d", 1000+index),
			PinHash:    pinHashOf("1234"),
		}
	}
	return out
}()

// Users returns the twenty-five customers, the same on every run.
func Users() []User { return slices.Clone(users) }

// openingAccounts uses a tiny linear congruential generator, so the fixtures
// are the same on every machine on every run. A workshop where your numbers
// differ from the numbers in the README teaches you to distrust the README.
func openingAccounts() []Account {
	state := uint32(20_260_904)
	random := func() float64 {
		state = state*1_664_525 + 1_013_904_223
		return float64(state) / 4_294_967_296.0
	}

	out := make([]Account, len(users))
	for index, user := range users {
		shard := "core"
		// Accounts 7 and 19 never made it off the old cluster.
		if index == 6 || index == 18 {
			shard = "legacy-core"
		}
		out[index] = Account{
			ID:              fmt.Sprintf("acc_%03d", index+1),
			UserID:          user.ID,
			BalancePence:    5_000 + int64(math.Floor(random()*120_000)),
			DailyLimitPence: 30_000,
			Shard:           shard,
		}
	}
	return out
}

var (
	accountsMu sync.Mutex
	accounts   = openingAccounts()
)

// ResetBalances undoes any debits an earlier run in this process left behind.
func ResetBalances() {
	accountsMu.Lock()
	defer accountsMu.Unlock()
	accounts = openingAccounts()
}

// Sleep waits, the way a call to another system does.
func Sleep(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }

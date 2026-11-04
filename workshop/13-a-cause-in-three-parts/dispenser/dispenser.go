// Package dispenser is the note dispenser driver, which ships with the ATM
// software.
//
// Do not read this file until you have found the cause from the data. It is here
// for the same reason bank/ledger.go was in part 10: the code that explains your
// latency is usually somewhere you were not looking.
package dispenser

import "github.com/jagreehal/autotel-go/v2/workshop/bank"

// Machine is one ATM.
type Machine struct{ ID, Model, Branch, SoftwareVersion string }

// Dispense counts out the notes. Build 4.2.0 replaced the note-mix calculation,
// and on Diebold hardware a mixed-denomination amount (£50 and over) now
// recalibrates the cassettes before every dispense. Each condition alone is
// harmless.
func Dispense(machine Machine, amountPence int64) {
	recalibrates := machine.SoftwareVersion == "4.2.0" &&
		machine.Model == "Diebold-522" &&
		amountPence >= 5000

	if recalibrates {
		bank.Sleep(600)
	} else {
		bank.Sleep(30)
	}
}

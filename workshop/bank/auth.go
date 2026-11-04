package bank

import "sync"

var (
	attemptsMu     sync.Mutex
	failedAttempts = map[string]int{}
)

// FindUserByCard looks a customer up by the card they pushed into the slot.
func FindUserByCard(cardNumber string) (User, error) {
	Sleep(15)
	for _, user := range users {
		if user.CardNumber == cardNumber {
			return user, nil
		}
	}
	last4 := cardNumber[max(0, len(cardNumber)-4):]
	return User{}, &Error{CardNotRecognised, "Card not recognised", map[string]any{"card.last4": last4}}
}

// VerifyPin keeps the card after three wrong PINs. The counter is per card and
// survives between withdrawals, which is why a customer can be locked out by
// something that happened ten minutes ago in a different request.
func VerifyPin(cardNumber, pin string) (User, error) {
	Sleep(5)
	attemptsMu.Lock()
	defer attemptsMu.Unlock()

	attempts := failedAttempts[cardNumber]
	if attempts >= 3 {
		return User{}, &Error{CardRetained, "Card retained", map[string]any{"auth.attempts": attempts}}
	}

	for _, user := range users {
		if user.CardNumber == cardNumber && user.PinHash == pinHashOf(pin) {
			delete(failedAttempts, cardNumber)
			return user, nil
		}
	}

	next := attempts + 1
	failedAttempts[cardNumber] = next
	code, message := PinIncorrect, "PIN incorrect"
	if next >= 3 {
		code, message = CardRetained, "Card retained"
	}
	return User{}, &Error{code, message, map[string]any{
		"auth.attempts":           next,
		"auth.attempts_remaining": max(0, 3-next),
	}}
}

// ResetAuthAttempts forgets every failed PIN.
func ResetAuthAttempts() {
	attemptsMu.Lock()
	defer attemptsMu.Unlock()
	clear(failedAttempts)
}

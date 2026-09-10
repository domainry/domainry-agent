// Package execution contains protocol-independent execution rules. Run
// identity, host authorization, durable reservations and recovery stay with
// each owning application and repository.
package execution

import "errors"

const DefaultMaxToolCalls = 20

var (
	ErrCallLimit = errors.New("tool call budget exceeded")
	ErrCostLimit = errors.New("tool cost budget exceeded")
)

type Budget struct{ Calls, Cost int }
type Usage struct{ Calls, Cost int }

// Reserve is pure: callers commit the returned usage under their own fence
// and transaction, or reconstruct it from an immutable execution history.
// Cost=0 is the explicit unmetered-cost mode used by personal conversations.
// It accepts only a zero cost charge; it never silently disables a charge.
func (b Budget) Reserve(used, charge Usage) (Usage, error) {
	if b.Calls < 1 || used.Calls < 0 || charge.Calls < 1 || used.Calls > b.Calls || charge.Calls > b.Calls-used.Calls {
		return used, ErrCallLimit
	}
	if b.Cost < 0 || used.Cost < 0 || charge.Cost < 0 || used.Cost > b.Cost || charge.Cost > b.Cost-used.Cost {
		return used, ErrCostLimit
	}
	return Usage{Calls: used.Calls + charge.Calls, Cost: used.Cost + charge.Cost}, nil
}

// AddCost rejects malformed historic usage and integer overflow before it
// can make the next reservation appear cheaper than its actual total.
func AddCost(used, charge int) (int, error) {
	const maxInt = int(^uint(0) >> 1)
	if used < 0 || charge < 0 || used > maxInt-charge {
		return 0, ErrCostLimit
	}
	return used + charge, nil
}

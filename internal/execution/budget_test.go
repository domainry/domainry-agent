package execution

import "testing"

func TestReservationsRejectOverflowAndLeaveUsageUnchanged(t *testing.T) {
	const maxInt = int(^uint(0) >> 1)
	for _, sample := range []struct {
		name         string
		budget       Budget
		used, charge Usage
		want         error
	}{
		{"last available call", Budget{Calls: 2, Cost: 5}, Usage{Calls: 1, Cost: 3}, Usage{Calls: 1, Cost: 2}, nil},
		{"call limit", Budget{Calls: 2, Cost: 5}, Usage{Calls: 2, Cost: 3}, Usage{Calls: 1, Cost: 1}, ErrCallLimit},
		{"cost limit", Budget{Calls: 3, Cost: 5}, Usage{Calls: 1, Cost: 4}, Usage{Calls: 1, Cost: 2}, ErrCostLimit},
		{"call overflow", Budget{Calls: maxInt}, Usage{Calls: maxInt}, Usage{Calls: 1}, ErrCallLimit},
		{"cost overflow", Budget{Calls: 3, Cost: maxInt}, Usage{Calls: 1, Cost: maxInt - 1}, Usage{Calls: 1, Cost: 2}, ErrCostLimit},
		{"negative recorded cost", Budget{Calls: 3, Cost: 5}, Usage{Calls: 1, Cost: -1}, Usage{Calls: 1, Cost: 2}, ErrCostLimit},
		{"negative charge", Budget{Calls: 3, Cost: 5}, Usage{}, Usage{Calls: 1, Cost: -1}, ErrCostLimit},
		{"conversation counts only", Budget{Calls: 3}, Usage{Calls: 1}, Usage{Calls: 1}, nil},
		{"unconfigured costs reject charges", Budget{Calls: 3}, Usage{}, Usage{Calls: 1, Cost: 1}, ErrCostLimit},
	} {
		t.Run(sample.name, func(t *testing.T) {
			next, err := sample.budget.Reserve(sample.used, sample.charge)
			if err != sample.want {
				t.Fatal(err)
			}
			if err != nil && next != sample.used {
				t.Fatal("failed reservation changed usage")
			}
		})
	}
	if _, err := AddCost(maxInt, 1); err != ErrCostLimit {
		t.Fatal("cost sum overflow accepted")
	}
	if _, err := AddCost(2, -1); err != ErrCostLimit {
		t.Fatal("negative historical charge accepted")
	}
}

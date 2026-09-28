// Package verify turns the evidence gathered during a restore drill into a
// verdict: which verification levels passed, how long each recovery phase took
// and exactly how much acknowledged data was lost.
package verify

import (
	"sort"
	"time"
)

// Ack is a write the application saw acknowledged before the failure.
type Ack struct {
	WriteID string    `json:"writeId"`
	AckedAt time.Time `json:"ackedAt"`
}

// RPOMeasurement mirrors checkride.ledger.RpoReport; tests/contracts keeps the
// two implementations in agreement.
type RPOMeasurement struct {
	FailureAt     time.Time
	Acknowledged  int
	Recovered     int
	Lost          int
	Holes         int
	Unexpected    int
	RecoveryPoint *time.Time
	FirstLostAt   *time.Time
	RPO           time.Duration
	// recovered[i] reports whether expected[i] was found; used for the ledger chart.
	expected  []Ack
	recovered []bool
}

// Consistent reports whether the restore is a clean point in time: no lost
// write is older than a recovered one.
func (m RPOMeasurement) Consistent() bool { return m.Holes == 0 }

// Resolution is the gap between the last recovered and the first lost write;
// the true RPO lies within it.
func (m RPOMeasurement) Resolution() *time.Duration {
	if m.RecoveryPoint == nil || m.FirstLostAt == nil {
		return nil
	}
	gap := m.FirstLostAt.Sub(*m.RecoveryPoint)
	return &gap
}

// MeasureRPO compares acknowledged writes with the write IDs found in the
// restored database. Only writes acknowledged at or before failureAt count.
func MeasureRPO(acks []Ack, present []string, failureAt time.Time) RPOMeasurement {
	expected := make([]Ack, 0, len(acks))
	for _, ack := range acks {
		if !ack.AckedAt.After(failureAt) {
			expected = append(expected, ack)
		}
	}
	sort.Slice(expected, func(i, j int) bool {
		if !expected[i].AckedAt.Equal(expected[j].AckedAt) {
			return expected[i].AckedAt.Before(expected[j].AckedAt)
		}
		return expected[i].WriteID < expected[j].WriteID
	})

	found := make(map[string]struct{}, len(present))
	for _, id := range present {
		found[id] = struct{}{}
	}
	recovered := make([]bool, len(expected))
	prefix, lastRecovered, firstLost := len(expected), -1, -1
	lost := 0
	for index, ack := range expected {
		_, recovered[index] = found[ack.WriteID]
		if recovered[index] {
			lastRecovered = index
			continue
		}
		lost++
		if firstLost < 0 {
			firstLost, prefix = index, index
		}
	}
	holes := 0
	for index, ok := range recovered {
		if !ok && index < lastRecovered {
			holes++
		}
	}

	expectedIDs := make(map[string]struct{}, len(expected))
	for _, ack := range expected {
		expectedIDs[ack.WriteID] = struct{}{}
	}
	unexpected := 0
	for id := range found {
		if _, ok := expectedIDs[id]; !ok {
			unexpected++
		}
	}

	measurement := RPOMeasurement{
		FailureAt:    failureAt,
		Acknowledged: len(expected),
		Recovered:    len(expected) - lost,
		Lost:         lost,
		Holes:        holes,
		Unexpected:   unexpected,
		expected:     expected,
		recovered:    recovered,
	}
	if prefix > 0 {
		point := expected[prefix-1].AckedAt
		measurement.RecoveryPoint = &point
	}
	if firstLost >= 0 {
		first := expected[firstLost].AckedAt
		measurement.FirstLostAt = &first
	}
	switch {
	case lost == 0:
		measurement.RPO = 0
	case measurement.RecoveryPoint != nil:
		measurement.RPO = failureAt.Sub(*measurement.RecoveryPoint)
	default:
		measurement.RPO = failureAt.Sub(expected[0].AckedAt)
	}
	return measurement
}

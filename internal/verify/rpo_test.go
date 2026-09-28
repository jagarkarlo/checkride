package verify

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

type contractCase struct {
	Name      string      `json:"name"`
	Acks      [][2]string `json:"acks"`
	Present   []string    `json:"present"`
	FailureAt time.Time   `json:"failureAt"`
	Expected  struct {
		Acknowledged  int        `json:"acknowledged"`
		Recovered     int        `json:"recovered"`
		Lost          int        `json:"lost"`
		Holes         int        `json:"holes"`
		Unexpected    int        `json:"unexpected"`
		RecoveryPoint *time.Time `json:"recoveryPoint"`
		FirstLostAt   *time.Time `json:"firstLostAt"`
		RPOSeconds    float64    `json:"rpoSeconds"`
	} `json:"expected"`
}

func TestMeasureRPOMatchesSharedContract(t *testing.T) {
	data, err := os.ReadFile("../../tests/contracts/rpo_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct{ Cases []contractCase }
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) == 0 {
		t.Fatal("no contract cases")
	}
	for _, tc := range file.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			acks := make([]Ack, 0, len(tc.Acks))
			for _, pair := range tc.Acks {
				at, err := time.Parse(time.RFC3339Nano, pair[1])
				if err != nil {
					t.Fatal(err)
				}
				acks = append(acks, Ack{WriteID: pair[0], AckedAt: at})
			}
			got := MeasureRPO(acks, tc.Present, tc.FailureAt)
			want := tc.Expected
			if got.Acknowledged != want.Acknowledged || got.Recovered != want.Recovered || got.Lost != want.Lost ||
				got.Holes != want.Holes || got.Unexpected != want.Unexpected {
				t.Fatalf("counts = %d/%d/%d/%d/%d, want %d/%d/%d/%d/%d", got.Acknowledged, got.Recovered, got.Lost,
					got.Holes, got.Unexpected, want.Acknowledged, want.Recovered, want.Lost, want.Holes, want.Unexpected)
			}
			if !sameTime(got.RecoveryPoint, want.RecoveryPoint) || !sameTime(got.FirstLostAt, want.FirstLostAt) {
				t.Fatalf("recoveryPoint/firstLostAt = %v/%v, want %v/%v", got.RecoveryPoint, got.FirstLostAt, want.RecoveryPoint, want.FirstLostAt)
			}
			if got.RPO != time.Duration(want.RPOSeconds*float64(time.Second)) {
				t.Fatalf("rpo = %v, want %vs", got.RPO, want.RPOSeconds)
			}
		})
	}
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

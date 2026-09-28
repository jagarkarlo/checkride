package verify

import (
	"fmt"
	"strconv"
	"strings"
)

// Level is a verification depth, V0 to V4.
type Level int

const (
	V0 Level = iota
	V1
	V2
	V3
	V4
)

func (l Level) String() string { return "V" + strconv.Itoa(int(l)) }

type LevelInfo struct {
	Question string
	Evidence string
}

// Levels matches checkride.levels.LEVELS.
var Levels = [...]LevelInfo{
	V0: {"Did the backup report success?", "Backup tool status"},
	V1: {"Did the restore report success?", "Restore tool status"},
	V2: {"Is the workload healthy?", "Pods ready, HTTP and TCP checks"},
	V3: {"Is the data structurally intact?", "Tables, row counts, checksums"},
	V4: {"Is the data correct?", "Business invariants, acknowledged-write ledger, cross-store consistency"},
}

// ParseLevel accepts V3, v3 or 3.
func ParseLevel(value string) (Level, error) {
	text := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(value)), "V")
	number, err := strconv.Atoi(text)
	if err != nil || number < int(V0) || number > int(V4) || strconv.Itoa(number) != text {
		return 0, fmt.Errorf("unknown verification level %q; expected V0 to V4", value)
	}
	return Level(number), nil
}

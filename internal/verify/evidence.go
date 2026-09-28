package verify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	maxPhases          = 64
	maxChecks          = 256
	maxWrites          = 200_000
	maxDurationSeconds = int64((1<<63 - 1) / int64(time.Second))
)

var (
	compactDuration = regexp.MustCompile(`^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$`)
	dnsLabel        = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

// Evidence is a DrillRun document: what an orchestrator (or an operator
// running a drill by hand) observed during one restore drill.
type Evidence struct {
	APIVersion string      `json:"apiVersion"`
	Kind       string      `json:"kind"`
	Metadata   RunMetadata `json:"metadata"`
	Spec       RunSpec     `json:"spec"`
	Status     RunStatus   `json:"status"`

	upTo           Level
	rto, rpo       time.Duration
	hasRTO, hasRPO bool
}

type RunMetadata struct {
	Name  string `json:"name"`
	Drill string `json:"drill"`
}

type RunSpec struct {
	Scenario   string          `json:"scenario"`
	UpTo       json.RawMessage `json:"upTo"`
	Objectives *Objectives     `json:"objectives"`
	V4Evidence *V4Evidence     `json:"v4Evidence"`
}

type V4Evidence struct {
	Invariants []string `json:"invariants"`
}

type Objectives struct {
	RTO json.RawMessage `json:"rto"`
	RPO json.RawMessage `json:"rpo"`
}

type RunStatus struct {
	FailureAt   time.Time       `json:"failureAt"`
	CompletedAt time.Time       `json:"completedAt"`
	Phases      []Phase         `json:"phases"`
	Checks      []Check         `json:"checks"`
	Ledger      *LedgerEvidence `json:"ledger"`
}

type Phase struct {
	Name      string    `json:"name"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt"`
}

type Check struct {
	Level  json.RawMessage `json:"level"`
	Name   string          `json:"name"`
	Passed *bool           `json:"passed"`
	Detail string          `json:"detail"`
	level  Level
}

type LedgerEvidence struct {
	Acks    []Ack    `json:"acks"`
	Present []string `json:"present"`
}

// ValidationError lists every problem found in an evidence document.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string { return strings.Join(e.Problems, "; ") }

// DecodeError means the document is not well-formed DrillRun JSON: a syntax
// error, a wrong field type or an unknown field.
type DecodeError struct{ Err error }

func (e *DecodeError) Error() string { return "invalid JSON: " + e.Err.Error() }

// ParseEvidence strictly decodes and validates a DrillRun document.
func ParseEvidence(data []byte) (*Evidence, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var evidence Evidence
	if err := decoder.Decode(&evidence); err != nil {
		return nil, &DecodeError{Err: err}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, &DecodeError{Err: errors.New("document must contain exactly one JSON value")}
	}
	if problems := evidence.validate(); len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}
	return &evidence, nil
}

func (e *Evidence) validate() []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if e.APIVersion != "checkride/v1alpha1" {
		add("apiVersion: must be checkride/v1alpha1")
	}
	if e.Kind != "DrillRun" {
		add("kind: must be DrillRun")
	}
	if !validLabel(e.Metadata.Name) {
		add("metadata.name: must be a DNS label of at most 63 characters")
	}
	if e.Metadata.Drill != "" && !validLabel(e.Metadata.Drill) {
		add("metadata.drill: must be a DNS label of at most 63 characters")
	}

	e.upTo = V4
	if len(e.Spec.UpTo) > 0 {
		level, err := parseLevelJSON(e.Spec.UpTo)
		if err != nil {
			add("spec.upTo: %v", err)
		}
		e.upTo = level
	}
	if objectives := e.Spec.Objectives; objectives != nil {
		var err error
		if e.rto, e.hasRTO, err = parseObjective(objectives.RTO); err != nil {
			add("spec.objectives.rto: %v", err)
		}
		if e.rpo, e.hasRPO, err = parseObjective(objectives.RPO); err != nil {
			add("spec.objectives.rpo: %v", err)
		}
	}

	status := &e.Status
	if status.FailureAt.IsZero() {
		add("status.failureAt: field required")
	}
	if status.CompletedAt.IsZero() {
		add("status.completedAt: field required")
	} else if !status.FailureAt.IsZero() && status.CompletedAt.Before(status.FailureAt) {
		add("status.completedAt: must not be before failureAt")
	}
	if len(status.Phases) > maxPhases {
		add("status.phases: at most %d phases", maxPhases)
	}
	for index, phase := range status.Phases {
		path := fmt.Sprintf("status.phases.%d", index)
		if strings.TrimSpace(phase.Name) == "" {
			add("%s.name: field required", path)
		}
		if phase.StartedAt.IsZero() || phase.EndedAt.IsZero() {
			add("%s: startedAt and endedAt are required", path)
		} else if phase.EndedAt.Before(phase.StartedAt) {
			add("%s.endedAt: must not be before startedAt", path)
		} else if !status.CompletedAt.IsZero() && phase.EndedAt.After(status.CompletedAt) {
			add("%s.endedAt: must not be after status.completedAt", path)
		}
	}
	if len(status.Checks) > maxChecks {
		add("status.checks: at most %d checks", maxChecks)
	}
	for index := range status.Checks {
		check := &status.Checks[index]
		path := fmt.Sprintf("status.checks.%d", index)
		level, err := parseLevelJSON(check.Level)
		if err != nil {
			add("%s.level: %v", path, err)
		}
		check.level = level
		if strings.TrimSpace(check.Name) == "" {
			add("%s.name: field required", path)
		}
		if check.Passed == nil {
			add("%s.passed: field required", path)
		}
	}
	if e.upTo == V4 {
		hasLedger := status.Ledger != nil && len(status.Ledger.Acks) > 0
		hasInvariantEvidence := false
		if e.Spec.V4Evidence != nil {
			if len(e.Spec.V4Evidence.Invariants) > maxChecks {
				add("spec.v4Evidence.invariants: at most %d declared invariants", maxChecks)
			}
			seen := make(map[string]struct{}, len(e.Spec.V4Evidence.Invariants))
			for index, name := range e.Spec.V4Evidence.Invariants {
				if strings.TrimSpace(name) == "" {
					add("spec.v4Evidence.invariants.%d: name must not be empty", index)
					continue
				}
				if _, duplicate := seen[name]; duplicate {
					add("spec.v4Evidence.invariants.%d: duplicate invariant %q", index, name)
					continue
				}
				seen[name] = struct{}{}
				hasInvariantEvidence = true
				found := false
				for _, check := range status.Checks {
					if check.level == V4 && check.Name == name {
						found = true
						break
					}
				}
				if !found {
					add("spec.v4Evidence.invariants.%d: declared invariant %q needs a matching V4 check", index, name)
				}
			}
		}
		if !hasLedger && !hasInvariantEvidence {
			add("spec.v4Evidence: V4 requires acknowledged writes in status.ledger.acks or declared invariant evidence")
		}
	}
	if ledger := status.Ledger; ledger != nil {
		if len(ledger.Acks) > maxWrites || len(ledger.Present) > maxWrites {
			add("status.ledger: at most %d acknowledged and %d present writes", maxWrites, maxWrites)
		}
		seen := make(map[string]struct{}, len(ledger.Acks))
		bad := 0
		for index, ack := range ledger.Acks {
			if bad == 10 {
				add("status.ledger.acks: further problems omitted")
				break
			}
			if ack.WriteID == "" || ack.AckedAt.IsZero() {
				add("status.ledger.acks.%d: writeId and ackedAt are required", index)
				bad++
				continue
			}
			if _, duplicate := seen[ack.WriteID]; duplicate {
				add("status.ledger.acks.%d.writeId: duplicate write %q", index, ack.WriteID)
				bad++
			}
			seen[ack.WriteID] = struct{}{}
		}
	}
	return problems
}

func validLabel(value string) bool { return len(value) <= 63 && dnsLabel.MatchString(value) }

func parseLevelJSON(raw json.RawMessage) (Level, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return ParseLevel(text)
	}
	var number int
	if err := json.Unmarshal(raw, &number); err == nil {
		return ParseLevel(strconv.Itoa(number))
	}
	return 0, errors.New("expected V0 to V4")
}

// parseObjective accepts compact durations such as 90s, 15m or 1h30m, or an
// integer number of seconds, matching the drill specification.
func parseObjective(raw json.RawMessage) (time.Duration, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, false, nil
	}
	var seconds int64
	if err := json.Unmarshal(trimmed, &seconds); err == nil {
		if seconds < 0 {
			return 0, false, errors.New("must not be negative")
		}
		if seconds > maxDurationSeconds {
			return 0, false, errors.New("duration exceeds the maximum supported value")
		}
		return time.Duration(seconds) * time.Second, true, nil
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err != nil {
		return 0, false, errors.New("must be a compact duration such as 15m or an integer number of seconds")
	}
	parts := compactDuration.FindStringSubmatch(strings.TrimSpace(text))
	if parts == nil || (parts[1] == "" && parts[2] == "" && parts[3] == "") {
		return 0, false, errors.New("invalid duration; use forms like 90s, 15m or 1h30m")
	}
	total, err := time.ParseDuration(text)
	if err != nil {
		return 0, false, errors.New("duration exceeds the maximum supported value")
	}
	return total, true, nil
}

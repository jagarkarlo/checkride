package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const maxDrillRequestBytes = 1 << 20

var (
	dnsLabelPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	durationPattern = regexp.MustCompile(`^(\d+h)?(\d+m)?(\d+s)?$`)
	scenarios       = map[string]struct{}{
		"namespace-loss":         {},
		"cluster-loss":           {},
		"bad-migration":          {},
		"ransomware":             {},
		"lost-secret":            {},
		"storage-class-mismatch": {},
	}
)

type validationResult struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

type drillDocument struct {
	APIVersion *string        `json:"apiVersion"`
	Kind       *string        `json:"kind"`
	Metadata   *drillMetadata `json:"metadata"`
	Spec       *drill         `json:"spec"`
}

type drillMetadata struct {
	Name *string `json:"name"`
}

type drill struct {
	Scenario   *string         `json:"scenario"`
	Target     *drillTarget    `json:"target"`
	Restore    json.RawMessage `json:"restore"`
	Verify     *drillVerify    `json:"verify"`
	Objectives json.RawMessage `json:"objectives"`
}

type drillTarget struct {
	Namespace         *string `json:"namespace"`
	ArgoCDApplication *string `json:"argocdApplication"`
	CNPGCluster       *string `json:"cnpgCluster"`
}

type drillRestore struct {
	Into        json.RawMessage `json:"into"`
	PointInTime *string         `json:"pointInTime"`
}

type drillVerify struct {
	UpTo       json.RawMessage `json:"upTo"`
	Ledger     json.RawMessage `json:"ledger"`
	Invariants json.RawMessage `json:"invariants"`
}

type drillInvariant struct {
	Name   *string         `json:"name"`
	SQL    *string         `json:"sql"`
	Expect json.RawMessage `json:"expect"`
}

type drillObjectives struct {
	RTO     json.RawMessage `json:"rto"`
	RPO     json.RawMessage `json:"rpo"`
	Timeout json.RawMessage `json:"timeout"`
}

func validateDrillHandler(writer http.ResponseWriter, request *http.Request) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeJSON(writer, http.StatusUnsupportedMediaType, validationResult{
			Valid: false, Errors: []string{"Content-Type must be application/json"}, Warnings: []string{},
		})
		return
	}

	request.Body = http.MaxBytesReader(writer, request.Body, maxDrillRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var document drillDocument
	if err := decoder.Decode(&document); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(writer, http.StatusRequestEntityTooLarge, validationResult{
				Valid: false, Errors: []string{"request body exceeds 1 MiB"}, Warnings: []string{},
			})
			return
		}
		writeJSON(writer, http.StatusBadRequest, validationResult{
			Valid: false, Errors: []string{fmt.Sprintf("invalid JSON: %v", err)}, Warnings: []string{},
		})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(writer, http.StatusBadRequest, validationResult{
			Valid: false, Errors: []string{"request body must contain exactly one JSON value"}, Warnings: []string{},
		})
		return
	}

	errorsFound, warnings := validateDrill(document)
	result := validationResult{Valid: len(errorsFound) == 0, Errors: errorsFound, Warnings: warnings}
	if result.Errors == nil {
		result.Errors = []string{}
	}
	if result.Warnings == nil {
		result.Warnings = []string{}
	}
	status := http.StatusOK
	if !result.Valid {
		status = http.StatusUnprocessableEntity
	}
	writeJSON(writer, status, result)
}

func validateDrill(document drillDocument) ([]string, []string) {
	var problems []string
	var warnings []string
	if document.APIVersion == nil {
		problems = append(problems, "apiVersion: field required")
	} else if *document.APIVersion != "checkride/v1alpha1" {
		problems = append(problems, "apiVersion: must be checkride/v1alpha1")
	}
	if document.Kind == nil {
		problems = append(problems, "kind: field required")
	} else if *document.Kind != "Drill" {
		problems = append(problems, "kind: must be Drill")
	}

	if document.Metadata == nil {
		problems = append(problems, "metadata: field required")
	} else if document.Metadata.Name == nil {
		problems = append(problems, "metadata.name: field required")
	} else if !validDNSLabel(*document.Metadata.Name) {
		problems = append(problems, "metadata.name: must be a DNS label of at most 63 characters")
	}

	if document.Spec == nil {
		problems = append(problems, "spec: field required")
		return problems, warnings
	}
	spec := document.Spec
	if spec.Scenario == nil {
		problems = append(problems, "spec.scenario: field required")
	} else if _, ok := scenarios[*spec.Scenario]; !ok {
		problems = append(problems, "spec.scenario: unknown scenario")
	}
	if spec.Target == nil {
		problems = append(problems, "spec.target: field required")
	} else if spec.Target.Namespace == nil {
		problems = append(problems, "spec.target.namespace: field required")
	} else if !validDNSLabel(*spec.Target.Namespace) {
		problems = append(problems, "spec.target.namespace: must be a DNS label of at most 63 characters")
	}

	restoreInto := "separate-cluster"
	var restore drillRestore
	restoreValid := true
	if len(spec.Restore) > 0 {
		if isJSONNull(spec.Restore) {
			problems = append(problems, "spec.restore: must be an object")
			restoreValid = false
		} else if err := decodeStrictJSON(spec.Restore, &restore); err != nil {
			problems = append(problems, "spec.restore: "+err.Error())
			restoreValid = false
		}
	}
	if restoreValid {
		if len(restore.Into) > 0 {
			if err := json.Unmarshal(restore.Into, &restoreInto); err != nil {
				problems = append(problems, "spec.restore.into: must be separate-cluster or namespace")
				restoreValid = false
			}
		}
		if restoreValid && restoreInto != "separate-cluster" && restoreInto != "namespace" {
			problems = append(problems, "spec.restore.into: must be separate-cluster or namespace")
		}
		if pointInTime := restore.PointInTime; pointInTime != nil {
			if _, err := time.Parse(time.RFC3339Nano, *pointInTime); err != nil {
				problems = append(problems, "spec.restore.pointInTime: must be an ISO 8601 timestamp with a UTC offset")
			}
		}
	}
	if restoreInto == "namespace" {
		warnings = append(warnings, "restoring into a namespace of the source cluster hides missing CRDs, operators and Secrets; restore into a separate cluster for stronger evidence")
	}
	if spec.Scenario != nil && *spec.Scenario == "cluster-loss" && restoreInto == "namespace" {
		problems = append(problems, "spec: a cluster-loss drill must restore into a separate cluster")
	}

	if spec.Verify == nil {
		problems = append(problems, "spec.verify: field required")
	} else {
		level, err := parseVerificationLevel(spec.Verify.UpTo)
		if err != nil {
			problems = append(problems, "spec.verify.upTo: "+err.Error())
		}
		ledger, ledgerErr := parseOptionalBool(spec.Verify.Ledger, "spec.verify.ledger")
		if ledgerErr != nil {
			problems = append(problems, ledgerErr.Error())
		}
		invariants, invariantErr := parseInvariants(spec.Verify.Invariants)
		if invariantErr != nil {
			problems = append(problems, invariantErr.Error())
		}
		if err == nil && level == 4 && !ledger && len(invariants) == 0 {
			problems = append(problems, "spec.verify: V4 verification needs the write ledger or at least one invariant")
		}
		if ledger || len(invariants) > 0 {
			if spec.Target == nil || spec.Target.CNPGCluster == nil || *spec.Target.CNPGCluster == "" {
				problems = append(problems, "spec: the write ledger and SQL invariants need target.cnpgCluster")
			}
		}
	}

	objectives := drillObjectives{}
	objectivesValid := true
	if len(spec.Objectives) > 0 {
		if isJSONNull(spec.Objectives) {
			problems = append(problems, "spec.objectives: must be an object")
			objectivesValid = false
		} else if err := decodeStrictJSON(spec.Objectives, &objectives); err != nil {
			problems = append(problems, "spec.objectives: "+err.Error())
			objectivesValid = false
		}
	}
	if objectivesValid {
		rtoMissing := len(objectives.RTO) == 0 || isJSONNull(objectives.RTO)
		rpoMissing := len(objectives.RPO) == 0 || isJSONNull(objectives.RPO)
		if rtoMissing || rpoMissing {
			warnings = append(warnings, "no RTO/RPO objectives; the drill can measure recovery but not judge it")
		}
		if !rtoMissing {
			if err := validateDuration(objectives.RTO); err != nil {
				problems = append(problems, "spec.objectives.rto: "+err.Error())
			}
		}
		if !rpoMissing {
			if err := validateDuration(objectives.RPO); err != nil {
				problems = append(problems, "spec.objectives.rpo: "+err.Error())
			}
		}
		if len(objectives.Timeout) > 0 && !isJSONNull(objectives.Timeout) {
			if err := validateDuration(objectives.Timeout); err != nil {
				problems = append(problems, "spec.objectives.timeout: "+err.Error())
			}
		}
	}

	return problems, warnings
}

func validDNSLabel(value string) bool {
	return len(value) <= 63 && dnsLabelPattern.MatchString(value)
}

func parseVerificationLevel(raw json.RawMessage) (int, error) {
	if len(raw) == 0 {
		return 4, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(text), "V"))
		var level int
		if _, err := fmt.Sscanf(text, "%d", &level); err == nil && level >= 0 && level <= 4 && fmt.Sprint(level) == text {
			return level, nil
		}
	} else {
		var level int
		if err := json.Unmarshal(raw, &level); err == nil && level >= 0 && level <= 4 {
			return level, nil
		}
	}
	return 0, errors.New("expected V0 to V4")
}

func parseOptionalBool(raw json.RawMessage, field string) (bool, error) {
	if len(raw) == 0 {
		return false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if !bytes.Equal(trimmed, []byte("true")) && !bytes.Equal(trimmed, []byte("false")) {
		return false, fmt.Errorf("%s: must be a boolean", field)
	}
	return bytes.Equal(trimmed, []byte("true")), nil
}

func parseInvariants(raw json.RawMessage) ([]drillInvariant, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, errors.New("spec.verify.invariants: must be an array")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, errors.New("spec.verify.invariants: must be an array")
	}
	invariants := make([]drillInvariant, 0, len(items))
	for index, item := range items {
		var invariant drillInvariant
		if err := decodeStrictJSON(item, &invariant); err != nil {
			return nil, fmt.Errorf("spec.verify.invariants.%d: %s", index, err)
		}
		if invariant.Name == nil || invariant.SQL == nil || len(invariant.Expect) == 0 || bytes.Equal(bytes.TrimSpace(invariant.Expect), []byte("null")) {
			return nil, fmt.Errorf("spec.verify.invariants.%d: name, sql and expect are required", index)
		}
		if !isJSONScalar(invariant.Expect) {
			return nil, fmt.Errorf("spec.verify.invariants.%d.expect: must be a string, number or boolean", index)
		}
		invariants = append(invariants, invariant)
	}
	return invariants, nil
}

func validateDuration(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	var text string
	if len(trimmed) > 0 && trimmed[0] == '"' {
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return errors.New("must be a compact duration such as 15m")
		}
	} else {
		var seconds int64
		if err := json.Unmarshal(trimmed, &seconds); err != nil {
			return errors.New("must be a compact duration such as 15m or an integer number of seconds")
		}
		return nil
	}
	parts := durationPattern.FindStringSubmatch(strings.TrimSpace(text))
	if parts == nil || (parts[1] == "" && parts[2] == "" && parts[3] == "") {
		return errors.New("invalid duration; use forms like 90s, 15m or 1h30m")
	}
	return nil
}

func isJSONScalar(raw json.RawMessage) bool {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	switch value.(type) {
	case string, bool, json.Number, float64:
		return true
	default:
		return false
	}
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("expected exactly one JSON value")
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

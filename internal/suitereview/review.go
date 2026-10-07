package suitereview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"unicode/utf8"

	"github.com/jagarkarlo/nostekon/internal/verify"
)

var names = []string{"zero-loss", "tail-loss", "budget-loss"}

type measurement struct {
	Acknowledged     *int     `json:"acknowledged"`
	Recovered        *int     `json:"recovered"`
	Lost             *int     `json:"lost"`
	Holes            *int     `json:"holes"`
	Unexpected       *int     `json:"unexpected"`
	Seconds          *float64 `json:"seconds"`
	ObjectiveSeconds *int     `json:"objectiveSeconds"`
	Met              *bool    `json:"met"`
}

type recordedCase struct {
	Name             string          `json:"name"`
	DrillRun         string          `json:"drillRun"`
	ExpectedExitCode *int            `json:"expectedExitCode"`
	ObservedExitCode json.RawMessage `json:"observedExitCode"`
	Passed           *bool           `json:"passed"`
	RPO              *measurement    `json:"rpo"`
	Detail           *string         `json:"detail"`
	Error            *string         `json:"error"`
}

type summary struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Status     string         `json:"status"`
	Passed     *bool          `json:"passed"`
	Cases      []recordedCase `json:"cases"`
}

type Case struct {
	Name              string         `json:"name"`
	DrillRun          string         `json:"drillRun"`
	ExpectedExitCode  int            `json:"expectedExitCode"`
	ObservedExitCode  *int           `json:"observedExitCode"`
	EvaluatedExitCode *int           `json:"evaluatedExitCode"`
	EvidenceSHA256    *string        `json:"evidenceSHA256"`
	Report            *verify.Report `json:"report"`
	Issues            []string       `json:"issues"`
}

type Review struct {
	APIVersion      string            `json:"apiVersion"`
	Kind            string            `json:"kind"`
	Passed          bool              `json:"passed"`
	Complete        bool              `json:"complete"`
	RunnerStatus    string            `json:"runnerStatus"`
	RunnerPassed    bool              `json:"runnerPassed"`
	EvidenceMatches bool              `json:"evidenceMatches"`
	SummarySHA256   string            `json:"summarySHA256"`
	Provenance      verify.Provenance `json:"provenance"`
	Cases           []Case            `json:"cases"`
}

func Digest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func limit(name string) int {
	if name == "suite.json" || name == "manifest.json" {
		return 64 << 10
	}
	return 16 << 20
}

func ReadDirectory(path string) (map[string][]byte, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	sources := make(map[string][]byte)
	for _, name := range append([]string{"suite.json"}, artifactNames()...) {
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) && name != "suite.json" {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if !info.Mode().IsRegular() || info.Size() > int64(limit(name)) {
			return nil, fmt.Errorf("%s must be a regular file within its size limit", name)
		}
		file, err := root.Open(name)
		if err != nil {
			return nil, err
		}
		opened, statErr := file.Stat()
		if statErr != nil || !opened.Mode().IsRegular() {
			file.Close()
			return nil, fmt.Errorf("%s is not a readable regular file", name)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, int64(limit(name))+1))
		file.Close()
		if readErr != nil || len(data) > limit(name) {
			return nil, fmt.Errorf("%s could not be read within its size limit", name)
		}
		sources[name] = data
	}
	return sources, nil
}

func artifactNames() []string {
	files := make([]string, len(names))
	for index, name := range names {
		files[index] = name + ".drillrun.json"
	}
	return files
}

func parse(data []byte) (summary, error) {
	var suite summary
	if len(data) > limit("suite.json") || !utf8.Valid(data) {
		return suite, errors.New("suite summary must be UTF-8 within 64 KiB")
	}
	if err := json.Unmarshal(data, &suite); err != nil {
		return suite, fmt.Errorf("decode suite summary: %w", err)
	}
	if suite.APIVersion != "nostekon/lab-suite/v1alpha1" || suite.Kind != "LabSuiteResult" || suite.Passed == nil || suite.Cases == nil || len(suite.Cases) > 3 {
		return suite, errors.New("invalid LabSuiteResult version, kind or cases")
	}
	if suite.Status != "running" && suite.Status != "passed" && suite.Status != "failed" && suite.Status != "interrupted" || *suite.Passed != (suite.Status == "passed") {
		return suite, errors.New("invalid or contradictory suite status")
	}
	for index, item := range suite.Cases {
		expected := 0
		if index == 1 {
			expected = 1
		}
		var observed *int
		if item.Name != names[index] || item.DrillRun != names[index]+".drillrun.json" || item.Passed == nil || item.ExpectedExitCode == nil || *item.ExpectedExitCode != expected || len(item.ObservedExitCode) == 0 || json.Unmarshal(item.ObservedExitCode, &observed) != nil || observed != nil && *observed != 0 && *observed != 1 && *observed != 130 {
			return suite, fmt.Errorf("invalid suite case %d", index+1)
		}
		for _, detail := range []*string{item.Detail, item.Error} {
			if detail != nil && len([]rune(*detail)) > 500 {
				return suite, fmt.Errorf("case %s detail exceeds 500 characters", item.Name)
			}
		}
		if measured := item.RPO; measured != nil {
			for _, count := range []*int{measured.Acknowledged, measured.Recovered, measured.Lost, measured.Holes, measured.Unexpected} {
				if count == nil || *count < 0 || *count > 100 {
					return suite, fmt.Errorf("invalid RPO counts in %s", item.Name)
				}
			}
			if measured.Seconds == nil || *measured.Seconds < 0 || math.IsInf(*measured.Seconds, 0) || math.IsNaN(*measured.Seconds) || measured.ObjectiveSeconds == nil || *measured.ObjectiveSeconds < 0 || *measured.ObjectiveSeconds > 86400 || measured.Met == nil {
				return suite, fmt.Errorf("invalid RPO measurement in %s", item.Name)
			}
		}
		if *suite.Passed && !*item.Passed {
			return suite, errors.New("suite status contradicts its case results")
		}
	}
	if *suite.Passed && len(suite.Cases) != 3 {
		return suite, errors.New("passing suite must have three cases")
	}
	return suite, nil
}

func Evaluate(sources map[string][]byte) (Review, error) {
	data, present := sources["suite.json"]
	if !present {
		return Review{}, errors.New("missing suite.json")
	}
	suite, err := parse(data)
	if err != nil {
		return Review{}, err
	}
	review := Review{APIVersion: "nostekon/suite-review/v1alpha1", Kind: "SuiteReview", Complete: len(suite.Cases) == 3, RunnerStatus: suite.Status, RunnerPassed: *suite.Passed, SummarySHA256: Digest(data), Provenance: verify.Provenance{Status: "unverified"}, Cases: []Case{}}
	for _, recorded := range suite.Cases {
		item := Case{Name: recorded.Name, DrillRun: recorded.DrillRun, ExpectedExitCode: *recorded.ExpectedExitCode, Issues: []string{}}
		json.Unmarshal(recorded.ObservedExitCode, &item.ObservedExitCode)
		source, exists := sources[recorded.DrillRun]
		switch {
		case !exists:
			item.Issues = append(item.Issues, "Missing evidence: "+recorded.DrillRun)
		case len(source) > limit(recorded.DrillRun) || !utf8.Valid(source):
			item.Issues = append(item.Issues, "Evidence must be UTF-8 within 16 MiB.")
		default:
			digest := Digest(source)
			item.EvidenceSHA256 = &digest
			evidence, err := verify.ParseEvidence(source)
			if err != nil {
				item.Issues = append(item.Issues, "Invalid DrillRun evidence.")
				break
			}
			report := verify.Build(evidence)
			item.Report = &report
			exit := 2
			if report.Verdict == verify.Verified {
				exit = 0
			} else if report.Verdict == verify.Failed {
				exit = 1
			}
			item.EvaluatedExitCode = &exit
			if item.ObservedExitCode == nil || exit != *item.ObservedExitCode {
				item.Issues = append(item.Issues, "Evaluated exit code differs from the suite summary.")
			}
			if report.RequestedLevel != "V4" {
				item.Issues = append(item.Issues, "Evidence does not request V4 verification.")
			}
			compare(&item, recorded.RPO)
		}
		review.Cases = append(review.Cases, item)
	}
	review.EvidenceMatches = len(review.Cases) > 0
	for _, item := range review.Cases {
		review.EvidenceMatches = review.EvidenceMatches && len(item.Issues) == 0
	}
	review.Passed = review.Complete && review.RunnerPassed && review.EvidenceMatches
	return review, nil
}

func compare(item *Case, recorded *measurement) {
	report := item.Report
	actual := report.RPO
	if recorded == nil || actual == nil || actual.ObjectiveSeconds == nil || actual.Met == nil {
		item.Issues = append(item.Issues, "No complete RPO measurement to compare.")
		return
	}
	for _, field := range []struct {
		name     string
		actual   int
		expected int
	}{{"acknowledged", actual.Acknowledged, *recorded.Acknowledged}, {"recovered", actual.Recovered, *recorded.Recovered}, {"lost", actual.Lost, *recorded.Lost}, {"holes", actual.Holes, *recorded.Holes}, {"unexpected", actual.Unexpected, *recorded.Unexpected}} {
		if field.actual != field.expected {
			item.Issues = append(item.Issues, field.name+" differs from the suite summary.")
		}
	}
	if *actual.ObjectiveSeconds != float64(*recorded.ObjectiveSeconds) {
		item.Issues = append(item.Issues, "objectiveSeconds differs from the suite summary.")
	}
	if *actual.Met != *recorded.Met {
		item.Issues = append(item.Issues, "met differs from the suite summary.")
	}
	if math.Abs(actual.Seconds-*recorded.Seconds) > 0.000001 {
		item.Issues = append(item.Issues, "seconds differs from the suite summary.")
	}
	if !actual.Consistent {
		item.Issues = append(item.Issues, "The restored ledger is inconsistent.")
	}
	expectedLoss := 2
	if item.Name == "zero-loss" {
		expectedLoss = 0
	}
	unrelatedFailure := false
	expectedDetail := fmt.Sprintf("acknowledged-write ledger failed 0s RPO objective: %d lost, %d holes, %d unexpected", actual.Lost, actual.Holes, actual.Unexpected)
	for _, level := range report.Levels {
		for _, check := range level.Checks {
			expectedRunnerFailure := level.ID == "V4" && check.Name == "Lab execution" && check.Source == "reported" && check.Detail == expectedDetail
			if level.InScope && !check.Passed && check.Source != "ledger" && !expectedRunnerFailure {
				unrelatedFailure = true
			}
		}
	}
	if actual.Lost != expectedLoss || actual.Holes != 0 || actual.Unexpected != 0 || item.EvaluatedExitCode == nil || *item.EvaluatedExitCode != item.ExpectedExitCode || item.Name == "tail-loss" && (report.FirstFailed == nil || *report.FirstFailed != "V4" || report.DeepestPassed == nil || *report.DeepestPassed != "V3" || *actual.Met || *actual.ObjectiveSeconds != 0 || unrelatedFailure || report.RTO != nil && report.RTO.Met != nil && !*report.RTO.Met) {
		item.Issues = append(item.Issues, "Evidence does not demonstrate the expected policy outcome.")
	}
}

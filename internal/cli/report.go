package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/jagarkarlo/checkride/internal/verify"
)

const maxReportInputBytes = 16 << 20

// Run evaluates a captured DrillRun file. The exported entry point is shared
// by the standalone command and tests.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stderr, "usage: checkride-report <run.json>")
		if len(args) == 1 {
			return 0
		}
		return 2
	}
	file, err := os.Open(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "open evidence: %v\n", err)
		return 1
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxReportInputBytes+1))
	if err != nil {
		fmt.Fprintf(stderr, "read evidence: %v\n", err)
		return 1
	}
	if len(data) > maxReportInputBytes {
		fmt.Fprintf(stderr, "evidence exceeds 16 MiB\n")
		return 1
	}
	evidence, err := verify.ParseEvidence(data)
	if err != nil {
		var decodeErr *verify.DecodeError
		var validationErr *verify.ValidationError
		switch {
		case errors.As(err, &decodeErr):
			fmt.Fprintln(stderr, decodeErr)
		case errors.As(err, &validationErr):
			for _, problem := range validationErr.Problems {
				fmt.Fprintf(stderr, "- %s\n", problem)
			}
		default:
			fmt.Fprintln(stderr, err)
		}
		return 2
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	report := verify.Build(evidence)
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintf(stderr, "write report: %v\n", err)
		return 1
	}
	switch report.Verdict {
	case verify.Verified:
		return 0
	case verify.Failed:
		return 1
	default:
		return 2
	}
}

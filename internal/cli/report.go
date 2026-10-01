package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jagarkarlo/checkride/internal/attest"
	"github.com/jagarkarlo/checkride/internal/metrics"
	"github.com/jagarkarlo/checkride/internal/verify"
)

const maxReportInputBytes = 16 << 20

// Run evaluates a captured DrillRun file. The exported entry point is shared
// by the standalone command and tests.
func Run(args []string, stdout, stderr io.Writer) int {
	usage := "usage: checkride-report [--attestation FILE --trusted-key PUBLIC.pem] [--pushgateway-url URL [--pushgateway-job NAME] [--pushgateway-instance NAME]] <run.json>"
	if len(args) == 0 || len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stderr, usage)
		if len(args) == 1 {
			return 0
		}
		return 2
	}
	flags := flag.NewFlagSet("checkride-report", flag.ContinueOnError)
	flags.SetOutput(stderr)
	attestationPath := flags.String("attestation", "", "detached Ed25519 attestation JSON file")
	trustedKeyPath := flags.String("trusted-key", "", "trusted Ed25519 PKIX public key PEM")
	pushgatewayURL := flags.String("pushgateway-url", "", "push recovery metrics to this Prometheus Pushgateway URL")
	pushgatewayJob := flags.String("pushgateway-job", "checkride", "Pushgateway job label")
	pushgatewayInstance := flags.String("pushgateway-instance", "", "Pushgateway instance label (defaults to the evidence name)")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if flags.NArg() != 1 || (*attestationPath == "") != (*trustedKeyPath == "") {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	file, err := os.Open(flags.Arg(0))
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
	if *attestationPath != "" {
		sidecar, err := attest.ReadSidecar(*attestationPath)
		if err != nil {
			fmt.Fprintf(stderr, "verify attestation: %v\n", err)
			return 1
		}
		trustedKey, err := attest.LoadPublicKey(*trustedKeyPath)
		if err != nil {
			fmt.Fprintf(stderr, "load trusted key: %v\n", err)
			return 1
		}
		if err := attest.Verify(data, sidecar, trustedKey); err != nil {
			fmt.Fprintf(stderr, "verify attestation: %v\n", err)
			return 1
		}
		report.Provenance = verify.Provenance{
			Status: "verified", Algorithm: sidecar.Algorithm,
			KeyID: sidecar.KeyID, EvidenceSHA256: sidecar.EvidenceSHA256,
		}
	}
	if *pushgatewayURL != "" {
		instance := *pushgatewayInstance
		if instance == "" {
			instance = evidence.Metadata.Name
		}
		if err := metrics.Push(*pushgatewayURL, *pushgatewayJob, map[string]string{"instance": instance}, metrics.FromReport(report)); err != nil {
			fmt.Fprintf(stderr, "push metrics: %v\n", err)
		}
	}
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

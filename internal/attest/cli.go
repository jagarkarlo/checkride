package attest

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/jagarkarlo/checkride/internal/verify"
)

type commandFlags struct {
	evidencePath    string
	keyPath         string
	outputPath      string
	attestationPath string
	trustedKeyPath  string
}

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stderr, "usage: checkride-attest <keygen|sign|verify> [options]")
		return 2
	}
	switch args[0] {
	case "keygen":
		return runKeygen(args[1:], stdout, stderr)
	case "sign":
		return runSign(args[1:], stdout, stderr)
	case "verify":
		return runVerify(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\nusage: checkride-attest <keygen|sign|verify> [options]\n", args[0])
		return 2
	}
}

func runKeygen(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("keygen", flag.ContinueOnError)
	flags.SetOutput(stderr)
	privatePath := flags.String("private", "", "private key output path (mode 0600)")
	publicPath := flags.String("public", "", "public key output path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *privatePath == "" || *publicPath == "" {
		fmt.Fprintln(stderr, "usage: checkride-attest keygen --private PRIVATE.pem --public PUBLIC.pem")
		return 2
	}
	if err := WriteKeyPair(*privatePath, *publicPath); err != nil {
		fmt.Fprintf(stderr, "key generation failed: %v\n", err)
		return 1
	}
	publicKey, err := LoadPublicKey(*publicPath)
	if err != nil {
		fmt.Fprintf(stderr, "load generated public key: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Generated Ed25519 key pair; keyId=%s\n", KeyID(publicKey))
	return 0
}

func runSign(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("sign", flag.ContinueOnError)
	flags.SetOutput(stderr)
	evidencePath := flags.String("evidence", "", "DrillRun JSON file to sign")
	keyPath := flags.String("key", "", "Ed25519 PKCS#8 private key PEM")
	outputPath := flags.String("output", "", "new detached attestation JSON output file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *evidencePath == "" || *keyPath == "" || *outputPath == "" {
		fmt.Fprintln(stderr, "usage: checkride-attest sign --evidence RUN.json --key PRIVATE.pem --output RUN.attestation.json")
		return 2
	}
	evidence, err := ReadEvidence(*evidencePath)
	if err != nil {
		fmt.Fprintf(stderr, "signing failed: %v\n", err)
		return 1
	}
	if _, err := verify.ParseEvidence(evidence); err != nil {
		writeEvidenceProblem(stderr, err)
		return 2
	}
	privateKey, err := LoadPrivateKey(*keyPath)
	if err != nil {
		fmt.Fprintf(stderr, "signing failed: %v\n", err)
		return 1
	}
	sidecar, err := Sign(evidence, privateKey)
	if err != nil {
		fmt.Fprintf(stderr, "signing failed: %v\n", err)
		return 1
	}
	if err := WriteSidecar(*outputPath, sidecar); err != nil {
		fmt.Fprintf(stderr, "write attestation: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Signed evidence sha256=%s keyId=%s\n", sidecar.EvidenceSHA256, sidecar.KeyID)
	return 0
}

func runVerify(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	evidencePath := flags.String("evidence", "", "DrillRun JSON file to verify")
	attestationPath := flags.String("attestation", "", "detached attestation JSON file")
	trustedKeyPath := flags.String("trusted-key", "", "trusted Ed25519 PKIX public key PEM")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *evidencePath == "" || *attestationPath == "" || *trustedKeyPath == "" {
		fmt.Fprintln(stderr, "usage: checkride-attest verify --evidence RUN.json --attestation RUN.attestation.json --trusted-key TRUSTED.pem")
		return 2
	}
	evidence, err := ReadEvidence(*evidencePath)
	if err != nil {
		fmt.Fprintf(stderr, "verification failed: %v\n", err)
		return 1
	}
	if _, err := verify.ParseEvidence(evidence); err != nil {
		writeEvidenceProblem(stderr, err)
		return 2
	}
	sidecar, err := ReadSidecar(*attestationPath)
	if err != nil {
		fmt.Fprintf(stderr, "verification failed: %v\n", err)
		return 1
	}
	trustedKey, err := LoadPublicKey(*trustedKeyPath)
	if err != nil {
		fmt.Fprintf(stderr, "verification failed: %v\n", err)
		return 1
	}
	if err := Verify(evidence, sidecar, trustedKey); err != nil {
		fmt.Fprintf(stderr, "verification failed: %v\n", err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(struct {
		Verified       bool   `json:"verified"`
		Algorithm      string `json:"algorithm"`
		KeyID          string `json:"keyId"`
		EvidenceSHA256 string `json:"evidenceSHA256"`
	}{true, sidecar.Algorithm, sidecar.KeyID, sidecar.EvidenceSHA256}); err != nil {
		fmt.Fprintf(stderr, "write verification result: %v\n", err)
		return 1
	}
	return 0
}

func writeEvidenceProblem(stderr io.Writer, err error) {
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
}

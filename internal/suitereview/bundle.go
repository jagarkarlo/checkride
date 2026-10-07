package suitereview

import (
	"archive/zip"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

const maxBundleBytes = 49 << 20

type bundleManifest struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Job        struct {
		ID               string `json:"id"`
		Status           string `json:"status"`
		CompletedAt      string `json:"completedAt"`
		RecoveryRequired bool   `json:"recoveryRequired"`
	} `json:"job"`
	Files []struct {
		Name   string  `json:"name"`
		Size   *uint64 `json:"size"`
		SHA256 string  `json:"sha256"`
	} `json:"files"`
	MissingArtifacts []string `json:"missingArtifacts"`
}

func Read(path string) (map[string][]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return ReadDirectory(path)
	}
	if !info.Mode().IsRegular() || info.Size() > maxBundleBytes {
		return nil, errors.New("bundle must be a regular ZIP file within 49 MiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxBundleBytes {
		return nil, errors.New("bundle must be a regular ZIP file within 49 MiB")
	}
	archive, err := zip.NewReader(file, info.Size())
	if err != nil {
		return nil, fmt.Errorf("read ZIP: %w", err)
	}
	if len(archive.File) > 5 {
		return nil, errors.New("bundle has too many entries")
	}
	known := map[string]bool{"suite.json": true}
	for _, name := range artifactNames() {
		known[name] = true
	}
	sources := make(map[string][]byte)
	for _, entry := range archive.File {
		if !known[entry.Name] && entry.Name != "manifest.json" {
			return nil, errors.New("unexpected bundle entry")
		}
		if _, duplicate := sources[entry.Name]; duplicate || entry.Method != zip.Store || entry.Flags&1 != 0 || !entry.Mode().IsRegular() || entry.UncompressedSize64 != entry.CompressedSize64 || entry.UncompressedSize64 > uint64(limit(entry.Name)) {
			return nil, errors.New("duplicate, unsupported or oversized bundle entry")
		}
		reader, err := entry.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, int64(limit(entry.Name))+1))
		reader.Close()
		if readErr != nil || len(data) > limit(entry.Name) || uint64(len(data)) != entry.UncompressedSize64 || !utf8.Valid(data) {
			return nil, errors.New("invalid or oversized UTF-8 bundle entry")
		}
		sources[entry.Name] = data
	}
	var manifest bundleManifest
	if err := json.Unmarshal(sources["manifest.json"], &manifest); err != nil {
		return nil, errors.New("missing or invalid manifest.json")
	}
	_, idErr := hex.DecodeString(manifest.Job.ID)
	_, timeErr := time.Parse(time.RFC3339, manifest.Job.CompletedAt)
	terminal := manifest.Job.Status == "completed" || manifest.Job.Status == "failed" || manifest.Job.Status == "cancelled" || manifest.Job.Status == "timed_out" || manifest.Job.Status == "interrupted"
	if manifest.APIVersion != "nostekon/evidence-bundle/v1alpha1" || manifest.Kind != "LabEvidenceBundle" || manifest.Files == nil || manifest.MissingArtifacts == nil || len(manifest.Files) > 4 || len(manifest.MissingArtifacts) > 4 || len(manifest.Job.ID) != 24 || strings.ToLower(manifest.Job.ID) != manifest.Job.ID || idErr != nil || timeErr != nil || !terminal || manifest.Job.RecoveryRequired {
		return nil, errors.New("invalid evidence bundle manifest or unreconciled job")
	}
	accounted := make(map[string]bool)
	for _, descriptor := range manifest.Files {
		data, exists := sources[descriptor.Name]
		if !known[descriptor.Name] || accounted[descriptor.Name] || !exists || descriptor.Size == nil || *descriptor.Size != uint64(len(data)) || descriptor.SHA256 != Digest(data) {
			return nil, errors.New("invalid bundle descriptor, size or checksum")
		}
		accounted[descriptor.Name] = true
	}
	for _, name := range manifest.MissingArtifacts {
		_, exists := sources[name]
		if !known[name] || accounted[name] || exists {
			return nil, errors.New("invalid missing-artifact declaration")
		}
		accounted[name] = true
	}
	if len(accounted) != 4 || len(sources) != len(manifest.Files)+1 {
		return nil, errors.New("bundle manifest does not account for all artifacts")
	}
	delete(sources, "manifest.json")
	return sources, nil
}

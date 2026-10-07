package labjobs

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type bundleFile struct {
	Name   string `json:"name"`
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
}

type bundleManifest struct {
	APIVersion       string       `json:"apiVersion"`
	Kind             string       `json:"kind"`
	Job              Job          `json:"job"`
	Files            []bundleFile `json:"files"`
	MissingArtifacts []string     `json:"missingArtifacts"`
}

func (manager *Manager) Export(id string) ([]byte, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return nil, errors.New("lab history is closed")
	}
	execution, exists := manager.jobs[id]
	if !exists {
		return nil, ErrNotFound
	}
	if manager.active != "" || execution.job.CompletedAt == nil {
		return nil, ErrBusy
	}
	if execution.job.RecoveryRequired {
		return nil, ErrRecoveryRequired
	}
	job := execution.job
	job.Log, job.StorageError, job.Summary, job.Artifacts = "", "", nil, []string{}
	manifest := bundleManifest{APIVersion: "nostekon/evidence-bundle/v1alpha1", Kind: "LabEvidenceBundle", Job: job, Files: []bundleFile{}, MissingArtifacts: []string{}}
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for _, name := range artifactNames {
		info, err := manager.root.Lstat(filepath.Join(id, "suite", name))
		if errors.Is(err, os.ErrNotExist) {
			manifest.MissingArtifacts = append(manifest.MissingArtifacts, name)
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("cannot export non-regular or unreadable artifact %s", name)
		}
		data, err := manager.readArtifact(id, name)
		if err != nil {
			return nil, fmt.Errorf("cannot export bounded artifact %s", name)
		}
		checksum := sha256.Sum256(data)
		manifest.Files = append(manifest.Files, bundleFile{Name: name, Size: len(data), SHA256: hex.EncodeToString(checksum[:])})
		manifest.Job.Artifacts = append(manifest.Job.Artifacts, name)
		header := &zip.FileHeader{Name: name, Method: zip.Store}
		header.SetMode(0600)
		entry, err := archive.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write(data); err != nil {
			return nil, err
		}
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	header := &zip.FileHeader{Name: "manifest.json", Method: zip.Store}
	header.SetMode(0600)
	entry, err := archive.CreateHeader(header)
	if err != nil {
		return nil, err
	}
	if _, err := entry.Write(data); err != nil {
		return nil, err
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

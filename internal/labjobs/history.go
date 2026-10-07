package labjobs

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type savedJob struct {
	Version int `json:"version"`
	Job     Job `json:"job"`
}

func (manager *Manager) saveJob(execution *execution) error {
	job := execution.job
	job.Log, job.LogTruncated = execution.log.snapshot()
	job.Summary, job.Artifacts = nil, []string{}
	data, err := json.Marshal(savedJob{Version: 1, Job: job})
	if err != nil {
		return err
	}
	identifier := make([]byte, 12)
	if _, err := rand.Read(identifier); err != nil {
		return err
	}
	temporary := filepath.Join(job.ID, ".job-"+hex.EncodeToString(identifier)+".tmp")
	file, err := manager.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer manager.root.Remove(temporary)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := manager.root.Rename(temporary, filepath.Join(job.ID, "job.json")); err != nil {
		return err
	}
	directory, err := manager.root.Open(job.ID)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (manager *Manager) loadHistory() error {
	directory, err := manager.root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	inspected := 0
	for {
		entries, readErr := directory.ReadDir(100)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		for _, entry := range entries {
			inspected++
			if inspected > 1000 {
				return errors.New("too many lab data entries; archive older results")
			}
			id := entry.Name()
			decoded, decodeErr := hex.DecodeString(id)
			if decodeErr != nil || len(decoded) != 12 || id != strings.ToLower(id) {
				continue
			}
			info, err := manager.root.Lstat(id)
			if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
				return fmt.Errorf("job %s: directory must be private and not a symlink", id)
			}
			job, err := manager.loadJob(id)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("job %s: %w", id, err)
			}
			execution := &execution{job: job, done: make(chan struct{})}
			_, _ = execution.log.Write([]byte(job.Log))
			execution.log.truncated = execution.log.truncated || job.LogTruncated
			close(execution.done)
			manager.jobs[id] = execution
			manager.order = append(manager.order, id)
			if len(manager.jobs) > 50 {
				return errors.New("more than 50 saved lab jobs; archive older results")
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	sort.Slice(manager.order, func(first, second int) bool {
		left, right := manager.jobs[manager.order[first]].job, manager.jobs[manager.order[second]].job
		if left.StartedAt.Equal(right.StartedAt) {
			return left.ID < right.ID
		}
		return left.StartedAt.Before(right.StartedAt)
	})
	return nil
}

func (manager *Manager) loadJob(id string) (Job, error) {
	path := filepath.Join(id, "job.json")
	info, err := manager.root.Lstat(path)
	if err != nil {
		return Job{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return Job{}, errors.New("metadata must be a private regular file no larger than 1 MiB")
	}
	file, err := manager.root.Open(path)
	if err != nil {
		return Job{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return Job{}, errors.New("cannot read bounded metadata")
	}
	var record savedJob
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return Job{}, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Job{}, errors.New("metadata contains trailing data")
	}
	job := record.Job
	if record.Version != 1 || job.ID != id || job.Options.Validate() != nil || job.StartedAt.IsZero() || job.CompletedAt == nil || job.CompletedAt.Before(job.StartedAt) {
		return Job{}, errors.New("invalid job metadata")
	}
	switch job.Status {
	case "completed", "failed", "cancelled", "timed_out":
	default:
		return Job{}, errors.New("invalid saved job status")
	}
	return job, nil
}

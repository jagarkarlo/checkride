package labjobs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"
)

var ErrBusy = errors.New("a lab job is already active")
var ErrNotFound = errors.New("lab job or artifact not found")
var ErrCapacity = errors.New("lab history limit reached; stop the server and archive older job directories")
var ErrRecoveryRequired = errors.New("interrupted jobs require cleanup acknowledgement before starting another suite")

var artifactNames = []string{"suite.json", "zero-loss.drillrun.json", "tail-loss.drillrun.json", "budget-loss.drillrun.json"}

type Options struct {
	Writes     int `json:"writes"`
	RPOSeconds int `json:"rpoSeconds"`
}

func (options Options) Validate() error {
	if options.Writes < 1 || options.Writes > 98 || options.RPOSeconds < 1 || options.RPOSeconds > 86400 {
		return errors.New("writes must be 1..98 and rpoSeconds must be 1..86400")
	}
	return nil
}

type Job struct {
	ID               string          `json:"id"`
	Status           string          `json:"status"`
	Options          Options         `json:"options"`
	StartedAt        time.Time       `json:"startedAt"`
	CompletedAt      *time.Time      `json:"completedAt,omitempty"`
	ExitCode         *int            `json:"exitCode,omitempty"`
	Log              string          `json:"log"`
	LogTruncated     bool            `json:"logTruncated"`
	Summary          json.RawMessage `json:"summary,omitempty"`
	Artifacts        []string        `json:"artifacts"`
	StorageError     string          `json:"storageError,omitempty"`
	RecoveryRequired bool            `json:"recoveryRequired,omitempty"`
}

type execution struct {
	job       Job
	command   *exec.Cmd
	log       tailBuffer
	done      chan struct{}
	outcome   string
	timer     *time.Timer
	killTimer *time.Timer
}

type Manager struct {
	mu         sync.Mutex
	executable string
	directory  string
	root       *os.Root
	jobs       map[string]*execution
	order      []string
	active     string
	closed     bool
	command    func(string, ...string) *exec.Cmd
	timeout    time.Duration
	grace      time.Duration
	storageErr error
	lock       *os.File
}

func New(executable, directory string) (*Manager, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("lab jobs require Linux process-group cancellation")
	}
	resolved, err := exec.LookPath(executable)
	if err != nil {
		return nil, fmt.Errorf("find lab executable: %w", err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return nil, err
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("lab data directory must be private (0700) and not a symlink")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	manager := &Manager{executable: resolved, directory: directory, root: root, jobs: make(map[string]*execution), command: exec.Command, timeout: 15 * time.Minute, grace: 30 * time.Second}
	manager.lock, err = lockHistory(root)
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("lock lab history: %w", err)
	}
	if err := manager.loadHistory(); err != nil {
		_ = root.Close()
		_ = manager.lock.Close()
		return nil, fmt.Errorf("load lab history: %w", err)
	}
	return manager, nil
}

func (manager *Manager) Start(options Options) (Job, error) {
	if err := options.Validate(); err != nil {
		return Job{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return Job{}, errors.New("lab runner is shutting down")
	}
	if manager.active != "" {
		return Job{}, ErrBusy
	}
	if manager.storageErr != nil {
		return Job{}, fmt.Errorf("lab history unavailable: %w", manager.storageErr)
	}
	for _, execution := range manager.jobs {
		if execution.job.RecoveryRequired {
			return Job{}, ErrRecoveryRequired
		}
	}
	if len(manager.jobs) >= 50 {
		return Job{}, ErrCapacity
	}
	identifier := make([]byte, 12)
	if _, err := rand.Read(identifier); err != nil {
		return Job{}, err
	}
	id := hex.EncodeToString(identifier)
	if err := manager.root.Mkdir(id, 0700); err != nil {
		return Job{}, err
	}
	output := filepath.Join(manager.directory, id, "suite")
	command := manager.command(manager.executable, "lab", "suite", "--writes", strconv.Itoa(options.Writes), "--rpo-seconds", strconv.Itoa(options.RPOSeconds), "--output-dir", output)
	prepare(command)
	command.WaitDelay = manager.grace
	execution := &execution{job: Job{ID: id, Status: "running", Options: options, StartedAt: time.Now().UTC(), Artifacts: []string{}}, command: command, done: make(chan struct{})}
	command.Stdout, command.Stderr = &execution.log, &execution.log
	if err := manager.saveJob(execution); err != nil {
		return Job{}, fmt.Errorf("save initial lab job: %w", err)
	}
	if err := command.Start(); err != nil {
		_ = manager.root.Remove(filepath.Join(id, "job.json"))
		_ = manager.root.Remove(id)
		return Job{}, fmt.Errorf("start lab runner: %w", err)
	}
	manager.jobs[id] = execution
	manager.order = append(manager.order, id)
	manager.active = id
	execution.timer = time.AfterFunc(manager.timeout, func() { _ = manager.stop(id, "timed_out") })
	go manager.wait(execution)
	return execution.job, nil
}

func (manager *Manager) wait(execution *execution) {
	err := execution.command.Wait()
	_ = terminate(execution.command)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	execution.timer.Stop()
	if execution.killTimer != nil {
		execution.killTimer.Stop()
	}
	completed := time.Now().UTC()
	code := execution.command.ProcessState.ExitCode()
	execution.job.CompletedAt, execution.job.ExitCode = &completed, &code
	execution.job.Status = "completed"
	if err != nil {
		execution.job.Status = "failed"
	}
	if execution.outcome != "" {
		execution.job.Status = execution.outcome
	}
	if err := manager.saveJob(execution); err != nil {
		manager.storageErr = err
		execution.job.StorageError = err.Error()
	}
	manager.active = ""
	close(execution.done)
}

func (manager *Manager) stop(id, outcome string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	execution, exists := manager.jobs[id]
	if !exists {
		return ErrNotFound
	}
	if execution.job.CompletedAt != nil || execution.outcome != "" {
		return nil
	}
	execution.outcome = outcome
	execution.job.Status = "cancelling"
	if err := manager.saveJob(execution); err != nil {
		manager.storageErr = err
		execution.job.StorageError = err.Error()
	}
	_ = interrupt(execution.command)
	execution.killTimer = time.AfterFunc(manager.grace, func() {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		if execution.job.CompletedAt == nil {
			_ = terminate(execution.command)
		}
	})
	return nil
}

func (manager *Manager) Cancel(id string) error { return manager.stop(id, "cancelled") }

func (manager *Manager) AcknowledgeRecovery(id string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed || manager.storageErr != nil {
		return errors.New("lab history is not available for recovery acknowledgement")
	}
	execution, exists := manager.jobs[id]
	if !exists {
		return ErrNotFound
	}
	if !execution.job.RecoveryRequired {
		return nil
	}
	execution.job.RecoveryRequired = false
	if err := manager.saveJob(execution); err != nil {
		execution.job.RecoveryRequired = true
		return fmt.Errorf("save recovery acknowledgement: %w", err)
	}
	return nil
}

func (manager *Manager) Get(id string) (Job, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	execution, exists := manager.jobs[id]
	if !exists {
		return Job{}, ErrNotFound
	}
	job := execution.job
	job.Log, job.LogTruncated = execution.log.snapshot()
	job.Artifacts = []string{}
	for _, name := range artifactNames {
		info, err := manager.root.Lstat(filepath.Join(id, "suite", name))
		if err == nil && info.Mode().IsRegular() && info.Size() <= 16<<20 {
			job.Artifacts = append(job.Artifacts, name)
			if name == "suite.json" {
				data, err := manager.readArtifact(id, name)
				if err == nil && json.Valid(data) {
					job.Summary = json.RawMessage(data)
				}
			}
		}
	}
	return job, nil
}

func (manager *Manager) List() []Job {
	manager.mu.Lock()
	ids := append([]string(nil), manager.order...)
	manager.mu.Unlock()
	jobs := make([]Job, 0, len(ids))
	for index := len(ids) - 1; index >= 0; index-- {
		job, err := manager.Get(ids[index])
		if err == nil {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

func (manager *Manager) readArtifact(id, name string) ([]byte, error) {
	allowed := false
	for _, artifact := range artifactNames {
		if artifact == name {
			allowed = true
		}
	}
	if !allowed {
		return nil, ErrNotFound
	}
	metadata, err := manager.root.Lstat(filepath.Join(id, "suite", name))
	if err != nil || !metadata.Mode().IsRegular() {
		return nil, ErrNotFound
	}
	file, err := manager.root.Open(filepath.Join(id, "suite", name))
	if err != nil {
		return nil, ErrNotFound
	}
	defer file.Close()
	info, err := file.Stat()
	limit := int64(16 << 20)
	if name == "suite.json" {
		limit = 64 << 10
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, ErrNotFound
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, ErrNotFound
	}
	return data, nil
}

func (manager *Manager) Artifact(id, name string) ([]byte, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, exists := manager.jobs[id]; !exists {
		return nil, ErrNotFound
	}
	return manager.readArtifact(id, name)
}

func (manager *Manager) Close() {
	manager.mu.Lock()
	manager.closed = true
	id := manager.active
	var done chan struct{}
	if id != "" {
		done = manager.jobs[id].done
	}
	manager.mu.Unlock()
	if id != "" {
		_ = manager.Cancel(id)
		<-done
	}
	_ = manager.root.Close()
	_ = manager.lock.Close()
}

type tailBuffer struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (buffer *tailBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	buffer.data = append(buffer.data, data...)
	if len(buffer.data) > 64<<10 {
		buffer.data = append([]byte(nil), buffer.data[len(buffer.data)-(64<<10):]...)
		buffer.truncated = true
	}
	return len(data), nil
}

func (buffer *tailBuffer) snapshot() (string, bool) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return string(buffer.data), buffer.truncated
}

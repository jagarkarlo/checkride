package labjobs

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFixedSuiteCommandAndResult(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "jobs")
	manager, err := New(executable, directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	manager.command = func(name string, arguments ...string) *exec.Cmd {
		command := exec.Command(name, append([]string{"-test.run=TestLabProcessHelper", "--"}, arguments...)...)
		command.Env = append(os.Environ(), "NOSTEKON_TEST_HELPER=complete")
		return command
	}
	job, err := manager.Start(Options{Writes: 10, RPOSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	if job.Artifacts == nil {
		t.Fatal("new jobs must expose an empty artifact array, not null")
	}
	deadline := time.After(5 * time.Second)
	for {
		job, err = manager.Get(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job.CompletedAt != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("job did not complete")
		case <-time.After(time.Millisecond):
		}
	}
	if job.Status != "completed" || job.ExitCode == nil || *job.ExitCode != 0 {
		t.Fatalf("unexpected result: %+v", job)
	}
	data, err := os.ReadFile(filepath.Join(directory, job.ID, "arguments.json"))
	if err != nil {
		t.Fatal(err)
	}
	var arguments []string
	if err := json.Unmarshal(data, &arguments); err != nil {
		t.Fatal(err)
	}
	want := []string{"lab", "suite", "--writes", "10", "--rpo-seconds", "60", "--output-dir", filepath.Join(directory, job.ID, "suite")}
	if !reflect.DeepEqual(arguments, want) {
		t.Fatalf("arguments = %v, want %v", arguments, want)
	}
	if !reflect.DeepEqual(job.Artifacts, []string{"suite.json"}) {
		t.Fatalf("artifacts = %v", job.Artifacts)
	}
	manager.Close()
	reopened, err := New(executable, directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Close)
	restored, err := reopened.Get(job.ID)
	if err != nil {
		t.Fatalf("reopen completed job: %v", err)
	}
	if restored.Status != "completed" || restored.ExitCode == nil || *restored.ExitCode != 0 || restored.Options != job.Options || !restored.StartedAt.Equal(job.StartedAt) || restored.CompletedAt == nil || !restored.CompletedAt.Equal(*job.CompletedAt) || restored.Log != "suite helper completed\n" {
		t.Fatalf("restored job = %+v", restored)
	}
	if jobs := reopened.List(); len(jobs) != 1 || jobs[0].ID != job.ID {
		t.Fatalf("restored history = %+v", jobs)
	}
	if data, err := reopened.Artifact(job.ID, "suite.json"); err != nil || string(data) != `{"status":"passed"}` {
		t.Fatalf("restored artifact = %s, %v", data, err)
	}
}

func TestEvidenceBundlePreservesOriginalsAndExcludesPrivateData(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	id := strings.Repeat("a", 24)
	suite := filepath.Join(directory, id, "suite")
	if err := os.MkdirAll(suite, 0700); err != nil {
		t.Fatal(err)
	}
	record := `{"version":1,"job":{"id":"aaaaaaaaaaaaaaaaaaaaaaaa","status":"completed","options":{"writes":10,"rpoSeconds":60},"startedAt":"2026-01-01T00:00:00Z","completedAt":"2026-01-01T00:01:00Z","exitCode":0,"log":"private process output","logTruncated":true,"artifacts":[],"storageError":"private host path"}}`
	if err := os.WriteFile(filepath.Join(directory, id, "job.json"), []byte(record), 0600); err != nil {
		t.Fatal(err)
	}
	originals := map[string][]byte{"suite.json": []byte("{\n  \"status\": \"passed\"\n}\n"), "zero-loss.drillrun.json": []byte("original bytes, even when malformed\n")}
	for name, data := range originals {
		if err := os.WriteFile(filepath.Join(suite, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(suite, "zero-loss.drillrun.json.ledger.db"), []byte("private ledger"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := New("/bin/true", directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	exporter, supported := any(manager).(interface{ Export(string) ([]byte, error) })
	if !supported {
		t.Fatal("lab manager does not support evidence bundle export")
	}
	data, err := exporter.Export(id)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string][]byte)
	for _, entry := range archive.File {
		if entry.Mode().Perm() != 0600 {
			t.Fatalf("bundle entry %s has public permissions: %v", entry.Name, entry.Mode())
		}
		file, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name] = contents
	}
	if len(files) != 3 {
		t.Fatalf("unexpected bundle entries: %v", files)
	}
	for name, original := range originals {
		if !bytes.Equal(files[name], original) {
			t.Fatalf("changed original evidence %s", name)
		}
	}
	var manifest struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Job        Job    `json:"job"`
		Files      []struct {
			Name   string `json:"name"`
			Size   int    `json:"size"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
		MissingArtifacts []string `json:"missingArtifacts"`
	}
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.APIVersion != "nostekon/evidence-bundle/v1alpha1" || manifest.Kind != "LabEvidenceBundle" || manifest.Job.ID != id || manifest.Job.Status != "completed" || manifest.Job.Options != (Options{10, 60}) || manifest.Job.Log != "" || manifest.Job.StorageError != "" || len(manifest.Job.Summary) != 0 {
		t.Fatalf("invalid or private manifest: %+v", manifest)
	}
	if len(manifest.Files) != 2 || !reflect.DeepEqual(manifest.MissingArtifacts, []string{"tail-loss.drillrun.json", "budget-loss.drillrun.json"}) {
		t.Fatalf("missing/available files = %+v", manifest)
	}
	for _, entry := range manifest.Files {
		original, exists := originals[entry.Name]
		checksum := sha256.Sum256(original)
		if !exists || entry.Size != len(original) || entry.SHA256 != hex.EncodeToString(checksum[:]) {
			t.Fatalf("invalid checksum entry: %+v", entry)
		}
	}
	if _, err := exporter.Export("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown export = %v", err)
	}
}

func TestLabProcessHelper(t *testing.T) {
	mode := os.Getenv("NOSTEKON_TEST_HELPER")
	if mode == "" {
		return
	}
	separator := 0
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index + 1
			break
		}
	}
	arguments := os.Args[separator:]
	output := arguments[len(arguments)-1]
	if err := os.Mkdir(output, 0700); err != nil {
		panic(err)
	}
	if mode == "hold" {
		interrupts := make(chan os.Signal, 1)
		signal.Notify(interrupts, os.Interrupt)
		if err := os.WriteFile(filepath.Join(output, "suite.json"), []byte(`{"status":"running"}`), 0600); err != nil {
			panic(err)
		}
		<-interrupts
		if err := os.WriteFile(filepath.Join(output, "suite.json"), []byte(`{"status":"interrupted"}`), 0600); err != nil {
			panic(err)
		}
		os.Exit(130)
	}
	data, _ := json.Marshal(arguments)
	if err := os.WriteFile(filepath.Join(filepath.Dir(output), "arguments.json"), data, 0600); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(output, "suite.json"), []byte(`{"status":"passed"}`), 0600); err != nil {
		panic(err)
	}
	println("suite helper completed")
	os.Exit(0)
}

func TestJobBoundariesAndCancellation(t *testing.T) {
	executable, _ := os.Executable()
	manager, err := New(executable, filepath.Join(t.TempDir(), "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	manager.command = func(name string, arguments ...string) *exec.Cmd {
		command := exec.Command(name, append([]string{"-test.run=TestLabProcessHelper", "--"}, arguments...)...)
		command.Env = append(os.Environ(), "NOSTEKON_TEST_HELPER=hold")
		return command
	}
	for _, options := range []Options{{0, 60}, {99, 60}, {10, 0}, {10, 86401}} {
		if _, err := manager.Start(options); err == nil {
			t.Fatalf("accepted invalid options: %+v", options)
		}
	}
	job, err := manager.Start(Options{10, 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start(Options{10, 60}); !errors.Is(err, ErrBusy) {
		t.Fatalf("overlap error = %v", err)
	}
	if _, err := manager.Export(job.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("active export = %v", err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		job, _ = manager.Get(job.ID)
		if len(job.Summary) != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no progress checkpoint")
		}
		<-time.After(time.Millisecond)
	}
	if err := manager.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	<-manager.jobs[job.ID].done
	job, err = manager.Get(job.ID)
	if err != nil || job.Status != "cancelled" || string(job.Summary) != `{"status":"interrupted"}` {
		t.Fatalf("cancelled job = %+v, %v", job, err)
	}
	if err := manager.Cancel(job.ID); err != nil {
		t.Fatalf("repeated cancel = %v", err)
	}
	if err := manager.Cancel("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown cancel = %v", err)
	}
	for _, name := range []string{"../arguments.json", "suite.json.ledger.db", "zero-loss.drillrun.json.ledger.db"} {
		if _, err := manager.Artifact(job.ID, name); !errors.Is(err, ErrNotFound) {
			t.Fatalf("exposed %s: %v", name, err)
		}
	}
	outside := filepath.Join(t.TempDir(), "private.json")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(manager.directory, job.ID, "suite", "zero-loss.drillrun.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Artifact(job.ID, "zero-loss.drillrun.json"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("escaped artifact root: %v", err)
	}
	if len(manager.List()) != 1 {
		t.Fatal("completed job disappeared")
	}
}

func TestTimeoutAndSessionCapacity(t *testing.T) {
	executable, _ := os.Executable()
	manager, err := New(executable, filepath.Join(t.TempDir(), "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	manager.timeout, manager.grace = 30*time.Millisecond, 30*time.Millisecond
	manager.command = func(name string, arguments ...string) *exec.Cmd {
		command := exec.Command(name, append([]string{"-test.run=TestLabProcessHelper", "--"}, arguments...)...)
		command.Env = append(os.Environ(), "NOSTEKON_TEST_HELPER=hold")
		return command
	}
	job, err := manager.Start(Options{10, 60})
	if err != nil {
		t.Fatal(err)
	}
	<-manager.jobs[job.ID].done
	job, _ = manager.Get(job.ID)
	if job.Status != "timed_out" {
		t.Fatalf("timeout result = %+v", job)
	}
	for index := 0; index < 49; index++ {
		manager.jobs[strings.Repeat("x", index+1)] = &execution{}
	}
	if _, err := manager.Start(Options{10, 60}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity error = %v", err)
	}
}

func TestBoundedLogTail(t *testing.T) {
	buffer := tailBuffer{}
	input := strings.Repeat("x", 128<<10) + "last line"
	if count, err := buffer.Write([]byte(input)); err != nil || count != len(input) {
		t.Fatalf("write = %d, %v", count, err)
	}
	log, truncated := buffer.snapshot()
	if !truncated || len(log) != 64<<10 || !strings.HasSuffix(log, "last line") {
		t.Fatalf("tail size=%d, truncated=%v", len(log), truncated)
	}
}

func TestRestartRequiresRecoveryAcknowledgement(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	id := strings.Repeat("a", 24)
	if err := os.MkdirAll(filepath.Join(directory, id, "suite"), 0700); err != nil {
		t.Fatal(err)
	}
	metadata := `{"version":1,"job":{"id":"aaaaaaaaaaaaaaaaaaaaaaaa","status":"running","options":{"writes":10,"rpoSeconds":60},"startedAt":"2026-01-01T00:00:00Z","log":"last saved log","logTruncated":false,"artifacts":[]}}`
	if err := os.WriteFile(filepath.Join(directory, id, "job.json"), []byte(metadata), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, id, "suite", "suite.json"), []byte(`{"status":"running"}`), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := New("/bin/true", directory)
	if err != nil {
		t.Fatalf("reconcile interrupted job: %v", err)
	}
	t.Cleanup(manager.Close)
	job, err := manager.Get(id)
	if err != nil || job.Status != "interrupted" || !job.RecoveryRequired || job.CompletedAt == nil || job.ExitCode != nil || job.Log != "last saved log" || string(job.Summary) != `{"status":"running"}` {
		t.Fatalf("reconciled job = %+v, %v", job, err)
	}
	if _, err := manager.Start(Options{10, 60}); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("new job before cleanup acknowledgement = %v", err)
	}
	if _, err := manager.Export(id); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("export before cleanup acknowledgement = %v", err)
	}
	if err := manager.Cancel(id); err != nil {
		t.Fatalf("historical cancellation must not signal a reused process: %v", err)
	}
	if err := manager.AcknowledgeRecovery(id); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Export(id); err != nil {
		t.Fatalf("export acknowledged partial evidence: %v", err)
	}
	manager.Close()
	reopened, err := New("/bin/true", directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Close)
	job, _ = reopened.Get(id)
	if job.RecoveryRequired || job.Status != "interrupted" {
		t.Fatalf("acknowledgement did not persist: %+v", job)
	}
	started, err := reopened.Start(Options{10, 60})
	if err != nil {
		t.Fatalf("start after acknowledgement: %v", err)
	}
	<-reopened.jobs[started.ID].done
}

func TestEvidenceBundleRejectsUnsafeArtifacts(t *testing.T) {
	for _, scenario := range []string{"symlink", "dangling-symlink", "directory", "summary-too-large", "case-too-large"} {
		t.Run(scenario, func(t *testing.T) {
			manager, err := New("/bin/true", filepath.Join(t.TempDir(), "jobs"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(manager.Close)
			job, err := manager.Start(Options{10, 60})
			if err != nil {
				t.Fatal(err)
			}
			<-manager.jobs[job.ID].done
			suite := filepath.Join(manager.directory, job.ID, "suite")
			if err := os.Mkdir(suite, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(suite, "suite.json")
			switch scenario {
			case "symlink", "dangling-symlink":
				outside := filepath.Join(t.TempDir(), "private.json")
				if scenario == "symlink" {
					if err := os.WriteFile(outside, []byte("private data"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			default:
				limit := int64(64 << 10)
				if scenario == "case-too-large" {
					path, limit = filepath.Join(suite, "zero-loss.drillrun.json"), 16<<20
				}
				file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				err = file.Truncate(limit + 1)
				file.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := manager.Export(job.ID); err == nil {
				t.Fatal("unsafe artifact was exported or silently marked missing")
			}
		})
	}
}

func TestEvidenceBundleWithoutArtifactsIsExplicitlyPartial(t *testing.T) {
	manager, err := New("/bin/true", filepath.Join(t.TempDir(), "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	job, err := manager.Start(Options{10, 60})
	if err != nil {
		t.Fatal(err)
	}
	<-manager.jobs[job.ID].done
	data, err := manager.Export(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(archive.File) != 1 || archive.File[0].Name != "manifest.json" {
		t.Fatalf("metadata-only archive = %v, %v", archive, err)
	}
	file, err := archive.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var manifest struct {
		Files            []any    `json:"files"`
		MissingArtifacts []string `json:"missingArtifacts"`
	}
	if err := json.NewDecoder(file).Decode(&manifest); err != nil || len(manifest.Files) != 0 || !reflect.DeepEqual(manifest.MissingArtifacts, artifactNames) {
		t.Fatalf("partial manifest = %+v, %v", manifest, err)
	}
	manager.Close()
	if _, err := manager.Export(job.ID); err == nil {
		t.Fatal("exported from a closed history")
	}
}

func TestDataDirectoryHasExclusiveOwner(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	manager, err := New("/bin/true", directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	other, err := New("/bin/true", directory)
	if err == nil {
		other.Close()
		t.Fatal("two managers acquired the same history directory")
	}
	manager.Close()
	other, err = New("/bin/true", directory)
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	other.Close()
}

func TestCorruptHistoryStopsStartup(t *testing.T) {
	valid := `{"version":1,"job":{"id":"aaaaaaaaaaaaaaaaaaaaaaaa","status":"completed","options":{"writes":10,"rpoSeconds":60},"startedAt":"2026-01-01T00:00:00Z","completedAt":"2026-01-01T00:01:00Z","exitCode":0,"log":"","logTruncated":false,"artifacts":[]}}`
	for _, scenario := range []string{"json", "version", "identity", "permissions", "oversize", "trailing", "symlink", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "jobs")
			id := strings.Repeat("a", 24)
			if err := os.MkdirAll(filepath.Join(directory, id), 0700); err != nil {
				t.Fatal(err)
			}
			data, mode := valid, os.FileMode(0600)
			switch scenario {
			case "json":
				data = "{"
			case "version":
				data = strings.Replace(valid, `"version":1`, `"version":2`, 1)
			case "identity":
				data = strings.Replace(valid, id, strings.Repeat("b", 24), 1)
			case "permissions":
				mode = 0644
			case "oversize":
				data = strings.Repeat(" ", (1<<20)+1)
			case "trailing":
				data += " {}"
			}
			path := filepath.Join(directory, id, "job.json")
			if scenario == "symlink" {
				outside := filepath.Join(t.TempDir(), "job.json")
				if err := os.WriteFile(outside, []byte(valid), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			} else if scenario != "missing" {
				if err := os.WriteFile(path, []byte(data), mode); err != nil {
					t.Fatal(err)
				}
			}
			manager, err := New("/bin/true", directory)
			if err == nil {
				manager.Close()
				t.Fatalf("accepted %s history", scenario)
			}
		})
	}
}

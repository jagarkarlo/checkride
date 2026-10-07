package labjobs

import (
	"encoding/json"
	"errors"
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

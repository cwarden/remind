// Copyright 2026 The remindgo Authors. All rights reserved.
// Use of this source code is governed by the GNU General Public License,
// Version 2, which can be found in the LICENSE file.

//go:build linux

package remind // import "github.com/cwarden/remind/v6"

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunReadsStdin(t *testing.T) {
	res, err := Run([]string{"-q", "-", "1", "Feb", "2024"}, []byte("REM MSG from-stdin\n"))
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit status %d, want 0; stderr: %s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(string(res.Stdout), "from-stdin") {
		t.Errorf("stdout %q does not contain the reminder", res.Stdout)
	}
	if len(res.Stderr) != 0 {
		t.Errorf("stderr %q, want empty", res.Stderr)
	}
}

func TestRunReportsUsageExit(t *testing.T) {
	res, err := Run([]string{"-n"}, []byte("REM MSG test\n"))
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 1 {
		t.Errorf("exit status %d, want 1", res.ExitCode)
	}
	if !strings.Contains(string(res.Stderr), "Usage: remind") {
		t.Errorf("stderr %q does not contain the usage text", res.Stderr)
	}
}

func TestRunReportsExitStatement(t *testing.T) {
	res, err := Run([]string{"-q", "-", "1", "Feb", "2024"}, []byte("EXIT 5\n"))
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 5 {
		t.Errorf("exit status %d, want 5; stderr: %s", res.ExitCode, res.Stderr)
	}
}

func TestRunReportsMissingFile(t *testing.T) {
	res, err := Run([]string{"-q", filepath.Join(t.TempDir(), "missing.rem"), "1", "Feb", "2024"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode == 0 {
		t.Error("exit status 0 for a missing file")
	}
	if len(res.Stderr) == 0 {
		t.Error("no error message for a missing file")
	}
}

func TestRunRereadsEditedFiles(t *testing.T) {
	file := filepath.Join(t.TempDir(), "edit.rem")
	run := func(body, want string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		res, err := Run([]string{"-q", file, "1", "Feb", "2024"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(res.Stdout), want) {
			t.Errorf("stdout %q does not contain %q", res.Stdout, want)
		}
	}
	run("REM MSG first-version\n", "first-version")
	run("REM MSG second-version\n", "second-version")
}

func TestRunLeavesProcessStreamsAlone(t *testing.T) {
	before := [3]uintptr{os.Stdin.Fd(), os.Stdout.Fd(), os.Stderr.Fd()}
	if _, err := Run([]string{"-q", "-", "1", "Feb", "2024"}, []byte("REM MSG x\n")); err != nil {
		t.Fatal(err)
	}
	after := [3]uintptr{os.Stdin.Fd(), os.Stdout.Fd(), os.Stderr.Fd()}
	if before != after {
		t.Errorf("process descriptors changed from %v to %v", before, after)
	}
}

// stdinPipe points descriptor 0 at the read end of a new pipe until the test
// ends, so that it differs from /dev/null, and returns what /proc reports for
// it.
func stdinPipe(t *testing.T) string {
	t.Helper()
	saved, err := syscall.Dup(0)
	if err != nil {
		t.Fatal(err)
	}
	var p [2]int
	if err := syscall.Pipe2(p[:], syscall.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Dup3(p[0], 0, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		syscall.Dup3(saved, 0, 0)
		syscall.Close(saved)
		syscall.Close(p[0])
		syscall.Close(p[1])
	})
	target, err := os.Readlink("/proc/self/fd/0")
	if err != nil {
		t.Fatal(err)
	}
	return target
}

// Remind runs shell(), INCLUDECMD and RUN commands with /dev/null as their
// standard input. The C build does that by putting /dev/null on descriptor
// 0 around the command, which inside another program replaces the host
// program's terminal: a terminal reader that wakes up in that interval reads
// end-of-file. Each command here reports its own standard input and that of
// the process running remind ($PPID of the shell). The brackets in
// "pipe:[inode]" are removed, because remind evaluates brackets in reminder
// bodies as expressions.
func TestRunCommandsLeaveDescriptorZeroAlone(t *testing.T) {
	keep := ` | tr -cd a-z0-9/:`
	report := `parent=$(readlink /proc/$PPID/fd/0` + keep + `) child=$(readlink /proc/self/fd/0` + keep + `)`
	tests := []struct {
		name string
		body string
	}{
		{"shell", "SET r shell(\"echo " + report + "\")\nREM MSG [r]\n"},
		{"INCLUDECMD", "INCLUDECMD echo \"REM MSG " + report + "\"\n"},
		{"RUN", "REM RUN echo " + report + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdin := stdinPipe(t)
			file := filepath.Join(t.TempDir(), "cmd.rem")
			if err := os.WriteFile(file, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			res, err := Run([]string{"-q", file}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if res.ExitCode != 0 {
				t.Errorf("exit status %d, want 0; stderr: %s", res.ExitCode, res.Stderr)
			}
			want := "parent=" + strings.Trim(strings.ReplaceAll(stdin, "[", ""), "]") + " child=/dev/null"
			if !strings.Contains(string(res.Stdout), want) {
				t.Errorf("stdout %q does not contain %q", res.Stdout, want)
			}
			if got, _ := os.Readlink("/proc/self/fd/0"); got != stdin {
				t.Errorf("descriptor 0 is %q after the run, want %q", got, stdin)
			}
		})
	}
}

// A reminder with a TZ clause used to leave remind's SysTime variable set to
// the time the program started, so %1 in a reminder triggered from the queue
// gave the interval between the start of the program and the trigger time.
func TestQueuedReminderAfterTZReminderUsesCurrentTime(t *testing.T) {
	if testing.Short() {
		t.Skip("waits up to a minute for a queued reminder")
	}
	// Other tests leave TZ=UTC in the C environment, so use UTC here as well
	// instead of depending on test order.
	cSetenv(t, "TZ", "UTC")
	now := time.Now().UTC()
	at := now.Add(time.Minute)
	if at.Day() != now.Day() {
		t.Skip("the next minute is tomorrow")
	}
	body := "REM 1 Jan 2030 AT 09:00 TZ America/Los_Angeles MSG other-zone\n" +
		"REM AT " + at.Format("15:04") + " MSG queued: %1\n"
	res, err := Run([]string{"-a", "-"}, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit status %d, want 0; stderr: %s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(string(res.Stdout), "queued: now") {
		t.Errorf("stdout %q does not contain %q", res.Stdout, "queued: now")
	}
}

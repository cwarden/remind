// Copyright 2026 The remindgo Authors. All rights reserved.
// Use of this source code is governed by the GNU General Public License,
// Version 2, which can be found in the LICENSE file.

//go:build unix

package libshim // import "github.com/cwarden/remind/v6/libshim"

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"modernc.org/libc"
)

func newTLS(t *testing.T) *libc.TLS {
	t.Helper()
	tls := libc.NewTLS()
	t.Cleanup(tls.Close)
	return tls
}

func cString(t *testing.T, tls *libc.TLS, s string) uintptr {
	t.Helper()
	p, err := libc.CString(s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { libc.Xfree(tls, p) })
	return p
}

func errno(tls *libc.TLS) int32 {
	return *(*int32)(unsafe.Pointer(libc.X__errno_location(tls)))
}

// readAll reads a FILE* to EOF with fgetc.
func readAll(tls *libc.TLS, fp uintptr) string {
	var b strings.Builder
	for {
		c := libc.Xfgetc(tls, fp)
		if c < 0 {
			return b.String()
		}
		b.WriteByte(byte(c))
	}
}

func TestPopenReadsCommandOutput(t *testing.T) {
	tls := newTLS(t)
	fp := Xrem_popen(tls, cString(t, tls, "printf 'a b\\nc'"), cString(t, tls, "r"))
	if fp == 0 {
		t.Fatalf("popen failed, errno %d", errno(tls))
	}
	if got, want := readAll(tls, fp), "a b\nc"; got != want {
		t.Errorf("read %q, want %q", got, want)
	}
	if status := Xrem_pclose(tls, fp); status != 0 {
		t.Errorf("pclose returned %d, want 0", status)
	}
}

func TestPcloseReturnsExitStatus(t *testing.T) {
	tls := newTLS(t)
	fp := Xrem_popen(tls, cString(t, tls, "exit 3"), cString(t, tls, "r"))
	if fp == 0 {
		t.Fatalf("popen failed, errno %d", errno(tls))
	}
	readAll(tls, fp)
	status := Xrem_pclose(tls, fp)
	if got := syscall.WaitStatus(status).ExitStatus(); got != 3 {
		t.Errorf("exit status %d (wait status %d), want 3", got, status)
	}
}

func TestPopenRejectsWriteMode(t *testing.T) {
	tls := newTLS(t)
	fp := Xrem_popen(tls, cString(t, tls, "cat"), cString(t, tls, "w"))
	if fp != 0 {
		Xrem_pclose(tls, fp)
		t.Fatal("popen with mode w succeeded")
	}
	if got := errno(tls); got != libc.EINVAL {
		t.Errorf("errno %d, want EINVAL (%d)", got, libc.EINVAL)
	}
}

func TestPcloseUnknownFile(t *testing.T) {
	tls := newTLS(t)
	if status := Xrem_pclose(tls, 0xdead); status != -1 {
		t.Errorf("pclose of an unknown FILE returned %d, want -1", status)
	}
}

// captureFile swaps *f for a pipe while fn runs and returns what fn wrote.
func captureFile(t *testing.T, f **os.File, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := *f
	*f = w
	done := make(chan string)
	go func() {
		var b bytes.Buffer
		io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	*f = old
	w.Close()
	out := <-done
	r.Close()
	return out
}

func TestSystemDevnullDiscardsStdout(t *testing.T) {
	tls := newTLS(t)
	cmd := cString(t, tls, "echo leaked; echo err >&2")
	var stderr string
	stdout := captureFile(t, &os.Stdout, func() {
		stderr = captureFile(t, &os.Stderr, func() {
			Xrem_system_devnull(tls, cmd, 0)
		})
	})
	if stdout != "" {
		t.Errorf("stdout got %q, want nothing", stdout)
	}
	if stderr != "err\n" {
		t.Errorf("stderr got %q, want %q", stderr, "err\n")
	}
}

func TestSystemDevnullRedirectsStdoutToStderr(t *testing.T) {
	tls := newTLS(t)
	cmd := cString(t, tls, "echo leaked")
	stderr := captureFile(t, &os.Stderr, func() {
		Xrem_system_devnull(tls, cmd, 1)
	})
	if stderr != "leaked\n" {
		t.Errorf("stderr got %q, want %q", stderr, "leaked\n")
	}
}

// TestLimitFires runs itself as a child process that sets a one-second
// limit and then sleeps; the child must exit with status 1 and the C
// handler's message.
func TestLimitFires(t *testing.T) {
	if os.Getenv("LIBSHIM_LIMIT_CHILD") == "1" {
		tls := libc.NewTLS()
		Xrem_limit_execution_time(tls, 1)
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestLimitFires$")
	cmd.Env = append(os.Environ(), "LIBSHIM_LIMIT_CHILD=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)
	var ee *exec.ExitError
	if !asExitError(err, &ee) {
		t.Fatalf("child did not exit with an error: %v", err)
	}
	if ee.ExitCode() != 1 {
		t.Errorf("exit status %d, want 1", ee.ExitCode())
	}
	if !strings.Contains(stderr.String(), limitMessage) {
		t.Errorf("stderr %q does not contain %q", stderr.String(), limitMessage)
	}
	if elapsed > 5*time.Second {
		t.Errorf("limit took %v to fire", elapsed)
	}
}

func asExitError(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*target = ee
	}
	return ok
}

func TestUnlimitCancelsLimit(t *testing.T) {
	tls := newTLS(t)
	Xrem_limit_execution_time(tls, 1)
	Xrem_unlimit_execution_time(tls)
	Xrem_unlimit_execution_time(tls)
	time.Sleep(1500 * time.Millisecond)
}

func TestExitHookReceivesStatus(t *testing.T) {
	tls := newTLS(t)
	var got int32 = -1
	SetExitHook(func(status int32) {
		got = status
		panic("exit")
	})
	defer SetExitHook(nil)
	func() {
		defer func() {
			if r := recover(); r != "exit" {
				t.Errorf("recovered %v, want the hook's panic", r)
			}
		}()
		Xrem_exit(tls, 7)
		t.Error("Xrem_exit returned")
	}()
	if got != 7 {
		t.Errorf("hook received %d, want 7", got)
	}
}

// exitHandlerCalls counts calls to the C-callable test handler.
var exitHandlerCalls []int

func exitHandlerA(tls *libc.TLS) { exitHandlerCalls = append(exitHandlerCalls, 1) }
func exitHandlerB(tls *libc.TLS) { exitHandlerCalls = append(exitHandlerCalls, 2) }

func TestAtexitHandlersRunInReverseOrderDuringInProcessRun(t *testing.T) {
	tls := newTLS(t)
	SetExitHook(func(int32) { panic("exit") })
	defer SetExitHook(nil)
	exitHandlerCalls = nil
	fa := *(*uintptr)(unsafe.Pointer(&struct{ f func(*libc.TLS) }{exitHandlerA}))
	fb := *(*uintptr)(unsafe.Pointer(&struct{ f func(*libc.TLS) }{exitHandlerB}))
	if rc := Xrem_atexit(tls, fa); rc != 0 {
		t.Fatalf("atexit returned %d", rc)
	}
	if rc := Xrem_atexit(tls, fb); rc != 0 {
		t.Fatalf("atexit returned %d", rc)
	}
	RunExitHandlers(tls)
	if got := fmt.Sprint(exitHandlerCalls); got != "[2 1]" {
		t.Errorf("handlers ran as %s, want [2 1]", got)
	}
	RunExitHandlers(tls)
	if len(exitHandlerCalls) != 2 {
		t.Errorf("handlers ran again: %v", exitHandlerCalls)
	}
}

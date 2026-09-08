// Copyright 2026 The remindgo Authors. All rights reserved.
// Use of this source code is governed by the GNU General Public License,
// Version 2, which can be found in the LICENSE file.

//go:build unix

// Package libshim implements the rem_* hooks that the __CCGO__ branches of
// the Remind C sources call in place of fork(), popen() and pclose().
//
// ccgo links this package as -lshim: every exported function whose first
// parameter is *libc.TLS is visible to the generated code as the C function
// of the same name without the X prefix.
package libshim // import "github.com/cwarden/remind/v6/libshim"

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"modernc.org/libc"
)

// limitMessage is the text the C sigxcpu handler in main.c writes before
// exiting.
const limitMessage = "\n\nmax-execution-time exceeded.\n\n"

var (
	mu sync.Mutex
	// cmds maps a FILE* returned by Xrem_popen to its running command.
	cmds = map[uintptr]*exec.Cmd{}
	// limiter fires when --max-execution-time is exceeded.
	limiter *time.Timer
	// modeRead is the C string "r".
	modeRead uintptr
)

func init() {
	p, err := libc.CString("r")
	if err != nil {
		panic(err)
	}
	modeRead = p
}

func setErrno(tls *libc.TLS, e int32) {
	*(*int32)(unsafe.Pointer(libc.X__errno_location(tls))) = e
}

// errnoOf maps a Go error from os/exec or syscall to a C errno value.
func errnoOf(err error) int32 {
	var en syscall.Errno
	if errors.As(err, &en) {
		return int32(en)
	}
	if errors.Is(err, exec.ErrNotFound) {
		return libc.ENOENT
	}
	return libc.EAGAIN
}

// Xrem_popen runs cmd with /bin/sh -c and returns a FILE* open for reading
// the command's standard output. Only mode "r" is supported; Remind never
// opens a command for writing. On failure it returns NULL with errno set.
func Xrem_popen(tls *libc.TLS, cmd, mode uintptr) uintptr {
	if libc.GoString(mode) != "r" {
		setErrno(tls, libc.EINVAL)
		return 0
	}

	// The read end is handed to musl's fdopen, which owns and closes it.
	// An *os.File wrapper on that end would close the same descriptor
	// number again from its finalizer, so only the write end is wrapped.
	var fds [2]int
	if err := syscall.Pipe2(fds[:], syscall.O_CLOEXEC); err != nil {
		setErrno(tls, errnoOf(err))
		return 0
	}

	w := os.NewFile(uintptr(fds[1]), "pipe")
	c := exec.Command("/bin/sh", "-c", libc.GoString(cmd))
	c.Stdin = os.Stdin
	c.Stdout = w
	c.Stderr = os.Stderr
	err := c.Start()
	w.Close()
	if err != nil {
		syscall.Close(fds[0])
		setErrno(tls, errnoOf(err))
		return 0
	}

	fp := libc.Xfdopen(tls, int32(fds[0]), modeRead)
	if fp == 0 {
		syscall.Close(fds[0])
		c.Process.Kill()
		c.Wait()
		return 0
	}

	mu.Lock()
	cmds[fp] = c
	mu.Unlock()
	return fp
}

// Xrem_pclose closes a FILE* returned by Xrem_popen, waits for the command,
// and returns its wait status as pclose does: 0 on success, the raw wait
// status when the command exited with an error, and -1 when fp is unknown.
func Xrem_pclose(tls *libc.TLS, fp uintptr) int32 {
	mu.Lock()
	c, ok := cmds[fp]
	delete(cmds, fp)
	mu.Unlock()
	if !ok {
		setErrno(tls, libc.EINVAL)
		return -1
	}

	libc.Xfclose(tls, fp)
	err := c.Wait()
	if err == nil {
		return 0
	}

	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
			return int32(ws)
		}
	}
	return -1
}

// Xrem_system_devnull runs cmd with /bin/sh -c, standard input from
// /dev/null, standard output to /dev/null (or to standard error when
// stdoutToStderr is nonzero), and waits for it. It replaces the fork()
// in the C System() function for queued reminders in server mode.
func Xrem_system_devnull(tls *libc.TLS, cmd uintptr, stdoutToStderr int32) {
	c := exec.Command("/bin/sh", "-c", libc.GoString(cmd))
	if stdoutToStderr != 0 {
		c.Stdout = os.Stderr
	}
	c.Stderr = os.Stderr
	c.Run()
}

// Xrem_limit_execution_time arranges for the process to print the
// max-execution-time message and exit with status 1 after the given number
// of seconds, replacing the watchdog child process the C build forks.
func Xrem_limit_execution_time(tls *libc.TLS, seconds int32) {
	mu.Lock()
	defer mu.Unlock()
	if limiter != nil {
		limiter.Stop()
	}
	limiter = time.AfterFunc(time.Duration(seconds)*time.Second, func() {
		os.Stderr.WriteString(limitMessage)
		os.Exit(1)
	})
}

// ExitHook receives the status of an exit() call made while remind runs
// inside another Go program. It must not return; the in-process runner
// panics with the status and recovers it.
type ExitHook func(status int32)

var exitHook ExitHook

// SetExitHook installs the function Xrem_exit calls instead of ending the
// process. A nil hook restores the normal exit behaviour.
func SetExitHook(h ExitHook) {
	mu.Lock()
	defer mu.Unlock()
	exitHook = h
}

// exitHandlers holds the functions registered with atexit() during an
// in-process run, in registration order.
var exitHandlers []uintptr

// Xrem_atexit implements atexit() for the __CCGO__ build. During an
// in-process run (an exit hook is set) it records the handler for
// RunExitHandlers; otherwise it registers the handler with libc.
func Xrem_atexit(tls *libc.TLS, fn uintptr) int32 {
	mu.Lock()
	inProcess := exitHook != nil
	if inProcess {
		exitHandlers = append(exitHandlers, fn)
	}
	mu.Unlock()
	if inProcess {
		return 0
	}
	return libc.Xatexit(tls, fn)
}

// RunExitHandlers runs the handlers recorded by Xrem_atexit during the
// current in-process run, last registered first, as exit() would, and
// forgets them.
func RunExitHandlers(tls *libc.TLS) {
	mu.Lock()
	handlers := exitHandlers
	exitHandlers = nil
	mu.Unlock()
	for i := len(handlers) - 1; i >= 0; i-- {
		(*(*func(*libc.TLS))(unsafe.Pointer(&struct{ uintptr }{handlers[i]})))(tls)
	}
}

// Xrem_signal implements signal() for the __CCGO__ build. During an
// in-process run it installs nothing and reports SIG_DFL: remind's handlers
// would apply to the whole host process, and the cooperative signal check
// libc adds to every function return once a handler exists slows a run by
// about a tenth. Otherwise it defers to libc.
func Xrem_signal(tls *libc.TLS, sig int32, handler uintptr) uintptr {
	mu.Lock()
	inProcess := exitHook != nil
	mu.Unlock()
	if inProcess {
		return libc.SIG_DFL
	}
	return libc.Xsignal(tls, sig, handler)
}

// Xrem_exit implements exit() for the __CCGO__ build. Without an exit hook
// it ends the process through libc, running atexit handlers as exit does.
func Xrem_exit(tls *libc.TLS, status int32) {
	mu.Lock()
	h := exitHook
	mu.Unlock()
	if h != nil {
		h(status)
	}
	libc.Xexit(tls, status)
}

// Xrem_unlimit_execution_time cancels a pending execution time limit. It is
// safe to call when no limit is set; Remind calls it from its exit handler.
func Xrem_unlimit_execution_time(tls *libc.TLS) {
	mu.Lock()
	defer mu.Unlock()
	if limiter != nil {
		limiter.Stop()
		limiter = nil
	}
}

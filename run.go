// Copyright 2026 The remindgo Authors. All rights reserved.
// Use of this source code is governed by the GNU General Public License,
// Version 2, which can be found in the LICENSE file.

//go:build linux

package remind // import "github.com/cwarden/remind/v6"

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/cwarden/remind/v6/libshim"
	"modernc.org/libc"
)

// Result is the outcome of one in-process run of the remind program.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// exitStatus is the panic value that carries an exit() status out of the C
// code to Run.
type exitStatus int32

var runMu sync.Mutex

// Builtin reports whether the remind program is compiled into this binary
// on the current platform, so that Run works.
const Builtin = true

// Run runs the remind program inside this process with args as its
// command-line arguments (without argv[0]) and stdin as its standard input,
// and returns what it wrote to standard output and standard error together
// with its exit status.
//
// Remind is not reentrant, so runs are serialized. Between runs the parsed
// file cache is discarded, so files edited since the previous run are read
// again. The program's standard streams are redirected inside the C
// library; the process's own file descriptors are not touched.
//
// Limits: a run that queues timed reminders without -q waits for them in
// this process; --max-execution-time ends the whole process when it
// expires; and abort() is fatal, as in C.
func Run(args []string, stdin []byte) (*Result, error) {
	runMu.Lock()
	defer runMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	in, err := tempFile(stdin)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	out, err := tempFile(nil)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	errf, err := tempFile(nil)
	if err != nil {
		return nil, err
	}
	defer errf.Close()

	tls := libc.NewTLS()
	defer tls.Close()
	argv, freeArgs, err := cArgs(tls, append([]string{"remind"}, args...))
	if err != nil {
		return nil, err
	}
	defer freeArgs()

	// Remind's option parsing assumes the globals hold their C initial
	// values, which only a fresh process guarantees.
	resetGlobals()
	applySysDir()

	libc.Xfflush(tls, 0)
	restore := redirect(tls, in, out, errf)
	libshim.SetExitHook(func(status int32) { panic(exitStatus(status)) })
	rc, exited := runMain(tls, int32(len(args)+1), argv)
	libshim.RunExitHandlers(tls)
	libshim.SetExitHook(nil)
	libc.Xfflush(tls, 0)
	restore()

	// Free what the run allocated where remind has code for it: the
	// per-iteration cleanup it uses for repeated runs (only after a normal
	// return, when its structures are consistent) and the file cache. The
	// execution time limit is cancelled again in case exit() ran before the
	// atexit handler that cancels it was registered.
	if !exited {
		XPerIterationInit(tls)
	}
	Xrem_flush_file_cache(tls)
	libshim.Xrem_unlimit_execution_time(tls)
	// remind installs handlers for these with signal(), which libc backs
	// with os/signal.Notify on a channel owned by the TLS discarded above.
	signal.Reset(syscall.SIGALRM, syscall.SIGXCPU)

	res := &Result{ExitCode: int(rc)}
	if res.Stdout, err = readBack(out); err != nil {
		return nil, err
	}
	if res.Stderr, err = readBack(errf); err != nil {
		return nil, err
	}
	return res, nil
}

// runMain runs the C main and reports its exit status and whether it
// ended through exit() rather than by returning.
func runMain(tls *libc.TLS, argc int32, argv uintptr) (rc int32, exited bool) {
	defer func() {
		if r := recover(); r != nil {
			status, ok := r.(exitStatus)
			if !ok {
				panic(r)
			}
			rc = int32(status)
			exited = true
		}
	}()
	return Xmain(tls, argc, argv), false
}

// tempFile returns an unlinked temporary file holding content, positioned
// at its start.
func tempFile(content []byte) (*os.File, error) {
	f, err := os.CreateTemp("", "remind-run-")
	if err != nil {
		return nil, err
	}
	os.Remove(f.Name())
	if len(content) > 0 {
		if _, err := f.Write(content); err != nil {
			f.Close()
			return nil, err
		}
		if _, err := f.Seek(0, 0); err != nil {
			f.Close()
			return nil, err
		}
	}
	return f, nil
}

func readBack(f *os.File) ([]byte, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	return readFile(f)
}

func readFile(f *os.File) ([]byte, error) {
	var b []byte
	buf := make([]byte, 64*1024)
	for {
		n, err := f.Read(buf)
		b = append(b, buf[:n]...)
		if err != nil {
			if err.Error() == "EOF" {
				return b, nil
			}
			return b, err
		}
	}
}

// redirect points the C library's stdin, stdout and stderr at the given
// files by replacing the descriptor inside each FILE object, and returns a
// function that restores them. printf and friends write through these
// objects rather than through the process's descriptors 0, 1 and 2.
func redirect(tls *libc.TLS, in, out, errf *os.File) (restore func()) {
	stdin := &libc.X__stdin_FILE
	stdout := &libc.X__stdout_FILE
	stderr := &libc.X__stderr_FILE
	saved := [3]int32{stdin.Ffd, stdout.Ffd, stderr.Ffd}
	resetStdin := func() {
		// Drop buffered input and the end-of-file flag left by a
		// previous run.
		libc.Xclearerr(tls, libc.Xstdin)
		stdin.Frpos, stdin.Frend = 0, 0
	}
	resetStdin()
	stdin.Ffd = int32(in.Fd())
	stdout.Ffd = int32(out.Fd())
	stderr.Ffd = int32(errf.Fd())
	return func() {
		stdin.Ffd, stdout.Ffd, stderr.Ffd = saved[0], saved[1], saved[2]
		resetStdin()
	}
}

// cArgs builds a NULL-terminated C argv from args.
func cArgs(tls *libc.TLS, args []string) (argv uintptr, free func(), err error) {
	n := len(args)
	argv = libc.Xmalloc(tls, libc.Tsize_t((n+1)*int(unsafe.Sizeof(uintptr(0)))))
	if argv == 0 {
		return 0, nil, fmt.Errorf("remind: cannot allocate argv")
	}
	ptrs := unsafe.Slice((*uintptr)(unsafe.Pointer(argv)), n+1)
	free = func() {
		for _, p := range ptrs[:n] {
			if p != 0 {
				libc.Xfree(tls, p)
			}
		}
		libc.Xfree(tls, argv)
	}
	for i, a := range args {
		p, err := libc.CString(a)
		if err != nil {
			free()
			return 0, nil, err
		}
		ptrs[i] = p
	}
	ptrs[n] = 0
	return argv, free, nil
}

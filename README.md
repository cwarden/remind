# remind

Package `github.com/cwarden/remind/v6` is a ccgo/v4 (modernc.org/ccgo) version
of [Remind](https://dianne.skoll.ca/projects/remind/), the calendar and alarm
program by Dianne Skoll. The C sources of Remind 06.03.02 are transpiled to
Go and linked against modernc.org/libc, so the result builds with the Go
toolchain alone.

The module is licensed under the GNU General Public License, Version 2, the
same terms as Remind. See LICENSE and LICENSE-REMIND.

## Installing

```
go install github.com/cwarden/remind/v6/cmd/remind@latest
```

The binary accepts the same options as the C `remind` and produces the same
output. Generated code is included for Linux on 386, amd64, arm, arm64,
loong64, ppc64le, riscv64 and s390x. Other operating systems need the
generator run natively there (see Regenerating); the package still compiles
on them, with `Builtin` false and `Run` returning `ErrUnsupported`.

`rem2ps`, `rem2html`, `rem2pdf` and `tkremind` are not included.

## Using remind as a library

`Run` executes the remind program inside the calling process:

```go
res, err := remind.Run([]string{"-pppq", "-l", "-g", "-b2", file, "Mar", "1", "2024"}, nil)
// res.Stdout, res.Stderr, res.ExitCode
```

The second argument is the program's standard input. Runs are serialized,
because remind is not reentrant, and each run starts from the state of a
fresh process: every global of the generated code is reset, files cached by
the previous run are read again, and `exit()` and `atexit` handlers work as
they would in a separate process. Output is captured inside the C library,
so the calling program's own standard streams are unaffected.

Limits: a run that queues timed reminders (no `-q`) waits for them in the
calling process; `--max-execution-time` ends the whole process when it
expires; `abort()` is fatal, as in C; and the C environment is a copy of
the process environment taken at startup. `Builtin` reports whether `Run`
is available on the current platform.

## Using it from urd

[urd](https://github.com/cwarden/urd) imports this module and runs remind
in its own process through `Run` (see below), so no `remind` binary is
needed where the generated code is available. On other platforms urd runs
the external program named by `remind_command` in `~/.urdrc`.

## Differences from the C build

- Timed reminders that would run in a background process are handled in the
  foreground, as with `-f`. Run `remind file &` to get the shell back.
- Interactive input from a terminal has no line editing (no GNU Readline).
- `-u user` changes the user and group id but does not call `initgroups`.
- Signals are delivered when a transpiled function returns, so the
  `$ExpressionTimeLimit` alarm and SIGINT/SIGHUP in `-z` mode take effect
  slightly later than in C.
- `--max-execution-time=n` stops the program after exactly n seconds; the C
  watchdog polls once per second and stops it between n and n+2 seconds.

The system include directory (`$SysInclude`, used by `INCLUDE [...]`) is
`/usr/local/share/remind`. Override it with the `REMIND_SYSDIR` environment
variable, or at build time with

```
go build -ldflags "-X github.com/cwarden/remind/v6.DefaultSysDir=/usr/share/remind" ./cmd/remind
```

## Regenerating

The generated files `ccgo_<goos>_<goarch>.go` and, derived from each,
`reset_<goos>_<goarch>.go` (the function that gives every global its
initial value before a run) are produced from the release tarball by
`generator.go`. Regenerating needs a C compiler (for `./configure`),
`make`, `sh`, `patch`, `tar`, `wget` and `gofmt`:

```
make download        # fetch remind-06.03.02.tar.gz
make generate        # transpile for the host GOOS/GOARCH
make generate-all    # transpile for every supported Linux architecture
```

Linux targets can be generated on any Linux host because modernc.org/libc
ships their C headers. Other operating systems must run `make generate`
natively; the generator turns off inotify for them.

Environment variables honoured by the generator:

| Variable | Effect |
| --- | --- |
| `GO_GENERATE_DIR` | work directory (the Makefile uses /tmp/remindgo) |
| `GO_GENERATE_KEEP` | keep the work directory |
| `GO_GENERATE_DEV` | emit ccgo debugging aids and use `../libc` and `../ccgo/v4` checkouts |
| `GO_GENERATE_GOOS`, `GO_GENERATE_GOARCH` | target platform (default: the host's) |

Two runs of the generator on the same inputs produce files that differ only
in the order of the string literal table (and therefore in the offsets that
refer to it); ccgo assigns those offsets in map iteration order. Regenerate
and commit only when the sources, the patch, the ccgo flags, or the pinned
ccgo and libc versions change.

The C sources get small changes under `#ifdef __CCGO__`, kept in
`internal/patches/ccgo-hooks.patch`: `popen`, `pclose`, `exit` and `atexit`
are routed to Go implementations, `sigaction` is replaced by `signal`, the
three `fork` sites (background reminders, server-mode RUN commands, and the
`--max-execution-time` watchdog) call Go hooks, and `files.c` gains a
function that discards the parsed-file cache. The hooks are implemented in
the `libshim` package, which ccgo links with `-lshim`. The patch is authored
on the `go-port` branch of a Remind checkout next to this repository and
exported with `make patch`.

## Testing

```
make download
go test ./...
```

The tests in the root package extract the tarball, build the C `remind` and
`rem2ps` from it for reference, build the Go `remind`, and then run the
upstream acceptance suite (`tests/test-rem`), the timezone suite
(`tests/test-timezone-support`), a comparison of the C and Go binaries on
the command lines urd uses, and a comparison of repeated in-process `Run`
calls with fresh C processes. They need a C compiler, `make`, a non-root
user, and the system zoneinfo database, and skip when the tarball is absent.
The `Run` tests that need no reference binary always run.

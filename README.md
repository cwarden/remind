# remind

Package `github.com/cwarden/remind` is a ccgo/v4 (modernc.org/ccgo) version
of [Remind](https://dianne.skoll.ca/projects/remind/), the calendar and alarm
program by Dianne Skoll. The C sources of Remind 06.03.02 are transpiled to
Go and linked against modernc.org/libc, so the result builds with the Go
toolchain alone.

The module is licensed under the GNU General Public License, Version 2, the
same terms as Remind. See LICENSE and LICENSE-REMIND.

## Installing

```
go install github.com/cwarden/remind/cmd/remind@latest
```

The binary accepts the same options as the C `remind` and produces the same
output. Only linux/amd64 is generated at the moment.

`rem2ps`, `rem2html`, `rem2pdf` and `tkremind` are not included.

## Using it from urd

[urd](https://github.com/cwarden/urd) runs `remind` as a subprocess. Point it
at the Go binary in `~/.urdrc`:

```
set remind_command="/home/you/go/bin/remind"
```

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
go build -ldflags "-X github.com/cwarden/remind.DefaultSysDir=/usr/share/remind" ./cmd/remind
```

## Regenerating

The generated file `ccgo_linux_amd64.go` is produced from the release tarball
by `generator.go`. Regenerating needs a C compiler (for `./configure`),
`make`, `sh`, `patch`, `tar`, `wget` and `gofmt`:

```
make download    # fetch remind-06.03.02.tar.gz
make generate    # transpile into ccgo_linux_amd64.go
```

Environment variables honoured by the generator:

| Variable | Effect |
| --- | --- |
| `GO_GENERATE_DIR` | work directory (the Makefile uses /tmp/remindgo) |
| `GO_GENERATE_KEEP` | keep the work directory |
| `GO_GENERATE_DEV` | emit ccgo debugging aids and use `../libc` and `../ccgo/v4` checkouts |

The C sources get five small changes under `#ifdef __CCGO__`, kept in
`internal/patches/ccgo-hooks.patch`: `popen` and `pclose` are routed to Go
implementations, `sigaction` is replaced by `signal`, and the three `fork`
sites (background reminders, server-mode RUN commands, and the
`--max-execution-time` watchdog) call Go hooks. The hooks are implemented in
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
(`tests/test-timezone-support`), and a comparison of the C and Go binaries
on the command lines urd uses. They need a C compiler, `make`, a non-root
user, and the system zoneinfo database, and skip when the tarball is absent.

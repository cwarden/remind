// Copyright 2026 The remindgo Authors. All rights reserved.
// Use of this source code is governed by the GNU General Public License,
// Version 2, which can be found in the LICENSE file.

//go:build ignore

// Command generator transpiles the Remind C sources to Go with ccgo/v4.
//
// It extracts the upstream release tarball into a work directory, applies
// internal/patches/ccgo-hooks.patch, runs ./configure, generates src/xlat.c,
// and links the remind program into a single Go file, ccgo_<goos>_<goarch>.go.
//
// Environment:
//
//	GO_GENERATE_DIR   work directory (default: a fresh temporary directory)
//	GO_GENERATE_KEEP  keep the work directory when set
//	GO_GENERATE_DEV   emit ccgo debugging aids and use ../libc and ../ccgo/v4
//	                  checkouts next to this repository when present
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	ccgo "modernc.org/ccgo/v4/lib"
	util "modernc.org/fileutil/ccgo"
)

const (
	version     = "06.03.02"
	archivePath = "remind-" + version + ".tar.gz"
	patchPath   = "internal/patches/ccgo-hooks.patch"
	modulePath  = "github.com/cwarden/remind"
	// sysDir is the compiled-in $SysInclude directory, the same value
	// ./configure --prefix=/usr/local gives the C build.
	sysDir = "/usr/local/share/remind"
)

var (
	goos   = runtime.GOOS
	goarch = runtime.GOARCH
	target = fmt.Sprintf("%s/%s", goos, goarch)
	sed    = "sed"

	// remindSources is REMINDSRCS from src/Makefile.in, followed by the
	// generated xlat.c.
	remindSources = []string{
		"calendar.c", "dedupe.c", "dynbuf.c", "dorem.c", "dosubst.c", "expr.c",
		"files.c", "funcs.c", "globals.c", "hashtab.c", "hashtab_stats.c",
		"hbcal.c", "ifelse.c", "init.c", "main.c", "markup.c", "md5.c", "moon.c",
		"omit.c", "queue.c", "sort.c", "token.c", "trans.c", "trigger.c",
		"userfns.c", "utils.c", "var.c", "xlat.c",
	}

	// configureEnv disables features modernc.org/libc does not provide.
	configureEnv = []string{
		"ac_cv_lib_readline_readline=no",
		"ac_cv_func_readline=no",
		"ac_cv_header_readline_readline_h=no",
		"ac_cv_header_readline_history_h=no",
		"ac_cv_func_initgroups=no",
	}
)

func fail(rc int, msg string, args ...any) {
	fmt.Fprintln(os.Stderr, strings.TrimSpace(fmt.Sprintf(msg, args...)))
	os.Exit(rc)
}

func main() {
	if goos != "linux" {
		fail(1, "unsupported target: %s", target)
	}

	if _, err := os.Stat(archivePath); err != nil {
		fail(1, "cannot open %s (run make download): %v", archivePath, err)
	}

	repoRoot := util.MustAbsCwd(true)
	absPatch := filepath.Join(repoRoot, patchPath)
	extracted := "remind-" + version
	tempDir := os.Getenv("GO_GENERATE_DIR")
	dev := os.Getenv("GO_GENERATE_DEV") != ""
	switch {
	case tempDir != "":
		util.MustShell(true, nil, "sh", "-c", fmt.Sprintf("rm -rf %s", filepath.Join(tempDir, extracted)))
	default:
		var err error
		if tempDir, err = os.MkdirTemp("", "remind-generate-"); err != nil {
			fail(1, "creating temp dir: %v", err)
		}

		defer func() {
			switch os.Getenv("GO_GENERATE_KEEP") {
			case "":
				os.RemoveAll(tempDir)
			default:
				fmt.Printf("%s: temporary directory kept\n", tempDir)
			}
		}()
	}
	libRoot := filepath.Join(tempDir, extracted)
	fmt.Fprintf(os.Stderr, "archivePath %s\n", archivePath)
	fmt.Fprintf(os.Stderr, "tempDir %s\n", tempDir)
	fmt.Fprintf(os.Stderr, "libRoot %s\n", libRoot)
	util.MustShell(true, nil, "tar", "xzf", archivePath, "-C", tempDir)
	util.MustShell(true, nil, "patch", "-p1", "-d", libRoot, "-i", absPatch)
	util.MustCopyFile(false, "LICENSE-REMIND", filepath.Join(libRoot, "COPYRIGHT"), nil)
	result := "remind.go"
	util.MustInDir(true, libRoot, func() (err error) {
		// ccgo resolves modernc.org/libc and the shim package through the Go
		// module system from its working directory. A workspace joining a
		// throwaway module here with this repository makes both visible while
		// keeping the source paths in the generated header relative.
		util.MustShell(true, nil, "sh", "-c", fmt.Sprintf("go mod init example.com/remind && go work init . %s", repoRoot))
		if dev {
			for _, dir := range []string{"../libc", "../ccgo/v4"} {
				dir = filepath.Join(repoRoot, dir)
				if _, err := os.Stat(dir); err == nil {
					util.MustShell(true, nil, "go", "work", "use", dir)
				}
			}
		}
		util.MustShell(true, nil, "sh", "-c", strings.Join(configureEnv, " ")+" ./configure --prefix=/usr/local")
		util.MustShell(true, nil, "make", "-C", "src", "xlat.c")
		args := []string{os.Args[0]}
		if dev {
			args = append(args,
				"-absolute-paths",
				"-keep-object-files",
				"-positions",
			)
		}
		args = append(args,
			"--prefix-enumerator=_",
			"--prefix-external=x_",
			"--prefix-field=F",
			"--prefix-macro=m_",
			"--prefix-static-internal=_",
			"--prefix-static-none=_",
			"--prefix-tagged-enum=_",
			"--prefix-tagged-struct=T",
			"--prefix-tagged-union=T",
			"--prefix-typename=T",
			"--prefix-undefined=_",
			"-extended-errors",
			"-ignore-unsupported-alignment",
			"-DSYSDIR="+sysDir,
			"-Isrc",
			"-L"+modulePath,
			"-lshim",
			"-o", result,
			"--package-name", "remind",
		)
		for _, f := range remindSources {
			args = append(args, "src/"+f)
		}
		if err := ccgo.NewTask(goos, goarch, args, os.Stdout, os.Stderr, nil).Main(); err != nil {
			return err
		}

		util.MustShell(true, nil, sed, "-i", `s/\<T__\([a-zA-Z0-9][a-zA-Z0-9_]\+\)/t__\1/g`, result)
		util.MustShell(true, nil, sed, "-i", `s/\<x_\([a-zA-Z0-9_][a-zA-Z0-9_]\+\)/X\1/g`, result)
		// ccgo leaves the import qualifier tag on __builtin_ references
		// (remainder() in moon.c expands to rint()); libc provides them
		// under their plain names.
		util.MustShell(true, nil, sed, "-i", `s/\<iqlibc\.X__builtin_\([a-zA-Z0-9_]\+\)/libc.X\1/g`, result)
		return nil
	})

	fn := fmt.Sprintf("ccgo_%s_%s.go", goos, goarch)
	util.MustCopyFile(false, fn, filepath.Join(libRoot, result), nil)
	util.MustShell(true, nil, "gofmt", "-l", "-s", "-w", ".")
	util.MustShell(true, nil, "go", "build", "./...")
	util.Shell(nil, "git", "status")
}

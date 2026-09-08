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
//	GO_GENERATE_DIR     work directory (default: a fresh temporary directory)
//	GO_GENERATE_KEEP    keep the work directory when set
//	GO_GENERATE_DEV     emit ccgo debugging aids and use ../libc and ../ccgo/v4
//	                    checkouts next to this repository when present
//	GO_GENERATE_GOOS    target operating system (default: the host's)
//	GO_GENERATE_GOARCH  target architecture (default: the host's)
//
// Cross-generation works between Linux targets, whose C headers ship with
// modernc.org/libc; other operating systems must generate natively.
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
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
	goos   = util.Env("GO_GENERATE_GOOS", runtime.GOOS)
	goarch = util.Env("GO_GENERATE_GOARCH", runtime.GOARCH)
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
	if goos != runtime.GOOS && runtime.GOOS != "linux" {
		fail(1, "cannot generate for %s on %s", target, runtime.GOOS)
	}
	if goos != "linux" {
		// inotify is Linux-only; ./configure runs on the host.
		configureEnv = append(configureEnv,
			"ac_cv_header_sys_inotify_h=no",
			"ac_cv_func_inotify_init1=no",
		)
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
		if err := os.MkdirAll(tempDir, 0o755); err != nil {
			fail(1, "%v", err)
		}
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
	writeResetFile(fn, fmt.Sprintf("reset_%s_%s.go", goos, goarch))
	util.MustShell(true, nil, "gofmt", "-l", "-s", "-w", ".")
	util.MustShell(true, nil, "env", "GOOS="+goos, "GOARCH="+goarch, "go", "build", "./...")
	util.Shell(nil, "git", "status")
}

// writeResetFile derives from the generated file a function that assigns
// every package-level variable its initial value again and replays the
// generated init functions. Remind expects each run to start from the
// state of a fresh process; Run calls the function before the C main.
func writeResetFile(generated, out string) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, generated, nil, 0)
	if err != nil {
		fail(1, "parsing %s: %v", generated, err)
	}

	var body bytes.Buffer
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			if len(vs.Values) != 0 && len(vs.Values) != len(vs.Names) {
				fail(1, "%s: unsupported var declaration at %v", generated, fset.Position(vs.Pos()))
			}
			for i, name := range vs.Names {
				// String literal tables never change.
				if name.Name == "_" || strings.HasPrefix(name.Name, "__ccgo_ts") {
					continue
				}
				var expr string
				switch {
				case len(vs.Values) != 0:
					expr = nodeSource(fset, vs.Values[i])
				default:
					expr = "*new(" + nodeSource(fset, vs.Type) + ")"
				}
				fmt.Fprintf(&body, "\t%s = %s\n", name.Name, expr)
			}
		}
	}

	// ccgo fills function pointer fields of tables such as Func and
	// SysVarArr in init functions rather than in the literals.
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Name.Name != "init" || fd.Body == nil {
			continue
		}
		body.WriteString("\t{\n")
		for _, stmt := range fd.Body.List {
			fmt.Fprintf(&body, "\t\t%s\n", nodeSource(fset, stmt))
		}
		body.WriteString("\t}\n")
	}

	var w bytes.Buffer
	fmt.Fprintf(&w, "// Code generated by generator.go from %s. DO NOT EDIT.\n\n", generated)
	fmt.Fprintf(&w, "//go:build %s && %s\n\npackage remind\n\nimport (\n", goos, goarch)
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if bytes.Contains(body.Bytes(), []byte(name+".")) {
			fmt.Fprintf(&w, "\t%s\n", imp.Path.Value)
		}
	}
	w.WriteString(")\n\n// resetGlobals assigns every package-level variable of the generated code\n// its initial value, so that a run of the C main starts from the state of a\n// fresh process.\nfunc resetGlobals() {\n")
	w.Write(body.Bytes())
	w.WriteString("}\n")
	if err := os.WriteFile(out, w.Bytes(), 0o644); err != nil {
		fail(1, "%v", err)
	}
}

func nodeSource(fset *token.FileSet, n ast.Node) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, fset, n); err != nil {
		fail(1, "%v", err)
	}
	return b.String()
}

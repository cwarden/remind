// Copyright 2026 The remindgo Authors. All rights reserved.
// Use of this source code is governed by the GNU General Public License,
// Version 2, which can be found in the LICENSE file.

//go:build linux

package remind // import "github.com/cwarden/remind"

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	// Keep the generator's dependencies in go.mod; generator.go itself is
	// excluded from the build.
	_ "modernc.org/cc/v4"
	_ "modernc.org/ccgo/v4/lib"
	_ "modernc.org/fileutil/ccgo"
)

const (
	version     = "06.03.02"
	archivePath = "remind-" + version + ".tar.gz"
)

// tree is the extracted and configured release tarball, the C reference
// binaries built from it, and the Go remind built from this module.
type testTree struct {
	root     string
	goRemind string
	cRemind  string
	cRem2ps  string
}

var (
	tree     *testTree
	treeSkip string
)

func TestMain(m *testing.M) {
	flag.Parse()
	tempDir, err := setup()
	if err != nil {
		treeSkip = err.Error()
	}
	rc := m.Run()
	if tempDir != "" {
		os.RemoveAll(tempDir)
	}
	os.Exit(rc)
}

// setup builds everything the tests need. It returns a temporary directory
// to remove afterwards and an error describing why the tests must be skipped.
func setup() (tempDir string, err error) {
	if testing.Short() {
		return "", fmt.Errorf("skipped in -short mode")
	}
	if os.Getuid() == 0 {
		return "", fmt.Errorf("the Remind test suite refuses to run as root")
	}
	if _, err := os.Stat(archivePath); err != nil {
		return "", fmt.Errorf("%s not found (run make download)", archivePath)
	}
	for _, tool := range []string{"sh", "cc", "make", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			return "", fmt.Errorf("%s not found", tool)
		}
	}

	if tempDir, err = os.MkdirTemp("", "remindgo-test-"); err != nil {
		return "", err
	}

	root := filepath.Join(tempDir, "remind-"+version)
	steps := []*exec.Cmd{
		exec.Command("tar", "xzf", archivePath, "-C", tempDir),
		inDir(root, "sh", "./configure"),
		inDir(root, "make", "-C", "src", "remind", "rem2ps"),
		exec.Command("go", "build", "-o", filepath.Join(tempDir, "remind"), "./cmd/remind"),
	}
	for _, cmd := range steps {
		if out, err := cmd.CombinedOutput(); err != nil {
			os.RemoveAll(tempDir)
			return "", fmt.Errorf("%v: %v\n%s", cmd.Args, err, out)
		}
	}
	tree = &testTree{
		root:     root,
		goRemind: filepath.Join(tempDir, "remind"),
		cRemind:  filepath.Join(root, "src", "remind"),
		cRem2ps:  filepath.Join(root, "src", "rem2ps"),
	}
	return tempDir, nil
}

func inDir(dir, name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd
}

func requireTree(t *testing.T) *testTree {
	t.Helper()
	if tree == nil {
		t.Skip(treeSkip)
	}
	return tree
}

// runSuite runs one of the upstream shell test scripts with the Go remind
// and fails with the script's output, which ends with a diff against the
// expected output, when it reports a failure.
func runSuite(t *testing.T, script string, env ...string) {
	t.Helper()
	tr := requireTree(t)
	cmd := inDir(tr.root, "sh", script)
	cmd.Env = append(os.Environ(), env...)
	cmd.Env = append(cmd.Env, "REMIND_CMD="+tr.goRemind, "REM2PS="+tr.cRem2ps)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", script, err, out)
	}
	t.Logf("%s", lastLines(out, 3))
}

func lastLines(b []byte, n int) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// TestAcceptance runs tests/test-rem, the upstream acceptance suite.
func TestAcceptance(t *testing.T) {
	runSuite(t, "tests/test-rem")
}

// TestTimezones runs tests/test-timezone-support, which needs the system
// zoneinfo database.
func TestTimezones(t *testing.T) {
	if _, err := os.Stat("/usr/share/zoneinfo/Europe/Amsterdam"); err != nil {
		t.Skip("zoneinfo not installed")
	}
	runSuite(t, "tests/test-timezone-support")
}

// TestUrdInvocations runs remind the way urd (github.com/cwarden/urd) does
// and requires the Go and C binaries to produce identical output.
func TestUrdInvocations(t *testing.T) {
	tr := requireTree(t)
	files := []string{"tests/test.rem", "tests/todos.rem", "tests/tstlang.rem", "tests/yearfold.rem"}
	dates := [][]string{
		{"Jan", "1", "2024"},
		{"Feb", "1", "2024"},
		{"Aug", "1", "2025"},
		{"Feb", "1", "2026"},
	}
	flagSets := [][]string{
		{"-pppq", "-l", "-g", "-b2"},
		{"-n", "-b1"},
	}
	for _, file := range files {
		for _, date := range dates {
			for _, flags := range flagSets {
				args := append(append([]string{}, flags...), file)
				args = append(args, date...)
				// urd passes no time; a fixed time keeps the two runs
				// from disagreeing across a minute boundary.
				args = append(args, "12:00")
				compareRuns(t, tr, args, "")
			}
		}
	}
	compareRuns(t, tr, []string{"-n"}, "REM MSG test\n")
}

// TestRunMatchesSubprocess runs a sequence of differing invocations through
// Run in this process and requires each to match the C binary run afresh,
// which checks that state does not leak from one in-process run into the
// next.
func TestRunMatchesSubprocess(t *testing.T) {
	tr := requireTree(t)
	for _, v := range []string{"TZ=UTC", "LANG=C.UTF-8", "LC_ALL=C.UTF-8"} {
		kv := strings.SplitN(v, "=", 2)
		t.Setenv(kv[0], kv[1])
		cSetenv(t, kv[0], kv[1])
	}
	abs := func(file string) string { return filepath.Join(tr.root, file) }
	cases := [][]string{
		{"-pppq", "-l", "-g", "-b2", abs("tests/test.rem"), "Jan", "1", "2024", "12:00"},
		{"-n", "-b1", abs("tests/todos.rem"), "Feb", "1", "2024", "12:00"},
		{"-s", abs("tests/tstlang.rem"), "Aug", "1", "2025", "12:00"},
		{"-c", abs("tests/yearfold.rem"), "Feb", "1", "2026", "12:00"},
		{"-q", abs("tests/test.rem"), "16", "feb", "1991", "12:13"},
		{"-pppq", "-l", "-g", "-b2", abs("tests/todos.rem"), "Aug", "1", "2025", "12:00"},
		{"-n", "-b1", abs("tests/test.rem"), "Jan", "1", "2024", "12:00"},
		{"-q", abs("tests/nosuchfile.rem"), "1", "Jan", "2024"},
		{"-pppq", "-l", "-g", "-b2", abs("tests/test.rem"), "Feb", "1", "2024", "12:00"},
	}
	for i := 0; i < 2; i++ {
		for _, args := range cases {
			name := strings.Join(args, " ")
			cOut, cErr, cRC := runRemind(t, tr, tr.cRemind, args, "")
			res, err := Run(args, nil)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if res.ExitCode != cRC {
				t.Errorf("%s: exit status C=%d Run=%d", name, cRC, res.ExitCode)
			}
			if !bytes.Equal(cOut, res.Stdout) {
				t.Errorf("%s: stdout differs\n%s", name, unifiedDiff(t, cOut, res.Stdout))
			}
			if !bytes.Equal(cErr, res.Stderr) {
				t.Errorf("%s: stderr differs\n%s", name, unifiedDiff(t, cErr, res.Stderr))
			}
		}
	}
}

func compareRuns(t *testing.T, tr *testTree, args []string, stdin string) {
	t.Helper()
	name := strings.Join(args, " ")
	cOut, cErr, cRC := runRemind(t, tr, tr.cRemind, args, stdin)
	gOut, gErr, gRC := runRemind(t, tr, tr.goRemind, args, stdin)
	if cRC != gRC {
		t.Errorf("%s: exit status C=%d Go=%d", name, cRC, gRC)
	}
	if !bytes.Equal(cOut, gOut) {
		t.Errorf("%s: stdout differs\n%s", name, unifiedDiff(t, cOut, gOut))
	}
	if !bytes.Equal(cErr, gErr) {
		t.Errorf("%s: stderr differs\n%s", name, unifiedDiff(t, cErr, gErr))
	}
}

func runRemind(t *testing.T, tr *testTree, bin string, args []string, stdin string) (stdout, stderr []byte, rc int) {
	t.Helper()
	cmd := inDir(tr.root, bin, args...)
	cmd.Env = append(os.Environ(), "TZ=UTC", "LANG=C.UTF-8", "LC_ALL=C.UTF-8")
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		var ee *exec.ExitError
		if !errorsAs(err, &ee) {
			t.Fatalf("%s %v: %v", bin, args, err)
		}
		rc = ee.ExitCode()
	}
	return out.Bytes(), errb.Bytes(), rc
}

func errorsAs(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*target = ee
	}
	return ok
}

// unifiedDiff returns diff -u of the C and Go outputs, or both outputs when
// diff is unavailable.
func unifiedDiff(t *testing.T, c, g []byte) string {
	t.Helper()
	dir := t.TempDir()
	cf := filepath.Join(dir, "c")
	gf := filepath.Join(dir, "go")
	os.WriteFile(cf, c, 0o600)
	os.WriteFile(gf, g, 0o600)
	out, err := exec.Command("diff", "-u", cf, gf).CombinedOutput()
	if err == nil || len(out) > 0 {
		return string(out)
	}
	return fmt.Sprintf("C:\n%s\nGo:\n%s", c, g)
}

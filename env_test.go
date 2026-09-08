// Copyright 2026 The remindgo Authors. All rights reserved.
// Use of this source code is governed by the GNU General Public License,
// Version 2, which can be found in the LICENSE file.

//go:build linux

package remind // import "github.com/cwarden/remind/v6"

import (
	"testing"

	"modernc.org/libc"
)

// cSetenv sets a variable in the C library's environment, which remind
// reads with getenv.
func cSetenv(t *testing.T, key, value string) {
	t.Helper()
	tls := libc.NewTLS()
	defer tls.Close()
	k, err := libc.CString(key)
	if err != nil {
		t.Fatal(err)
	}
	v, err := libc.CString(value)
	if err != nil {
		t.Fatal(err)
	}
	if libc.Xsetenv(tls, k, v, 1) != 0 {
		t.Fatalf("setenv %s failed", key)
	}
}

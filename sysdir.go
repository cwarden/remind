// Copyright 2026 The remindgo Authors. All rights reserved.
// Use of this source code is governed by the GNU General Public License,
// Version 2, which can be found in the LICENSE file.

//go:build linux

package remind // import "github.com/cwarden/remind"

import (
	"os"

	"modernc.org/libc"
)

// DefaultSysDir overrides the compiled-in system include directory
// ($SysInclude, /usr/local/share/remind) when set at link time:
//
//	go build -ldflags "-X github.com/cwarden/remind.DefaultSysDir=/usr/share/remind"
//
// The REMIND_SYSDIR environment variable takes precedence over it.
var DefaultSysDir string

// sysDir is the override installed by SetSysDir, or 0 for the compiled-in
// directory.
var sysDir uintptr

// SetSysDir replaces the system include directory used for INCLUDE [...]
// and reported by $SysInclude. It must be called before Xmain runs.
func SetSysDir(dir string) error {
	p, err := libc.CString(dir)
	if err != nil {
		return err
	}
	sysDir = p
	applySysDir()
	return nil
}

// applySysDir installs the override, if any, into the C global.
func applySysDir() {
	if sysDir != 0 {
		XSysDir = sysDir
	}
}

func init() {
	if dir := os.Getenv("REMIND_SYSDIR"); dir != "" {
		SetSysDir(dir)
		return
	}
	if DefaultSysDir != "" {
		SetSysDir(DefaultSysDir)
	}
}

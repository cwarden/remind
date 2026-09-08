// Copyright 2026 The remindgo Authors. All rights reserved.
// Use of this source code is governed by the GNU General Public License,
// Version 2, which can be found in the LICENSE file.

//go:build !linux

package remind // import "github.com/cwarden/remind"

import "errors"

// Builtin reports whether the remind program is compiled into this binary
// on the current platform, so that Run works.
const Builtin = false

// Result is the outcome of one in-process run of the remind program.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// ErrUnsupported is returned by Run on platforms without generated code.
var ErrUnsupported = errors.New("remind: not built in on this platform")

// Run is not available on this platform.
func Run(args []string, stdin []byte) (*Result, error) {
	return nil, ErrUnsupported
}

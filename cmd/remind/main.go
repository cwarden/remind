// Copyright 2026 The remindgo Authors. All rights reserved.
// Use of this source code is governed by the GNU General Public License,
// Version 2, which can be found in the LICENSE file.

// Command remind is the Remind calendar program, transpiled from C to Go.
package main

import (
	"github.com/cwarden/remind"
	"modernc.org/libc"
)

func main() {
	libc.Start(remind.Xmain)
}

// Copyright 2026 The remindgo Authors. All rights reserved.
// Use of this source code is governed by the GNU General Public License,
// Version 2, which can be found in the LICENSE file.

//go:generate go run generator.go

// Package remind is a ccgo/v4 (modernc.org/ccgo) version of the Remind
// calendar program by Dianne Skoll (https://dianne.skoll.ca/projects/remind/).
//
// The generated code exports the C program's entry point as Xmain; the
// cmd/remind program wraps it with libc.Start.
package remind // import "github.com/cwarden/remind"

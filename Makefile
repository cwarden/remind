# Copyright 2026 The remindgo Authors. All rights reserved.
# Use of this source code is governed by the GNU General Public License,
# Version 2, which can be found in the LICENSE file.

.PHONY:	all build clean dev download editor generate generate-all lint patch test work

# Keep the exit status of pipelines such as `go run generator.go | tee`.
SHELL = /bin/bash
.SHELLFLAGS = -o pipefail -c

DIR = /tmp/remindgo
VERSION = 06.03.02
TAR = remind-$(VERSION).tar.gz
URL = https://dianne.skoll.ca/projects/remind/download/$(TAR)
# Checkout of https://salsa.debian.org/dskoll/remind.git whose go-port branch
# carries the __CCGO__ hook edits.
REMIND_SRC = ../remind

all: editor

build:
	go build -o remind ./cmd/remind

clean:
	rm -f log-* cpu.test mem.test *.out go.work* remind
	go clean

# The generated package is excluded from vet; the unsafe.Pointer check is
# disabled because the libc calling convention passes C pointers as uintptr.
lint:
	go vet -unsafeptr=false ./cmd/... ./libshim

editor:
	gofmt -l -s -w . 2>&1 | tee log-editor
	go test -c -o /dev/null 2>&1 | tee -a log-editor
	go build -v ./... 2>&1 | tee -a log-editor
	go build -o /dev/null generator.go

download:
	@if [ ! -f $(TAR) ]; then wget $(URL) ; fi

# Export the __CCGO__ hook edits from the remind checkout as a patch that
# applies to the release tarball.
patch:
	git -C $(REMIND_SRC) diff --src-prefix=a/ --dst-prefix=b/ $$(git -C $(REMIND_SRC) merge-base go-port $(VERSION))...go-port -- src > internal/patches/ccgo-hooks.patch

generate: download
	mkdir -p $(DIR) || true
	rm -rf $(DIR)/*
	echo -n > log-generate
	echo -n > log-generate-errors
	GO_GENERATE_DIR=$(DIR) go run generator.go 2> log-generate-errors | tee log-generate
	cat log-generate-errors
	go build -v ./... | tee -a log-generate
	go test -c -o /dev/null | tee -a log-generate
	git status
	grep 'TRC\|TODO\|ERRORF\|FAIL' log-generate || true
	grep 'TRC\|TODO\|ERRORF\|FAIL' log-generate-errors || true

# Every Linux architecture modernc.org/libc ships headers for.
ARCHES = 386 amd64 arm arm64 loong64 ppc64le riscv64 s390x

generate-all: download
	for a in $(ARCHES); do \
		GO_GENERATE_GOARCH=$$a GO_GENERATE_DIR=$(DIR)-$$a go run generator.go || exit 1; \
		rm -rf $(DIR)-$$a; \
	done
	git status

dev: download
	mkdir -p $(DIR) || true
	rm -rf $(DIR)/*
	echo -n > log-generate
	echo -n > log-generate-errors
	GO_GENERATE_DIR=$(DIR) GO_GENERATE_DEV=1 GO_GENERATE_KEEP=1 go run -tags=ccgo.dmesg,ccgo.assert generator.go 2>&1 | tee log-generate
	go build -v ./... | tee -a log-generate
	git status
	grep 'TRC\|TODO\|ERRORF\|FAIL' log-generate || true

test: download
	go test -v -timeout 1h -count=1 ./... 2>&1 | tee log-test

work:
	rm -f go.work*
	go work init
	go work use .
	go work use ../ccgo/v4
	go work use ../cc/v4
	go work use ../libc

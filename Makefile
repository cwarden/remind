# Copyright 2026 The remindgo Authors. All rights reserved.
# Use of this source code is governed by the GNU General Public License,
# Version 2, which can be found in the LICENSE file.

.PHONY:	all build clean dev download editor generate generate-all lint patch test unpack work

# Keep the exit status of pipelines such as `go run generator.go | tee`.
SHELL = /bin/bash
.SHELLFLAGS = -o pipefail -c

DIR = /tmp/remindgo
VERSION = 06.03.02
TAR = remind-$(VERSION).tar.gz
URL = https://dianne.skoll.ca/projects/remind/download/$(TAR)
# Where `make unpack` puts the patched sources for writing a new patch.
SRC_DIR = $(DIR)-src

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

# Unpack the release tarball into $(SRC_DIR) as a git repository and apply
# internal/patches/*.patch in file name order, one commit per patch. Edit the
# sources there, then run `make patch NAME=NN-short-description`.
unpack: download
	rm -rf $(SRC_DIR)
	mkdir -p $(SRC_DIR)
	tar xzf $(TAR) -C $(SRC_DIR) --strip-components=1
	git -C $(SRC_DIR) init -q
	git -C $(SRC_DIR) add -A
	git -C $(SRC_DIR) commit -q -m "remind $(VERSION)"
	for p in internal/patches/*.patch; do \
		git -C $(SRC_DIR) apply --index $(CURDIR)/$$p || exit 1; \
		git -C $(SRC_DIR) commit -q -m "$$(basename $$p)" || exit 1; \
	done

# Write the uncommitted edits in $(SRC_DIR) to internal/patches/$(NAME).patch
# and commit them there, so that the next patch starts from this one. A
# patch written this way applies after every existing patch, so give it the
# highest number.
patch:
	@if [ -z "$(NAME)" ]; then echo "usage: make patch NAME=NN-short-description"; exit 1; fi
	@if [ -e internal/patches/$(NAME).patch ]; then echo "internal/patches/$(NAME).patch exists"; exit 1; fi
	git -C $(SRC_DIR) diff --src-prefix=a/ --dst-prefix=b/ HEAD -- src > internal/patches/$(NAME).patch
	@if [ ! -s internal/patches/$(NAME).patch ]; then rm internal/patches/$(NAME).patch; echo "no edits under $(SRC_DIR)/src"; exit 1; fi
	git -C $(SRC_DIR) commit -q -a -m "$(NAME).patch"

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

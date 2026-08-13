.PHONY: test

GITFS_BIN := $(abspath ../../bin)/gitfs

test:
	cd ../.. && go build -o bin/gitfs ./cmd/gitfs
	GITFS_BIN=$(GITFS_BIN) bash test_gitfs.sh

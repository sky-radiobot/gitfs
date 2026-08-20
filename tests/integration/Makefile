.PHONY: test

GITFS_BIN := $(abspath ../../bin)/gitfs
GIT_LS_BIN := $(abspath ../../bin)/git-ls

test:
	cd ../.. && go build -o bin/gitfs ./cmd/gitfs && go build -o bin/git-ls ./cmd/git-ls
	GITFS_BIN=$(GITFS_BIN) bash test_gitfs.sh
	GITFS_BIN=$(GITFS_BIN) GIT_LS_BIN=$(GIT_LS_BIN) bash test_git_ls.sh

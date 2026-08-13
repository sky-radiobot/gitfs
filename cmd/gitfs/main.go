// Command gitfs is a minimal CLI frontend to the gitfs package. It mainly
// exists as the executable surface for the bash integration tests, and is
// handy for debugging.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"gitfs"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: gitfs [-git-binary PATH] [-sparse p1,p2] REPO SHA OP [ARGS]")
	fmt.Fprintln(os.Stderr, "ops:   cat PATH | ls [PATH] | stat PATH | glob PATTERN")
	os.Exit(2)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gitfs:", err)
	os.Exit(1)
}

func main() {
	gitBinary := flag.String("git-binary", "", "shell out to the git binary at PATH instead of using go-git")
	sparse := flag.String("sparse", "", "comma-separated repo-relative paths to restrict the filesystem to")
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) < 3 {
		usage()
	}
	repo, sha, op, opArgs := args[0], args[1], args[2], args[3:]

	var opts []gitfs.Option
	if *gitBinary != "" {
		opts = append(opts, gitfs.WithGitBinary(*gitBinary))
	}
	if *sparse != "" {
		opts = append(opts, gitfs.WithSparse(strings.Split(*sparse, ",")...))
	}

	fsys, err := gitfs.Open(repo, sha, opts...)
	if err != nil {
		fatal(err)
	}

	switch op {
	case "cat":
		if len(opArgs) != 1 {
			usage()
		}
		data, err := fsys.ReadFile(opArgs[0])
		if err != nil {
			fatal(err)
		}
		if _, err := os.Stdout.Write(data); err != nil {
			fatal(err)
		}
	case "ls":
		dir := "."
		if len(opArgs) == 1 {
			dir = opArgs[0]
		} else if len(opArgs) > 1 {
			usage()
		}
		entries, err := fsys.ReadDir(dir)
		if err != nil {
			fatal(err)
		}
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				fatal(err)
			}
			printInfo(info)
		}
	case "stat":
		if len(opArgs) != 1 {
			usage()
		}
		fi, err := fsys.Stat(opArgs[0])
		if err != nil {
			fatal(err)
		}
		printInfo(fi)
	case "glob":
		if len(opArgs) != 1 {
			usage()
		}
		matches, err := fsys.Glob(opArgs[0])
		if err != nil {
			fatal(err)
		}
		for _, m := range matches {
			fmt.Println(m)
		}
	default:
		usage()
	}
}

// printInfo prints "<mode>\t<size>\t<name>".
func printInfo(fi fs.FileInfo) {
	fmt.Printf("%s\t%d\t%s\n", fi.Mode(), fi.Size(), fi.Name())
}

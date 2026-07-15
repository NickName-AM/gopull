// gopull recursively finds git repositories under a directory and pulls them.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/NickName-AM/gopull/internal/discover"
	"github.com/NickName-AM/gopull/internal/puller"
)

func main() {
	var (
		parallel bool
		jobs     int
		list     bool
		depth    int
		remote   string
		branch   string
		force    bool
	)
	flag.BoolVar(&parallel, "p", false, "pull repositories concurrently")
	flag.BoolVar(&parallel, "parallel", false, "pull repositories concurrently")
	flag.IntVar(&jobs, "j", runtime.NumCPU(), "worker count in parallel mode")
	flag.IntVar(&jobs, "jobs", runtime.NumCPU(), "worker count in parallel mode")
	flag.BoolVar(&list, "l", false, "only list discovered repositories, don't pull")
	flag.BoolVar(&list, "list", false, "only list discovered repositories, don't pull")
	flag.IntVar(&depth, "d", 0, "max directory depth to search (0 = unlimited)")
	flag.IntVar(&depth, "depth", 0, "max directory depth to search (0 = unlimited)")
	flag.StringVar(&remote, "r", "", "remote to pull from (default: each repo's upstream)")
	flag.StringVar(&remote, "remote", "", "remote to pull from (default: each repo's upstream)")
	flag.StringVar(&branch, "b", "", "branch to pull (default: each repo's current branch)")
	flag.StringVar(&branch, "branch", "", "branch to pull (default: each repo's current branch)")
	flag.BoolVar(&force, "f", false, "with -b, merge the branch even into repos checked out on a different branch")
	flag.BoolVar(&force, "force-branch", false, "with -b, merge the branch even into repos checked out on a different branch")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: gopull [flags] [root]\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Recursively find git repositories under root (default \".\") and pull them.\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	// The flag package stops at the first positional argument, so
	// `gopull <root> -b main` would silently ignore the flags. Take the
	// first remaining argument as root and parse the rest as flags.
	root := "."
	if args := flag.Args(); len(args) > 0 {
		root = args[0]
		if len(args) > 1 {
			flag.CommandLine.Parse(args[1:])
			if extra := flag.Args(); len(extra) > 0 {
				fmt.Fprintf(os.Stderr, "gopull: unexpected arguments: %v\n", extra)
				os.Exit(2)
			}
		}
	}

	// git pull <remote> <branch>: a branch needs a remote to go with it.
	if branch != "" && remote == "" {
		remote = "origin"
	}
	opts := puller.Options{Remote: remote, Branch: branch, ForceBranch: force}

	os.Exit(run(root, parallel, jobs, list, depth, opts))
}

func run(root string, parallel bool, jobs int, list bool, depth int, opts puller.Options) int {
	repos, err := discover.Find(root, depth)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gopull: %v\n", err)
		return 1
	}
	if len(repos) == 0 {
		fmt.Fprintf(os.Stderr, "gopull: no git repositories found under %s\n", root)
		return 1
	}

	if list {
		for _, repo := range repos {
			fmt.Println(display(root, repo))
		}
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	report := func(r puller.Result) {
		name := display(root, r.Repo)
		switch r.Status {
		case puller.Updated:
			fmt.Printf("✓ %s (updated, %.1fs)\n", name, r.Duration.Seconds())
		case puller.UpToDate:
			fmt.Printf("· %s (up to date)\n", name)
		case puller.Skipped:
			fmt.Printf("- %s (skipped: %s)\n", name, r.Output)
		case puller.Failed:
			fmt.Printf("✗ %s (failed)\n", name)
		}
	}

	fmt.Printf("Pulling %d repositories", len(repos))
	if parallel {
		fmt.Printf(" with %d workers", min(max(jobs, 1), len(repos)))
	}
	fmt.Println("...")

	var results []puller.Result
	if parallel {
		results = puller.RunParallel(ctx, repos, opts, jobs, report)
	} else {
		results = puller.RunSequential(ctx, repos, opts, report)
	}

	return summarize(root, results)
}

func summarize(root string, results []puller.Result) int {
	var updated, upToDate, skipped int
	var failed []puller.Result
	for _, r := range results {
		switch r.Status {
		case puller.Updated:
			updated++
		case puller.UpToDate:
			upToDate++
		case puller.Skipped:
			skipped++
		case puller.Failed:
			failed = append(failed, r)
		}
	}

	fmt.Printf("\n%d updated, %d up to date, %d skipped, %d failed\n", updated, upToDate, skipped, len(failed))
	if len(failed) == 0 {
		return 0
	}
	for _, r := range failed {
		fmt.Printf("\n✗ %s:\n", display(root, r.Repo))
		for _, line := range strings.Split(r.Output, "\n") {
			fmt.Printf("    %s\n", line)
		}
	}
	return 1
}

// display returns repo relative to root when possible, for shorter output.
func display(root, repo string) string {
	rel, err := filepath.Rel(root, repo)
	if err != nil {
		return repo
	}
	return rel
}

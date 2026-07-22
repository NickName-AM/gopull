// Package puller runs git pull across repositories, sequentially or in parallel.
package puller

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Status is the outcome of pulling one repository.
type Status int

const (
	Updated Status = iota
	UpToDate
	Skipped
	// Canceled means the pull was interrupted, not that it went wrong.
	Canceled
	Failed
)

func (s Status) String() string {
	switch s {
	case Updated:
		return "updated"
	case UpToDate:
		return "up to date"
	case Skipped:
		return "skipped"
	case Canceled:
		return "canceled"
	default:
		return "failed"
	}
}

// Options control how each repository is pulled.
type Options struct {
	// Remote and Branch name an explicit source, as in `git pull origin main`.
	// When empty, each repo pulls its configured upstream.
	Remote string
	Branch string
	// ForceBranch pulls Branch even into repos whose checked-out branch is
	// different (git merges it into the current branch). Without it, such
	// repos fall back to their configured upstream, or are skipped if they
	// have none.
	ForceBranch bool
}

// Result is the outcome of a git pull in one repository.
type Result struct {
	Repo     string // repository path as passed in
	Status   Status
	Output   string // trimmed combined git output
	Duration time.Duration
	Err      error
}

// RunSequential pulls each repo one at a time, calling report as each
// result completes. Once ctx is canceled no further repos are pulled, so
// fewer results than repos come back.
func RunSequential(ctx context.Context, repos []string, opts Options, report func(Result)) []Result {
	results := make([]Result, 0, len(repos))
	for _, repo := range repos {
		if ctx.Err() != nil {
			break
		}
		r := pullOne(ctx, repo, opts)
		report(r)
		results = append(results, r)
	}
	return results
}

// RunParallel pulls repos concurrently using at most jobs workers, calling
// report from a single goroutine as each result completes. Once ctx is
// canceled workers stop taking new repos, so fewer results than repos come
// back.
func RunParallel(ctx context.Context, repos []string, opts Options, jobs int, report func(Result)) []Result {
	if jobs < 1 {
		jobs = 1
	}
	if jobs > len(repos) {
		jobs = len(repos)
	}

	work := make(chan string)
	out := make(chan Result)

	var wg sync.WaitGroup
	for range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for repo := range work {
				if ctx.Err() != nil {
					return
				}
				// Safe to block: the caller ranges over out until it closes.
				out <- pullOne(ctx, repo, opts)
			}
		}()
	}
	go func() {
		// Runs on both exit paths, so out always closes and the caller's
		// range terminates.
		defer func() {
			close(work)
			wg.Wait()
			close(out)
		}()
		for _, repo := range repos {
			select {
			case work <- repo:
			case <-ctx.Done():
				// Workers are on their way out and would never receive.
				return
			}
		}
	}()

	results := make([]Result, 0, len(repos))
	for r := range out {
		report(r)
		results = append(results, r)
	}
	return results
}

func pullOne(ctx context.Context, repo string, opts Options) Result {
	start := time.Now()
	if ctx.Err() != nil {
		return Result{Repo: repo, Status: Canceled}
	}

	var extraArgs []string
	if opts.Remote != "" {
		extraArgs = append(extraArgs, opts.Remote)
	}
	if opts.Branch != "" {
		extraArgs = append(extraArgs, opts.Branch)
	}

	// A named branch is only pulled into repos already on that branch,
	// unless ForceBranch allows merging it into whatever is checked out.
	if opts.Branch != "" && !opts.ForceBranch {
		cur, detached := currentBranch(ctx, repo)
		switch {
		case detached, cur != opts.Branch:
			if !hasUpstream(ctx, repo) {
				where := "detached HEAD"
				if !detached {
					where = "on " + cur
				}
				return Result{
					Repo:     repo,
					Status:   Skipped,
					Output:   where + ", not " + opts.Branch + ", and no upstream configured",
					Duration: time.Since(start),
				}
			}
			extraArgs = nil // fall back to the repo's own upstream
		}
	}

	args := append([]string{"-C", repo, "pull", "--ff-only"}, extraArgs...)
	cmd := exec.CommandContext(ctx, "git", args...)
	// Fail fast on missing credentials instead of hanging on a prompt.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	// SIGTERM rather than the default SIGKILL: git removes its lock files
	// on the way out. WaitDelay kills it if it does not take the hint.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second
	outBytes, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(outBytes))

	r := Result{Repo: repo, Output: output, Duration: time.Since(start)}
	switch {
	case err != nil && ctx.Err() != nil:
		// Interrupted mid-pull; the git output is noise about the kill.
		r.Status = Canceled
	case err != nil:
		r.Status = Failed
		r.Err = err
	case strings.Contains(output, "Already up to date"):
		r.Status = UpToDate
	default:
		r.Status = Updated
	}
	return r
}

// currentBranch returns the checked-out branch name, or detached=true for
// a detached HEAD (or any repo where HEAD can't be resolved to a branch).
func currentBranch(ctx context.Context, repo string) (branch string, detached bool) {
	out, err := exec.CommandContext(ctx, "git", "-C", repo, "symbolic-ref", "--short", "-q", "HEAD").Output()
	if err != nil {
		return "", true
	}
	return strings.TrimSpace(string(out)), false
}

// hasUpstream reports whether the current branch has an upstream configured.
func hasUpstream(ctx context.Context, repo string) bool {
	err := exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}").Run()
	return err == nil
}

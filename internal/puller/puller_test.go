package puller

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// setup creates a bare remote with one commit and returns the remote path
// plus a clone of it.
func setup(t *testing.T) (remote, clone string) {
	t.Helper()
	base := t.TempDir()

	src := filepath.Join(base, "src")
	git(t, base, "init", "-b", "main", src)
	git(t, src, "commit", "--allow-empty", "-m", "initial")

	remote = filepath.Join(base, "remote.git")
	git(t, base, "clone", "--bare", src, remote)

	clone = filepath.Join(base, "clone")
	git(t, base, "clone", remote, clone)
	return remote, clone
}

// advance adds a commit to the remote via a scratch clone.
func advance(t *testing.T, remote string) {
	t.Helper()
	scratch := filepath.Join(t.TempDir(), "scratch")
	git(t, filepath.Dir(scratch), "clone", remote, scratch)
	git(t, scratch, "commit", "--allow-empty", "-m", "advance")
	git(t, scratch, "push")
}

func TestPullOneUpToDate(t *testing.T) {
	_, clone := setup(t)
	r := pullOne(context.Background(), clone, Options{})
	if r.Status != UpToDate {
		t.Errorf("status = %v, want up to date; output: %s (err: %v)", r.Status, r.Output, r.Err)
	}
}

func TestPullOneUpdated(t *testing.T) {
	remote, clone := setup(t)
	advance(t, remote)
	r := pullOne(context.Background(), clone, Options{})
	if r.Status != Updated {
		t.Errorf("status = %v, want updated; output: %s (err: %v)", r.Status, r.Output, r.Err)
	}
}

func TestPullOneExplicitRemoteBranch(t *testing.T) {
	remote, clone := setup(t)
	advance(t, remote)
	r := pullOne(context.Background(), clone, Options{Remote: "origin", Branch: "main"})
	if r.Status != Updated {
		t.Errorf("status = %v, want updated; output: %s (err: %v)", r.Status, r.Output, r.Err)
	}

	r = pullOne(context.Background(), clone, Options{Remote: "origin", Branch: "no-such-branch", ForceBranch: true})
	if r.Status != Failed {
		t.Errorf("status = %v, want failed for missing branch; output: %s", r.Status, r.Output)
	}
}

func TestPullOneBranchGuard(t *testing.T) {
	opts := Options{Remote: "origin", Branch: "main"}

	t.Run("other branch with upstream falls back to upstream", func(t *testing.T) {
		remote, clone := setup(t)
		// Branch that tracks origin/main but is named differently.
		git(t, clone, "checkout", "-b", "feature")
		git(t, clone, "branch", "--set-upstream-to=origin/main")
		advance(t, remote)
		r := pullOne(context.Background(), clone, opts)
		if r.Status != Updated {
			t.Errorf("status = %v, want updated via upstream; output: %s (err: %v)", r.Status, r.Output, r.Err)
		}
	})

	t.Run("other branch without upstream is skipped", func(t *testing.T) {
		_, clone := setup(t)
		// checkout -b does not set an upstream for the new branch.
		git(t, clone, "checkout", "-b", "feature")
		r := pullOne(context.Background(), clone, opts)
		if r.Status != Skipped {
			t.Errorf("status = %v, want skipped; output: %s (err: %v)", r.Status, r.Output, r.Err)
		}
	})

	t.Run("detached HEAD is skipped", func(t *testing.T) {
		_, clone := setup(t)
		git(t, clone, "checkout", "--detach")
		r := pullOne(context.Background(), clone, opts)
		if r.Status != Skipped {
			t.Errorf("status = %v, want skipped; output: %s (err: %v)", r.Status, r.Output, r.Err)
		}
	})

	t.Run("force merges into other branch", func(t *testing.T) {
		remote, clone := setup(t)
		git(t, clone, "checkout", "-b", "feature")
		advance(t, remote)
		r := pullOne(context.Background(), clone, Options{Remote: "origin", Branch: "main", ForceBranch: true})
		if r.Status != Updated {
			t.Errorf("status = %v, want updated; output: %s (err: %v)", r.Status, r.Output, r.Err)
		}
	})
}

func TestPullOneFailedDiverged(t *testing.T) {
	remote, clone := setup(t)
	advance(t, remote)
	// Local commit diverging from the remote makes --ff-only refuse.
	git(t, clone, "commit", "--allow-empty", "-m", "local")
	r := pullOne(context.Background(), clone, Options{})
	if r.Status != Failed {
		t.Errorf("status = %v, want failed; output: %s", r.Status, r.Output)
	}
	if r.Err == nil {
		t.Error("expected non-nil Err for failed pull")
	}
}

func TestPullOneNotARepo(t *testing.T) {
	r := pullOne(context.Background(), t.TempDir(), Options{})
	if r.Status != Failed {
		t.Errorf("status = %v, want failed", r.Status)
	}
}

func TestPullOneCanceled(t *testing.T) {
	_, clone := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := pullOne(ctx, clone, Options{})
	if r.Status != Canceled {
		t.Errorf("status = %v, want canceled; output: %s (err: %v)", r.Status, r.Output, r.Err)
	}
}

func TestKilledBySignal(t *testing.T) {
	// A terminal Ctrl-C reaches git directly, so a pull can die by signal
	// before the context is canceled; that must not read as a failure.
	if err := exec.Command("sh", "-c", "kill -TERM $$").Run(); !killedBySignal(err) {
		t.Errorf("killedBySignal(%v) = false, want true for a signaled process", err)
	}
	if err := exec.Command("sh", "-c", "exit 1").Run(); killedBySignal(err) {
		t.Errorf("killedBySignal(%v) = true, want false for a normal exit", err)
	}
	if killedBySignal(nil) {
		t.Error("killedBySignal(nil) = true, want false")
	}
}

// run calls f in a goroutine and fails the test if it does not return in
// time, so a deadlock in the worker pool is a failure rather than a hang.
func run(t *testing.T, f func() []Result) []Result {
	t.Helper()
	done := make(chan []Result, 1)
	go func() { done <- f() }()
	select {
	case results := <-done:
		return results
	case <-time.After(30 * time.Second):
		t.Fatal("run did not return; workers are likely deadlocked")
		return nil
	}
}

func TestRunCanceledBeforeStart(t *testing.T) {
	var repos []string
	for range 4 {
		_, clone := setup(t)
		repos = append(repos, clone)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, tc := range []struct {
		name string
		run  func(func(Result)) []Result
	}{
		{"sequential", func(report func(Result)) []Result {
			return RunSequential(ctx, repos, Options{}, report)
		}},
		{"parallel", func(report func(Result)) []Result {
			return RunParallel(ctx, repos, Options{}, 3, report)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reported int
			results := run(t, func() []Result { return tc.run(func(Result) { reported++ }) })
			if len(results) != 0 {
				t.Errorf("got %d results, want none for a canceled run", len(results))
			}
			if reported != 0 {
				t.Errorf("report called %d times, want 0", reported)
			}
		})
	}
}

func TestRunCanceledMidway(t *testing.T) {
	var repos []string
	for range 6 {
		_, clone := setup(t)
		repos = append(repos, clone)
	}

	for _, tc := range []struct {
		name string
		run  func(context.Context, func(Result)) []Result
	}{
		{"sequential", func(ctx context.Context, report func(Result)) []Result {
			return RunSequential(ctx, repos, Options{}, report)
		}},
		{"parallel", func(ctx context.Context, report func(Result)) []Result {
			// One worker keeps the stopping point predictable.
			return RunParallel(ctx, repos, Options{}, 1, report)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// Cancel as soon as the first repo is done, as a Ctrl-C would.
			results := run(t, func() []Result {
				return tc.run(ctx, func(Result) { cancel() })
			})
			if len(results) == 0 || len(results) >= len(repos) {
				t.Errorf("got %d results, want between 1 and %d", len(results), len(repos)-1)
			}
		})
	}
}

func TestRunSequentialAndParallel(t *testing.T) {
	var repos []string
	for range 4 {
		_, clone := setup(t)
		repos = append(repos, clone)
	}

	for _, tc := range []struct {
		name string
		run  func(func(Result)) []Result
	}{
		{"sequential", func(report func(Result)) []Result {
			return RunSequential(context.Background(), repos, Options{}, report)
		}},
		{"parallel", func(report func(Result)) []Result {
			return RunParallel(context.Background(), repos, Options{}, 3, report)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reported int
			results := tc.run(func(Result) { reported++ })
			if len(results) != len(repos) {
				t.Fatalf("got %d results, want %d", len(results), len(repos))
			}
			if reported != len(repos) {
				t.Errorf("report called %d times, want %d", reported, len(repos))
			}
			seen := map[string]bool{}
			for _, r := range results {
				seen[r.Repo] = true
				if r.Status != UpToDate {
					t.Errorf("%s: status = %v, want up to date; output: %s", r.Repo, r.Status, r.Output)
				}
			}
			if len(seen) != len(repos) {
				t.Errorf("got results for %d distinct repos, want %d", len(seen), len(repos))
			}
		})
	}
}

package puller

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
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

func TestKilledByInterrupt(t *testing.T) {
	// Where the pull is not isolated in its own process group a terminal
	// Ctrl-C reaches git directly, so a pull can die by signal before the
	// context is canceled; that must not read as a failure.
	for _, sig := range []string{"TERM", "INT"} {
		err := exec.Command("sh", "-c", "kill -"+sig+" $$").Run()
		if !killedByInterrupt(err) {
			t.Errorf("killedByInterrupt(%v) = false, want true for SIG%s", err, sig)
		}
	}
	// Anything else really is a failure: reporting it as canceled would
	// swallow git's output and leave the exit code at 0.
	if err := exec.Command("sh", "-c", "kill -KILL $$").Run(); killedByInterrupt(err) {
		t.Errorf("killedByInterrupt(%v) = true, want false for SIGKILL", err)
	}
	if err := exec.Command("sh", "-c", "exit 1").Run(); killedByInterrupt(err) {
		t.Errorf("killedByInterrupt(%v) = true, want false for a normal exit", err)
	}
	if killedByInterrupt(nil) {
		t.Error("killedByInterrupt(nil) = true, want false")
	}
}

// TestGitHelper is not a test. It is the program fakeGit installs on PATH
// under the name "git", so that the exec plumbing in pullOne can be driven
// against a process whose exit timing and signal handling are known. A normal
// test run skips it, since the environment variable is only set by fakeGit.
//
// Modes that race the interrupt announce themselves by creating the file
// named in GOPULL_TEST_READY once they are set up, so the test can wait
// rather than guess at how long this binary takes to start.
func TestGitHelper(t *testing.T) {
	switch os.Getenv("GOPULL_TEST_GIT") {
	case "":
		t.Skip("only runs as the fake git installed by fakeGit")

	case "holdPipes":
		// Exits cleanly but leaves a process holding the output pipes,
		// the way a backgrounded ssh ControlPersist master does.
		fmt.Println("Updating abc..def")
		hog := exec.Command("sleep", "5")
		// Inheriting the pipes is the whole point: that is what keeps
		// them open past WaitDelay once this process is gone.
		hog.Stdout, hog.Stderr = os.Stdout, os.Stderr
		if err := hog.Start(); err != nil {
			t.Fatal(err)
		}

	case "ignoreTerm":
		// Refuses the interrupt and finishes the pull anyway.
		signal.Ignore(syscall.SIGTERM)
		announce(t)
		time.Sleep(300 * time.Millisecond)
		fmt.Println("Updating abc..def")

	case "spawnChild":
		// Stands in for the fetch and merge that "git pull" spawns: a
		// child that leaves a mark behind unless it is signaled too.
		// Its output goes to /dev/null so it does not hold the pipes as
		// well and blur the two cases together.
		child := exec.Command("sh", "-c", "sleep 1; : > "+os.Getenv("GOPULL_TEST_MARKER"))
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		announce(t)
		time.Sleep(5 * time.Second)
	}
	os.Exit(0)
}

// announce tells the test that the fake git is set up and ready to be
// interrupted. Called from TestGitHelper, in the fake git's own process.
func announce(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(os.Getenv("GOPULL_TEST_READY"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// awaitReady blocks until the fake git has announced itself.
func awaitReady(t *testing.T, ready string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("fake git never started")
}

// fakeGit puts TestGitHelper at the front of PATH under the name "git" for
// the duration of the test, running in the given mode.
func fakeGit(t *testing.T, mode string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nexec " + self + " -test.run='^TestGitHelper$'\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOPULL_TEST_GIT", mode)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// interruptPull runs pullOne against the fake git and cancels its context as
// soon as the fake git is ready, so the interrupt always lands mid-pull.
func interruptPull(t *testing.T, ready string) Result {
	t.Helper()
	repo := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan Result, 1)
	go func() { done <- pullOne(ctx, repo, Options{}) }()

	awaitReady(t, ready)
	cancel()

	select {
	case r := <-done:
		return r
	case <-time.After(30 * time.Second):
		t.Fatal("pullOne did not return after the interrupt")
		return Result{}
	}
}

func TestPullOneSurvivingChildHoldsPipes(t *testing.T) {
	// git exits cleanly but something it spawned keeps the pipes open, so
	// Wait reports ErrWaitDelay rather than nil. That must not turn a good
	// pull into a reported failure.
	fakeGit(t, "holdPipes")

	r := pullOne(context.Background(), t.TempDir(), Options{})
	if r.Status != Updated {
		t.Errorf("status = %v, want updated; output: %s (err: %v)", r.Status, r.Output, r.Err)
	}
	if r.Output != "Updating abc..def" {
		t.Errorf("output = %q, want the git output to survive", r.Output)
	}
}

func TestPullOneFinishesDuringShutdown(t *testing.T) {
	// The pull ignores the interrupt and completes anyway. It really did
	// pull, so it must not be written off as canceled with its output
	// dropped - the user would have no way to tell.
	ready := filepath.Join(t.TempDir(), "ready")
	fakeGit(t, "ignoreTerm")
	t.Setenv("GOPULL_TEST_READY", ready)

	r := interruptPull(t, ready)
	if r.Status != Updated {
		t.Errorf("status = %v, want updated; output: %s (err: %v)", r.Status, r.Output, r.Err)
	}
}

func TestPullOneTerminatesChildProcesses(t *testing.T) {
	// "git pull" holds index.lock through the fetch and merge it spawns,
	// so an interrupt has to reach those too, not just the wrapper.
	tmp := t.TempDir()
	ready, marker := filepath.Join(tmp, "ready"), filepath.Join(tmp, "survived")
	fakeGit(t, "spawnChild")
	t.Setenv("GOPULL_TEST_READY", ready)
	t.Setenv("GOPULL_TEST_MARKER", marker)

	r := interruptPull(t, ready)
	if r.Status != Canceled {
		t.Errorf("status = %v, want canceled; output: %s (err: %v)", r.Status, r.Output, r.Err)
	}

	// Long enough for the child to have reached its mark had it lived.
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("child outlived the interrupt, so it could still be holding index.lock")
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

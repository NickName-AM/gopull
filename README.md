# gopull

Recursively find git repositories under a directory and `git pull` them all,
sequentially or in parallel.

Discovery walks the tree and stops descending once a repository is found, so
repos nested inside other repos (vendored checkouts, submodule working trees)
are ignored. Pulls run `git pull --ff-only` with terminal prompts disabled, so
a repo with diverged branches or broken credentials shows up as a failure in
the summary instead of merging or hanging.

## Install

```sh
go install github.com/NickName-AM/gopull@latest
```

Or from a local checkout:

```sh
go build -o gopull .
```

Requires the `git` binary on PATH - pulls use your normal git config, SSH
keys, and credential helpers.

## Usage

```
gopull [flags] [root]        root defaults to "."

  -p, --parallel   pull repositories concurrently
  -j, --jobs N     worker count in parallel mode (default: number of CPUs)
  -l, --list       only list discovered repositories, don't pull
  -d, --depth N    max directory depth to search (0 = unlimited)
  -r, --remote R      remote to pull from (default: each repo's upstream)
  -b, --branch B      branch to pull (default: each repo's current branch)
  -f, --force-branch  with -b, merge the branch even into repos checked out
                      on a different branch
```

By default each repo pulls its own current branch from its configured
upstream. `-r`/`-b` name an explicit source (`git pull origin main` style);
`-b` alone implies `-r origin`.

With `-b main`, a repo that is **not** on `main` is never merged with
`origin/main`. Instead it falls back to a plain pull of its own configured
upstream, or is reported as skipped if it has none (detached HEADs are
skipped too). Pass `-f`/`--force-branch` to bypass this guard and merge the
named branch into whatever branch each repo has checked out.

Examples:

```sh
gopull ~/Development/projects          # sequential
gopull -p -j 8 ~/Development/projects  # parallel with 8 workers
gopull --list ~/Development/projects   # just show what would be pulled
```

## Interrupting

Press Ctrl-C to stop a run. No further repositories are dispatched, the pulls
already running are stopped, and you still get the summary for everything that
completed - repos whose pull was cut short are reported as `canceled`, and the
ones never started are counted as `not pulled`. A second Ctrl-C quits on the
spot, in case a pull refuses to wind down.

An interrupted pull is safe: git is asked to terminate rather than killed
outright, so it removes its own lock files on the way out. Each pull runs in
its own process group and the whole group is signaled, so the `fetch` and
`merge` that `git pull` spawns - the ones actually holding `index.lock` - are
stopped too, rather than being left behind to finish on their own. A pull that
ignores the interrupt and completes anyway is still reported as `updated`.

Exit code is 0 when every repo pulled cleanly (or was already up to date),
1 if any repo failed or was cut short without an interrupt, and 130 if the run
was interrupted.

## Output

```
Pulling 3 repositories with 3 workers...
· uptodate (up to date)
✓ work/behind (updated, 0.1s)
✗ work/deep/diverged (failed)

1 updated, 1 up to date, 1 failed

✗ work/deep/diverged:
    fatal: Not possible to fast-forward, aborting.
```

Interrupted with Ctrl-C partway through:

```
Pulling 8 repositories with 3 workers...
· fast1 (up to date)
· fast2 (up to date)

interrupt: stopping (Ctrl-C again to force quit)
· slow1 (canceled)

0 updated, 2 up to date, 0 skipped, 0 failed, 1 canceled, 5 not pulled
interrupted
```

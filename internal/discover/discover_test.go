package discover

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// mkRepo creates dir with a .git subdirectory.
func mkRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// mkWorktreeRepo creates dir with a .git file (linked worktree style).
func mkWorktreeRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFind(t *testing.T) {
	root := t.TempDir()
	mkRepo(t, filepath.Join(root, "a"))
	mkRepo(t, filepath.Join(root, "nested", "deep", "b"))
	mkWorktreeRepo(t, filepath.Join(root, "wt"))
	// Repo nested inside a repo must not be found.
	mkRepo(t, filepath.Join(root, "a", "inner"))
	// Hidden and noise dirs must not be searched.
	mkRepo(t, filepath.Join(root, ".hidden", "c"))
	mkRepo(t, filepath.Join(root, "node_modules", "d"))
	// Plain directory with no repos.
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := Find(context.Background(), root, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(root, "a"),
		filepath.Join(root, "nested", "deep", "b"),
		filepath.Join(root, "wt"),
	}
	if !slices.Equal(got, want) {
		t.Errorf("Find = %v, want %v", got, want)
	}
}

func TestFindRootIsRepo(t *testing.T) {
	root := t.TempDir()
	mkRepo(t, root)
	mkRepo(t, filepath.Join(root, "inner"))

	got, err := Find(context.Background(), root, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{root}
	if !slices.Equal(got, want) {
		t.Errorf("Find = %v, want %v", got, want)
	}
}

func TestFindMaxDepth(t *testing.T) {
	root := t.TempDir()
	mkRepo(t, filepath.Join(root, "shallow"))
	mkRepo(t, filepath.Join(root, "one", "two", "deep"))

	got, err := Find(context.Background(), root, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "shallow")}
	if !slices.Equal(got, want) {
		t.Errorf("Find(depth=2) = %v, want %v", got, want)
	}

	got, err = Find(context.Background(), root, 3)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{
		filepath.Join(root, "one", "two", "deep"),
		filepath.Join(root, "shallow"),
	}
	if !slices.Equal(got, want) {
		t.Errorf("Find(depth=3) = %v, want %v", got, want)
	}
}

func TestFindCanceled(t *testing.T) {
	root := t.TempDir()
	mkRepo(t, filepath.Join(root, "a"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Find(ctx, root, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestFindNotADirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Find(context.Background(), file, 0); err == nil {
		t.Error("Find on a file should fail")
	}
	if _, err := Find(context.Background(), filepath.Join(root, "missing"), 0); err == nil {
		t.Error("Find on a missing path should fail")
	}
}

// Package discover walks a directory tree and finds git repositories.
package discover

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Directories never worth descending into.
var skipDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
}

// Find walks root and returns the paths of all git repositories found,
// in lexical order. It does not descend into a repository once found,
// so repos nested inside other repos are not returned. If maxDepth > 0,
// directories more than maxDepth levels below root are not visited.
func Find(root string, maxDepth int) ([]string, error) {
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	if isRepo(root) {
		return []string{root}, nil
	}

	var repos []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", path, err)
				return nil
			}
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path == root {
			return nil
		}

		name := d.Name()
		if strings.HasPrefix(name, ".") || skipDirs[name] {
			return fs.SkipDir
		}

		if isRepo(path) {
			repos = append(repos, path)
			return fs.SkipDir
		}

		if maxDepth > 0 && depth(root, path) >= maxDepth {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return repos, nil
}

// isRepo reports whether dir contains a .git entry. A .git directory is a
// normal repository; a .git file is a linked worktree or submodule checkout,
// which is still pullable.
func isRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func depth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

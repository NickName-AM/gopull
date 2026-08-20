package main

import (
	"os"
	"testing"

	"github.com/NickName-AM/gopull/internal/puller"
)

// TestSummarizeExitCode pins the contract the README states: 0 only when
// every repo was actually pulled.
func TestSummarizeExitCode(t *testing.T) {
	res := func(statuses ...puller.Status) []puller.Result {
		out := make([]puller.Result, len(statuses))
		for i, s := range statuses {
			out[i] = puller.Result{Repo: "repo", Status: s}
		}
		return out
	}

	tests := []struct {
		name        string
		results     []puller.Result
		interrupted bool
		want        int
	}{
		{"all clean", res(puller.Updated, puller.UpToDate, puller.Skipped), false, 0},
		{"a failure", res(puller.Updated, puller.Failed), false, 1},
		{"interrupted", res(puller.Updated, puller.Canceled), true, 130},
		{"interrupted with a failure", res(puller.Failed), true, 130},
		// git died on its own - the repo went unpulled even though gopull
		// was never interrupted, so this must not read as success.
		{"canceled without an interrupt", res(puller.Updated, puller.Canceled), false, 1},
	}

	// summarize writes the tallies; the exit code is what is under test.
	quiet(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repos := make([]string, len(tt.results))
			if got := summarize(".", repos, tt.results, tt.interrupted); got != tt.want {
				t.Errorf("summarize() = %d, want %d", got, tt.want)
			}
		})
	}
}

// quiet sends stdout to /dev/null for the duration of the test.
func quiet(t *testing.T) {
	t.Helper()
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = devNull
	t.Cleanup(func() {
		os.Stdout = saved
		devNull.Close()
	})
}

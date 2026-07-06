// Package gitx shells out to the user's `git` binary so their SSH agents and
// credential helpers keep working. skillx never embeds credentials and never
// talks to any third-party index.
package gitx

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Run executes `git <args...>` in dir, returning stdout. Stderr is captured
// and included in the returned error on failure.
func Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// RunInteractive executes git with stdio attached to the terminal (used for
// clone/push so progress and auth prompts are visible).
func RunInteractive(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// IsRepo reports whether dir is inside a git work tree.
func IsRepo(dir string) bool {
	out, err := Run(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// TopLevel returns the repository root containing dir, or "" if none.
func TopLevel(dir string) string {
	out, err := Run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return out
}

// Clone clones url into dest. With shallow, uses --depth 1. branch may be "".
func Clone(url, dest string, shallow bool, branch string) error {
	args := []string{"clone"}
	if shallow {
		args = append(args, "--depth", "1")
	}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, "--", url, dest)
	return RunInteractive("", args...)
}

// Head returns the current HEAD commit hash of the repo at dir.
func Head(dir string) (string, error) {
	return Run(dir, "rev-parse", "HEAD")
}

// ShortHead returns the abbreviated HEAD commit hash.
func ShortHead(dir string) (string, error) {
	return Run(dir, "rev-parse", "--short", "HEAD")
}

// AddAndCommit stages the given paths (or everything when none are given)
// and commits with msg. Committing with nothing staged is not an error;
// it returns (false, nil).
func AddAndCommit(dir, msg string, paths ...string) (bool, error) {
	addArgs := []string{"add", "--all", "--"}
	if len(paths) > 0 {
		addArgs = append([]string{"add", "--"}, paths...)
	}
	if _, err := Run(dir, addArgs...); err != nil {
		return false, err
	}
	// Anything staged?
	if _, err := Run(dir, "diff", "--cached", "--quiet"); err == nil {
		return false, nil // no changes
	}
	if _, err := Run(dir, "commit", "-m", msg); err != nil {
		return false, err
	}
	return true, nil
}

// Push pushes the current branch to origin.
func Push(dir string) error {
	return RunInteractive(dir, "push")
}

// Fetch runs `git fetch` in dir.
func Fetch(dir string) error {
	_, err := Run(dir, "fetch", "--quiet")
	return err
}

// PullFFOnly fast-forwards the current branch.
func PullFFOnly(dir string) error {
	return RunInteractive(dir, "pull", "--ff-only")
}

// Dirty reports whether the work tree at dir has uncommitted changes
// (including untracked files).
func Dirty(dir string) (bool, error) {
	out, err := Run(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// AheadBehind returns how many commits HEAD is ahead of / behind its
// upstream. Errors if no upstream is configured.
func AheadBehind(dir string) (ahead, behind int, err error) {
	out, err := Run(dir, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output %q", out)
	}
	ahead, err = strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, err
	}
	behind, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, err
	}
	return ahead, behind, nil
}

// LogRange returns `git log --oneline --stat <from>..<to>` output, optionally
// limited to a subpath.
func LogRange(dir, from, to, subpath string) (string, error) {
	args := []string{"log", "--oneline", "--stat", from + ".." + to}
	if subpath != "" {
		args = append(args, "--", subpath)
	}
	return Run(dir, args...)
}

// HasCommit reports whether the repo at dir contains the given commit.
func HasCommit(dir, commit string) bool {
	_, err := Run(dir, "cat-file", "-e", commit+"^{commit}")
	return err == nil
}

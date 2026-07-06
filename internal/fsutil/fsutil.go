// Package fsutil contains small filesystem helpers shared across skillx.
package fsutil

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ExpandPath expands a leading "~" or "~/" to the user's home directory and
// returns a cleaned absolute-ish path. Paths without "~" are returned cleaned.
func ExpandPath(p string) string {
	if p == "" {
		return p
	}
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return filepath.Clean(p)
}

// ContractPath replaces the home directory prefix with "~" for display.
func ContractPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

// CopyDir recursively copies the directory tree at src to dst. dst is created
// if needed. Symlinks inside the tree are re-created as symlinks; ".git"
// directories are skipped (vendored skills must not carry a nested repo).
// An existing dst is not cleared first; use ReplaceDir for that.
func CopyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("copy source %s is not a directory", src)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == ".git" && e.IsDir() {
			continue
		}
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		fi, err := e.Info()
		if err != nil {
			return err
		}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(s)
			if err != nil {
				return err
			}
			_ = os.Remove(d)
			if err := os.Symlink(target, d); err != nil {
				return err
			}
		case fi.IsDir():
			if err := CopyDir(s, d); err != nil {
				return err
			}
		default:
			if err := copyFile(s, d, fi.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	return nil
}

// MoveDir moves the directory tree at src to dst (os.Rename with a
// copy-and-remove fallback for cross-device moves). dst must not exist.
func MoveDir(src, dst string) error {
	if err := checkNoOverlap(src, dst); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := CopyDir(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// ReplaceDir replaces dst with a copy of src. The copy is staged in a
// temporary sibling of dst and swapped in with a rename, so a failed copy
// leaves the old dst untouched. Overlapping src/dst (same directory, or one
// inside the other, symlinks resolved) are refused — removing dst first
// would destroy the source.
func ReplaceDir(src, dst string) error {
	if err := checkNoOverlap(src, dst); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dst), ".skillx-tmp-*")
	if err != nil {
		return err
	}
	// MkdirTemp creates 0700; the temp dir becomes dst, so mirror src's mode.
	if st, err := os.Stat(src); err == nil {
		_ = os.Chmod(tmp, st.Mode().Perm())
	}
	if err := CopyDir(src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.RemoveAll(dst); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	return nil
}

// checkNoOverlap errors when src and dst name the same directory or one
// contains the other, after resolving symlinks best-effort.
func checkNoOverlap(src, dst string) error {
	s, d := resolveBestEffort(src), resolveBestEffort(dst)
	sep := string(filepath.Separator)
	if s == d || strings.HasPrefix(d, s+sep) || strings.HasPrefix(s, d+sep) {
		return fmt.Errorf("source %s and destination %s overlap; refusing", src, dst)
	}
	return nil
}

// resolveBestEffort resolves symlinks in p; components that do not exist yet
// are appended to their deepest resolvable ancestor.
func resolveBestEffort(p string) string {
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(resolveBestEffort(parent), filepath.Base(p))
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

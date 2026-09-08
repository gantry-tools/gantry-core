package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type Status string

const (
	StatusAvailable     Status = "available"
	StatusMissing       Status = "missing"
	StatusInaccessible  Status = "inaccessible"
	StatusOutsideRoot   Status = "outside-root"
	StatusSymlinkEscape Status = "symlink-escape"
)

var ErrEscape = errors.New("path escapes configured root")

type Resolver struct{ root string }

func New(root string) (*Resolver, error) {
	abs, e := filepath.Abs(root)
	if e != nil {
		return nil, e
	}
	real, e := filepath.EvalSymlinks(abs)
	if e != nil {
		return nil, e
	}
	return &Resolver{root: real}, nil
}
func (r *Resolver) Root() string { return r.root }
func (r *Resolver) inside(path string) bool {
	rel, e := filepath.Rel(r.root, path)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
func (r *Resolver) candidate(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "/" {
		return r.root
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(r.root, path)
}
func (r *Resolver) Resolve(path string) (string, error) {
	candidate, e := filepath.Abs(r.candidate(path))
	if e != nil {
		return "", e
	}
	if !r.inside(candidate) {
		return "", ErrEscape
	}
	real, e := filepath.EvalSymlinks(candidate)
	if e != nil {
		return "", errors.New("path is unavailable")
	}
	if !r.inside(real) {
		return "", ErrEscape
	}
	return real, nil
}
func (r *Resolver) ResolveForCreate(path string) (string, error) {
	candidate, e := filepath.Abs(r.candidate(path))
	if e != nil {
		return "", e
	}
	if !r.inside(candidate) {
		return "", ErrEscape
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(candidate))
	if e != nil {
		return "", errors.New("parent is unavailable")
	}
	candidate = filepath.Join(parent, filepath.Base(candidate))
	if !r.inside(candidate) {
		return "", ErrEscape
	}
	return candidate, nil
}
func (r *Resolver) Status(path string) Status {
	candidate, e := filepath.Abs(r.candidate(path))
	if e != nil {
		return StatusInaccessible
	}
	if !r.inside(candidate) {
		return StatusOutsideRoot
	}
	real, e := filepath.EvalSymlinks(candidate)
	if e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return StatusMissing
		}
		return StatusInaccessible
	}
	if !r.inside(real) {
		return StatusSymlinkEscape
	}
	return StatusAvailable
}
func (r *Resolver) Relative(path string) string {
	rel, e := filepath.Rel(r.root, path)
	if e != nil || rel == "." {
		return "/"
	}
	return "/" + filepath.ToSlash(rel)
}

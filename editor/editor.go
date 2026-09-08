package editor

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Query struct {
	Pattern       string `json:"pattern"`
	Regex         bool   `json:"regex"`
	CaseSensitive bool   `json:"caseSensitive"`
}

func Compile(q Query) (*regexp.Regexp, error) {
	if q.Pattern == "" {
		return nil, errors.New("empty search pattern")
	}
	pattern := q.Pattern
	if !q.Regex {
		pattern = regexp.QuoteMeta(pattern)
	}
	if !q.CaseSensitive {
		pattern = "(?i)" + pattern
	}
	return regexp.Compile(pattern)
}
func LooksBinary(b []byte) bool {
	if len(b) > 8192 {
		b = b[:8192]
	}
	return bytes.IndexByte(b, 0) >= 0
}
func Replace(content []byte, q Query, replacement string, limit int) ([]byte, int, error) {
	if LooksBinary(content) {
		return nil, 0, errors.New("binary file")
	}
	re, e := Compile(q)
	if e != nil {
		return nil, 0, e
	}
	matches := re.FindAllIndex(content, -1)
	if limit > 0 && len(matches) > limit {
		return nil, 0, fmt.Errorf("replacement limit exceeded: %d", len(matches))
	}
	return re.ReplaceAll(content, []byte(replacement)), len(matches), nil
}

type Revision struct {
	Size             int64
	ModifiedUnixNano int64
	SHA256           [32]byte
}

func FileRevision(path string) (Revision, error) {
	info, e := os.Stat(path)
	if e != nil {
		return Revision{}, e
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Revision{}, err
	}
	return Revision{Size: info.Size(), ModifiedUnixNano: info.ModTime().UnixNano(), SHA256: sha256.Sum256(data)}, nil
}
func WriteAtomic(path string, data []byte, expected *Revision) error {
	info, e := os.Stat(path)
	mode := os.FileMode(0640)
	if e == nil {
		mode = info.Mode().Perm()
		if expected != nil {
			current, revisionErr := FileRevision(path)
			if revisionErr != nil {
				return revisionErr
			}
			if *expected != current {
				return errors.New("file changed since it was opened")
			}
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	dir := filepath.Dir(path)
	tmp, e := os.CreateTemp(dir, ".gantry-editor-*")
	if e != nil {
		return e
	}
	name := tmp.Name()
	defer os.Remove(name)
	if e = tmp.Chmod(mode); e == nil {
		_, e = tmp.Write(data)
	}
	if e == nil {
		e = tmp.Sync()
	}
	closeErr := tmp.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	return os.Rename(name, path)
}

// WriteAtomicMode atomically replaces path with data using the requested
// permission bits. It is intended for server-side edits that already enforce
// their own optimistic-concurrency policy.
func WriteAtomicMode(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".gantry-editor-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(mode.Perm()); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	return err
}
func LiteralContains(content, needle string, caseSensitive bool) bool {
	if !caseSensitive {
		content, needle = strings.ToLower(content), strings.ToLower(needle)
	}
	return strings.Contains(content, needle)
}

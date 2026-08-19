package gitfs

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"time"
)

var (
	errIsDir       = errors.New("is a directory")
	errNotDir      = errors.New("not a directory")
	errUnsupported = errors.New("unsupported git entry type")
)

// fileInfo is a static fs.FileInfo for a tree entry.
type fileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	g       *GitFS // for lazy Sys() computation; see WithExtendedStats
	path    string // repo-relative path, for lazy Sys() computation
}

func (fi fileInfo) Name() string       { return fi.name }
func (fi fileInfo) Size() int64        { return fi.size }
func (fi fileInfo) Mode() fs.FileMode  { return fi.mode }
func (fi fileInfo) ModTime() time.Time { return fi.modTime }
func (fi fileInfo) IsDir() bool        { return fi.mode.IsDir() }

// Sys returns nil unless the GitFS was opened with WithExtendedStats, in
// which case it returns an *ExtendedStat computed lazily (right here, on
// call), not up front.
func (fi fileInfo) Sys() any {
	if fi.g == nil || !fi.g.extendedStats {
		return nil
	}
	ci, err := fi.g.be.lastCommit(fi.path, fi.g.maxCommits)
	if err != nil {
		return &ExtendedStat{Err: err}
	}
	return &ExtendedStat{Commit: ci.sha, Author: ci.author, AuthorEmail: ci.email, Date: ci.date}
}

// ExtendedStat is returned by an fs.FileInfo's Sys() method when the GitFS
// was opened with WithExtendedStats: the last commit, at or before the
// pinned commit, that touched that entry's path. Err is set (and the
// other fields left zero) if the lookup itself failed.
type ExtendedStat struct {
	Commit      string // full 40-hex commit SHA
	Author      string
	AuthorEmail string
	Date        time.Time
	Err         error
}

// dirEntry adapts fileInfo to fs.DirEntry.
type dirEntry struct {
	fileInfo
}

func (d dirEntry) Type() fs.FileMode          { return d.mode.Type() }
func (d dirEntry) Info() (fs.FileInfo, error) { return d.fileInfo, nil }

// file is an open handle: a regular file or symlink (content != nil) or a
// directory (content == nil, entries loaded lazily).
type file struct {
	info    fileInfo
	g       *GitFS
	path    string        // repo-relative path, for lazy ReadDir and errors
	content *bytes.Reader // blob content; nil for directories
	entries []fs.DirEntry
	listed  bool
	eoff    int
}

var (
	_ fs.File        = (*file)(nil)
	_ fs.ReadDirFile = (*file)(nil)
	_ io.ReaderAt    = (*file)(nil)
	_ io.Seeker      = (*file)(nil)
)

func (f *file) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *file) Close() error               { return nil }

func (f *file) Read(p []byte) (int, error) {
	if f.content == nil {
		return 0, &fs.PathError{Op: "read", Path: f.info.name, Err: errIsDir}
	}
	return f.content.Read(p)
}

func (f *file) ReadAt(p []byte, off int64) (int, error) {
	if f.content == nil {
		return 0, &fs.PathError{Op: "read", Path: f.info.name, Err: errIsDir}
	}
	return f.content.ReadAt(p, off)
}

func (f *file) Seek(offset int64, whence int) (int64, error) {
	if f.content == nil {
		return 0, &fs.PathError{Op: "seek", Path: f.info.name, Err: errIsDir}
	}
	return f.content.Seek(offset, whence)
}

// ReadDir implements fs.ReadDirFile for directory handles.
func (f *file) ReadDir(count int) ([]fs.DirEntry, error) {
	if !f.info.mode.IsDir() {
		return nil, &fs.PathError{Op: "readdir", Path: f.info.name, Err: errNotDir}
	}
	if !f.listed {
		entries, err := f.g.dirEntries(f.path)
		if err != nil {
			return nil, &fs.PathError{Op: "readdir", Path: f.info.name, Err: err}
		}
		f.entries = entries
		f.listed = true
	}
	if count <= 0 {
		rest := f.entries[f.eoff:]
		f.eoff = len(f.entries)
		return rest, nil
	}
	if f.eoff >= len(f.entries) {
		return nil, io.EOF
	}
	n := min(count, len(f.entries)-f.eoff)
	rest := f.entries[f.eoff : f.eoff+n]
	f.eoff += n
	return rest, nil
}

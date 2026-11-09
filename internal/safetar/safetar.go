// Package safetar unpacks .tar.gz archives into a directory without letting an
// entry land outside it or write more bytes than the caller allows.
package safetar

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

var (
	ErrUnsafePath = errors.New("unsafe path in archive")
	ErrTooLarge   = errors.New("archive too large")
)

// Limits caps the bytes Extract writes: File for any one regular file and
// Total for all of them together.
type Limits struct {
	File, Total int64
}

// Extract unpacks the .tar.gz stream r into dir. Entries must be directories,
// regular files, or symlinks with a relative target that never climbs ("..").
// Any other entry, or a file past lim, stops the extraction with an error and
// may leave a partial tree in dir.
func Extract(r io.Reader, dir string, lim Limits) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	return extract(tar.NewReader(gz), dir, lim)
}

// entries is the part of *tar.Reader that extract uses, so tests can feed it
// headers that understate their body.
type entries interface {
	Next() (*tar.Header, error)
	io.Reader
}

func extract(tr entries, dir string, lim Limits) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	var written int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !filepath.IsLocal(h.Name) {
			return fmt.Errorf("%w: %s", ErrUnsafePath, h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = root.MkdirAll(h.Name, 0o755)
		case tar.TypeReg:
			// The limits hold on the bytes copied, not only on what the header claims.
			room := min(lim.File, lim.Total-written)
			if h.Size > room {
				return tooLarge(h.Name, h.Size, lim)
			}
			var n int64
			n, err = writeFile(root, h.Name, io.LimitReader(tr, room+1), fs.FileMode(h.Mode)&0o777)
			if err == nil && n > room {
				return tooLarge(h.Name, n, lim)
			}
			written += n
		case tar.TypeSymlink:
			if !filepath.IsLocal(h.Linkname) {
				return fmt.Errorf("%w: %s -> %s", ErrUnsafePath, h.Name, h.Linkname)
			}
			if err = root.MkdirAll(filepath.Dir(h.Name), 0o755); err == nil {
				err = root.Symlink(h.Linkname, h.Name)
			}
		default:
			return fmt.Errorf("%w: %s (type %c)", ErrUnsafePath, h.Name, h.Typeflag)
		}
		if err != nil {
			return err
		}
	}
}

// tooLarge reports name, at least size bytes long, as past lim.
func tooLarge(name string, size int64, lim Limits) error {
	if size > lim.File {
		return fmt.Errorf("%w: %s is over %d bytes", ErrTooLarge, name, lim.File)
	}
	return fmt.Errorf("%w: %s brings the total over %d bytes", ErrTooLarge, name, lim.Total)
}

func writeFile(root *os.Root, name string, r io.Reader, mode fs.FileMode) (int64, error) {
	if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return 0, err
	}
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	if err != nil {
		f.Close()
		return n, err
	}
	return n, f.Close()
}

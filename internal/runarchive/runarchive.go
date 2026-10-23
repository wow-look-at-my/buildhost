// Package runarchive packs a directory as a zstd-compressed pax tar and extracts one, for the hand-off and cache actions.
// Every host writes the same archive: nanosecond mtimes, symlink targets spelled with /, and a hardlink for a second name.
package runarchive

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
	"github.com/wow-look-at-my/go-containers/set"
)

// A file at most this size is read or written whole by a worker. A larger one streams on the archive's own goroutine.
const smallFile = 256 << 10

// window bounds how many small files the workers hold ahead of the writer.
const window = 256

func idString(dev, ino uint64) string {
	return strconv.FormatUint(dev, 10) + ":" + strconv.FormatUint(ino, 10)
}

type entry struct {
	name  string
	abs   string
	info  fs.FileInfo
	link  string
	hard  bool
	data  []byte
	err   error
	ready chan struct{}
}

func walk(root, member string, out *[]*entry) error {
	abs := root
	if member != "." {
		abs = filepath.Join(root, filepath.FromSlash(member))
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	*out = append(*out, &entry{name: member, abs: abs, info: info})
	if !info.IsDir() {
		return nil
	}
	names, err := readNames(abs)
	if err != nil {
		return err
	}
	for _, child := range names {
		next := child
		if member != "." {
			next = member + "/" + child
		}
		if err := walk(root, next, out); err != nil {
			return err
		}
	}
	return nil
}

func readNames(dir string) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	sort.Strings(names)
	return names, err
}

// tarMode is the permission field a packed entry carries. NT has no posix modes, only a read-only attribute, and the bits
// it reports leave a directory untraversable on a posix host.
func tarMode(mode fs.FileMode) int64 {
	if hostOS() == "windows" {
		switch {
		case mode.IsDir():
			return 0o755
		case mode.Perm()&0o200 == 0:
			return 0o444
		default:
			return 0o644
		}
	}
	bits := int64(mode.Perm())
	if mode&fs.ModeSetuid != 0 {
		bits |= 0o4000
	}
	if mode&fs.ModeSetgid != 0 {
		bits |= 0o2000
	}
	if mode&fs.ModeSticky != 0 {
		bits |= 0o1000
	}
	return bits
}

// Pack writes members of root, by default all of it, to w as a zstd-compressed pax tar.
func Pack(w io.Writer, root string, members []string) error {
	if len(members) == 0 {
		members = []string{"."}
	}
	var entries []*entry
	for _, m := range members {
		if err := walk(root, m, &entries); err != nil {
			return err
		}
	}
	inodes := map[string]string{}
	var small []*entry
	for _, e := range entries {
		mode := e.info.Mode()
		switch {
		case mode&fs.ModeSymlink != 0:
			target, err := os.Readlink(e.abs)
			if err != nil {
				return err
			}
			e.link = filepath.ToSlash(target)
		case mode.IsRegular():
			if id, nlink, ok := fileID(e.info); ok && nlink > 1 {
				if first, seen := inodes[id]; seen {
					e.link, e.hard = first, true
					continue
				}
				inodes[id] = e.name
			}
			if e.info.Size() <= smallFile {
				e.ready = make(chan struct{})
				small = append(small, e)
			}
		}
	}
	release := prefetch(small)

	zw, err := zstd.NewWriter(w, zstd.WithEncoderConcurrency(runtime.GOMAXPROCS(0)))
	if err != nil {
		return err
	}
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		err := writeEntry(tw, e)
		if e.ready != nil {
			release()
		}
		if err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return zw.Close()
}

// prefetch reads the small files on workers per CPU, in archive order. At most window files are read and not yet
// written, so memory stays at window*smallFile. The writer frees a slot as it consumes each file.
func prefetch(small []*entry) (release func()) {
	slots := make(chan struct{}, window)
	jobs := make(chan *entry)
	for range runtime.GOMAXPROCS(0) * 2 {
		go func() {
			for e := range jobs {
				e.data, e.err = os.ReadFile(e.abs)
				close(e.ready)
			}
		}()
	}
	go func() {
		for _, e := range small {
			slots <- struct{}{}
			jobs <- e
		}
		close(jobs)
	}()
	return func() { <-slots }
}

func writeEntry(tw *tar.Writer, e *entry) error {
	mode := e.info.Mode()
	hdr := &tar.Header{Name: e.name, Mode: tarMode(mode), ModTime: e.info.ModTime(), Format: tar.FormatPAX}
	switch {
	case e.hard:
		hdr.Typeflag, hdr.Linkname = tar.TypeLink, e.link
	case mode&fs.ModeSymlink != 0:
		hdr.Typeflag, hdr.Linkname = tar.TypeSymlink, e.link
	case mode.IsDir():
		hdr.Typeflag = tar.TypeDir
		hdr.Name = strings.TrimSuffix(e.name, "/") + "/"
		if e.name == "." {
			hdr.Name = "./"
		}
	case mode.IsRegular():
		hdr.Typeflag, hdr.Size = tar.TypeReg, e.info.Size()
	default:
		fmt.Fprintf(os.Stderr, "runarchive: %s is not a file, directory or symlink, so it is not packed\n", e.abs)
		return nil
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("%s: %w", e.abs, err)
	}
	if hdr.Typeflag != tar.TypeReg {
		return nil
	}
	if e.ready != nil {
		<-e.ready
		if e.err != nil {
			return e.err
		}
		if int64(len(e.data)) != hdr.Size {
			return fmt.Errorf("%s changed size while it was packed (%d of %d bytes)", e.abs, len(e.data), hdr.Size)
		}
		_, err := tw.Write(e.data)
		e.data = nil
		return err
	}
	f, err := os.Open(e.abs)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(tw, f)
	if err == nil && n != hdr.Size {
		err = fmt.Errorf("%s changed size while it was packed (%d of %d bytes)", e.abs, n, hdr.Size)
	}
	return err
}

// inside resolves an archive name under dir, and refuses one that leaves it.
func inside(dir, name string) (string, error) {
	clean := strings.TrimSuffix(strings.TrimLeft(strings.TrimPrefix(name, "./"), "/"), "/")
	if clean == "" || clean == "." {
		return dir, nil
	}
	if path.IsAbs(name) || (len(clean) > 1 && clean[1] == ':') || strings.Contains(clean, "\\") {
		return "", fmt.Errorf("entry %s leaves the directory it extracts into", name)
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".." {
			return "", fmt.Errorf("entry %s leaves the directory it extracts into", name)
		}
	}
	return filepath.Join(dir, filepath.FromSlash(clean)), nil
}

// clear removes what stands at p, a read-only file included, so an entry can take its place.
func clear(p string) error {
	info, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		_ = os.Chmod(p, 0o666)
	}
	return os.RemoveAll(p)
}

func create(p string) (*os.File, error) {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if !errors.Is(err, fs.ErrExist) {
		return f, err
	}
	if err := clear(p); err != nil {
		return nil, err
	}
	return os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

type fileJob struct {
	path string
	data []byte
	hdr  *tar.Header
}

func finishFile(f *os.File, p string, hdr *tar.Header) error {
	if err := f.Chmod(fs.FileMode(hdr.Mode & 0o777)); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chtimes(p, hdr.ModTime, hdr.ModTime)
}

type delayed struct {
	path, target string
	hdr          *tar.Header
}

// Unpack extracts a Pack archive into dir. It also reads the ustar and GNU archives a host tar wrote. Extraction stops at
// the end-of-archive marker. Hardlinks and symlinks are made after every file, so no entry writes through a link, and
// directories get their modes and mtimes after their contents.
func Unpack(r io.Reader, dir string) error {
	zr, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(runtime.GOMAXPROCS(0)))
	if err != nil {
		return err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)

	made := set.New[string]()
	ensureDir := func(d string) error {
		if made.Contains(d) {
			return nil
		}
		// MkdirAll stats first, so an existing drive root is never made.
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
		made.Add(d)
		return nil
	}
	if err := ensureDir(dir); err != nil {
		return err
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}
	failed := func() error {
		mu.Lock()
		defer mu.Unlock()
		return firstErr
	}
	jobs := make(chan fileJob, window)
	for range runtime.GOMAXPROCS(0) * 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				if failed() != nil {
					continue
				}
				f, err := create(job.path)
				if err == nil {
					_, err = f.Write(job.data)
					if err != nil {
						f.Close()
					} else {
						err = finishFile(f, job.path, job.hdr)
					}
				}
				if err != nil {
					fail(fmt.Errorf("%s: %w", job.hdr.Name, err))
				}
			}
		}()
	}
	var dirs, hardlinks, symlinks []delayed
	readErr := func() error {
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if err := failed(); err != nil {
				return err
			}
			p, err := inside(dir, hdr.Name)
			if err != nil {
				return err
			}
			switch hdr.Typeflag {
			case tar.TypeDir:
				if info, err := os.Lstat(p); err == nil && !info.IsDir() {
					if err := clear(p); err != nil {
						return err
					}
				}
				if err := ensureDir(p); err != nil {
					return err
				}
				if p != dir {
					dirs = append(dirs, delayed{path: p, hdr: hdr})
				}
			case tar.TypeReg, tar.TypeCont:
				if err := ensureDir(filepath.Dir(p)); err != nil {
					return err
				}
				if hdr.Size <= smallFile {
					data := make([]byte, hdr.Size)
					if _, err := io.ReadFull(tr, data); err != nil {
						return fmt.Errorf("%s: %w", hdr.Name, err)
					}
					jobs <- fileJob{path: p, data: data, hdr: hdr}
					continue
				}
				f, err := create(p)
				if err != nil {
					return err
				}
				if _, err := io.Copy(f, tr); err != nil {
					f.Close()
					return fmt.Errorf("%s: %w", hdr.Name, err)
				}
				if err := finishFile(f, p, hdr); err != nil {
					return err
				}
			case tar.TypeLink:
				target, err := inside(dir, hdr.Linkname)
				if err != nil {
					return err
				}
				hardlinks = append(hardlinks, delayed{path: p, target: target, hdr: hdr})
			case tar.TypeSymlink:
				symlinks = append(symlinks, delayed{path: p, target: hdr.Linkname, hdr: hdr})
			case tar.TypeXGlobalHeader:
			default:
				return fmt.Errorf("entry %s has type %q, which this archive does not extract", hdr.Name, hdr.Typeflag)
			}
		}
	}()
	close(jobs)
	wg.Wait()
	if readErr != nil {
		return readErr
	}
	if err := failed(); err != nil {
		return err
	}
	for _, l := range hardlinks {
		if err := ensureDir(filepath.Dir(l.path)); err != nil {
			return err
		}
		if err := clear(l.path); err != nil {
			return err
		}
		if err := os.Link(l.target, l.path); err != nil {
			return err
		}
	}
	for _, l := range symlinks {
		if err := ensureDir(filepath.Dir(l.path)); err != nil {
			return err
		}
		if err := clear(l.path); err != nil {
			return err
		}
		if err := os.Symlink(filepath.FromSlash(l.target), l.path); err != nil {
			return err
		}
	}
	sort.Slice(dirs, func(a, b int) bool { return len(dirs[a].path) > len(dirs[b].path) })
	for _, d := range dirs {
		if err := os.Chmod(d.path, fs.FileMode(d.hdr.Mode&0o7777)); err != nil {
			return err
		}
		if err := os.Chtimes(d.path, d.hdr.ModTime, d.hdr.ModTime); err != nil {
			return err
		}
	}
	return nil
}

package repackage

import (
	"context"
	"io"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

type Format string

const (
	FormatTarGZ  Format = "tar.gz"
	FormatTarXZ  Format = "tar.xz"
	FormatTarZST Format = "tar.zst"
	FormatZip    Format = "zip"
	FormatDeb    Format = "deb"
	FormatBrew   Format = "brew"
	FormatNPM    Format = "npm"
	FormatOCI    Format = "oci"
)

type Input struct {
	Project  db.Project
	Release  db.Release
	Artifact db.Artifact
	// Reader streams the artifact bytes (already stripped, when stripping ran). Size is
	Reader io.Reader
	Size   int64
	// TmpDir is scratch space for formats that must spool a member to learn its size
	TmpDir  string
	BaseURL string
	// CacheSuffix distinguishes derived rows this repackage writes into
	CacheSuffix string
	DownloadURL func(name, version string, os db.OS, arch db.Arch, format string) string
}

// SizeUnknown marks an Output whose length is not known up front because the body is
const SizeUnknown int64 = -1

type Output struct {
	Reader   io.ReadCloser
	Filename string
	Size     int64
	Metadata map[string]string
}

// streamPipe runs build in a goroutine, writing the archive into the returned reader.
func streamPipe(build func(w io.Writer) error) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(build(pw))
	}()
	return pr
}

// ChainClose returns rc with extra appended to its Close: closing the result closes rc
func ChainClose(rc io.ReadCloser, extra io.Closer) io.ReadCloser {
	return &chainCloser{ReadCloser: rc, extra: extra}
}

type chainCloser struct {
	io.ReadCloser
	extra io.Closer
}

func (c *chainCloser) Close() error {
	err := c.ReadCloser.Close()
	if cerr := c.extra.Close(); err == nil {
		err = cerr
	}
	return err
}

type Repackager interface {
	Format() Format
	Applicable(artifact db.Artifact) bool
	Repackage(ctx context.Context, input Input) (*Output, error)
}

var registry = map[Format]Repackager{}

func Register(r Repackager) {
	registry[r.Format()] = r
}

func LookupRepackager(f Format) (Repackager, bool) {
	r, ok := registry[f]
	return r, ok
}

func RegisteredFormats() []Format {
	formats := make([]Format, 0, len(registry))
	for f := range registry {
		formats = append(formats, f)
	}
	return formats
}

type Orchestrator struct {
	Store storage.Storage
	DB    *db.DB
}

func NewOrchestrator(store storage.Storage, database *db.DB) *Orchestrator {
	return &Orchestrator{Store: store, DB: database}
}

func (o *Orchestrator) PublishRelease(ctx context.Context, _ db.Project, release db.Release) error {
	return o.DB.PublishRelease(ctx, release.ID)
}

// TransformVersion identifies the byte-level transformation buildhost applies
const TransformVersion = "strip-native-1"

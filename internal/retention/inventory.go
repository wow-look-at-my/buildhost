package retention

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/go-containers/set"
)

// Roles a stored file can have. The role says which table references the blob.
const (
	RoleArtifact = "artifact"
	RoleStripped = "stripped"
	RoleDebug    = "debug"
	RolePackaged = "packaged"
	RoleSite     = "site"
	RoleOCIBlob  = "oci-blob"
	RoleGoproxy  = "goproxy"
)

// Hold values. A hold is the reason retention keeps a file. An empty hold means
// the file is reclaimable: the current policy frees its blob.
const (
	HoldNone       = ""
	HoldBranchTip  = "branch-tip"    // newest published release on its branch
	HoldKeepN      = "keep-n"        // inside the keep-N window on its branch
	HoldRecency    = "recency-guard" // newer than the recency guard
	HoldOCITag     = "oci-tag"       // an OCI tag points at the release
	HoldDocker     = "docker"        // a pushed-docker release, never evicted
	HoldDraft      = "draft"         // a deliberate draft, never swept
	HoldSharedBlob = "shared-blob"   // release evicted, blob still referenced
	HoldSite       = "site"          // a site deployment, not release-scoped
	HoldOCIBlob    = "oci-blob"      // a project OCI blob, not release-scoped
	HoldGoproxy    = "goproxy"       // a cached Go module zip, not a project
	HoldUnknown    = "unknown"       // the plan and this reason disagree
)

type FileEntry struct {
	StorageKey string    `json:"storage_key"`
	SHA256     string    `json:"sha256,omitempty"`
	Role       string    `json:"role"`
	Size       int64     `json:"size"`
	CreatedAt  time.Time `json:"created_at"`
	Project    string    `json:"project,omitempty"`
	ProjectID  int64     `json:"project_id,omitempty"`
	Module     string    `json:"module,omitempty"`
	ReleaseID  int64     `json:"release_id,omitempty"`
	Version    string    `json:"version,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	OS         string    `json:"os,omitempty"`
	Arch       string    `json:"arch,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Format     string    `json:"format,omitempty"`
	MediaType  string    `json:"media_type,omitempty"`
	Filename   string    `json:"filename,omitempty"`
	Published  bool      `json:"published"`
	Draft      bool      `json:"draft,omitempty"`

	// Refs is how many rows reference this storage key. A blob is freed only
	Refs int `json:"refs"`
	// Reclaimable is true when the current policy frees this blob now.
	Reclaimable bool     `json:"reclaimable"`
	Hold        string   `json:"hold"`
	Holds       []string `json:"holds,omitempty"`
}

// InventoryGroup aggregates entries that share a hold reason or a role.
type InventoryGroup struct {
	Name      string `json:"name"`
	Files     int    `json:"files"`
	Bytes     int64  `json:"bytes"`
	Blobs     int    `json:"blobs"`
	BlobBytes int64  `json:"blob_bytes"`
}

// InventoryTotals is the whole-server summary.
type InventoryTotals struct {
	Files            int   `json:"files"`
	Bytes            int64 `json:"bytes"`
	Blobs            int   `json:"blobs"`
	BlobBytes        int64 `json:"blob_bytes"`
	ReclaimableBlobs int   `json:"reclaimable_blobs"`
	ReclaimableBytes int64 `json:"reclaimable_bytes"`
	HeldBlobs        int   `json:"held_blobs"`
	HeldBytes        int64 `json:"held_bytes"`
	Releases         int   `json:"releases"`
	EvictedReleases  int   `json:"evicted_releases"`
	// HoldMismatches counts releases whose hold reason disagrees with the plan.
	HoldMismatches int `json:"hold_mismatches"`
}

// InventoryPolicy is the policy the inventory was computed under.
type InventoryPolicy struct {
	KeepN         int       `json:"keep_n"`
	RecencyHours  float64   `json:"recency_hours"`
	RecencyCutoff time.Time `json:"recency_cutoff"`
}

type Inventory struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Policy      InventoryPolicy  `json:"policy"`
	Totals      InventoryTotals  `json:"totals"`
	ByHold      []InventoryGroup `json:"by_hold"`
	ByRole      []InventoryGroup `json:"by_role"`
	Files       []FileEntry      `json:"files"`
}

// Inventory lists every stored file and explains what retention does with it.
// It changes nothing: the plan behind it is the same dry run the dashboard
// preview shows.
func (r *Retention) Inventory(ctx context.Context) (Inventory, error) {
	now := r.clock()
	cutoff := now.Add(-r.cfg.RecencyGuard).UTC().Truncate(time.Second)

	inv := Inventory{
		GeneratedAt: now.UTC(),
		Policy: InventoryPolicy{
			KeepN:         r.cfg.KeepN,
			RecencyHours:  r.cfg.RecencyGuard.Hours(),
			RecencyCutoff: cutoff,
		},
	}

	plan, err := r.Plan(ctx)
	if err != nil {
		return inv, fmt.Errorf("plan eviction: %w", err)
	}
	evicted := set.New[int64](plan.Releases())
	for _, ref := range plan.EvictedReleases {
		evicted.Add(ref.ID)
	}
	for _, ref := range plan.AbandonedReleases {
		evicted.Add(ref.ID)
	}
	freed := set.New[string](len(plan.FreedBlobs))
	for _, b := range plan.FreedBlobs {
		freed.Add(b.Key)
	}

	facts, err := r.db.ListReleaseRetentionFacts(ctx)
	if err != nil {
		return inv, fmt.Errorf("list release retention facts: %w", err)
	}
	holds := make(map[int64][]string, len(facts))
	for _, f := range facts {
		h := releaseHolds(f, r.cfg.KeepN, cutoff)
		// The plan is the truth. When the derived reasons disagree with it, say
		// so rather than print a reason that is wrong.
		if (len(h) == 0) != evicted.Contains(f.ID) {
			h = []string{HoldUnknown}
			inv.Totals.HoldMismatches++
		}
		holds[f.ID] = h
	}
	inv.Totals.Releases = len(facts)
	inv.Totals.EvictedReleases = plan.Releases()

	files, err := r.listFiles(ctx)
	if err != nil {
		return inv, err
	}

	refs := make(map[string]int, len(files))
	for _, f := range files {
		refs[f.StorageKey]++
	}
	for i := range files {
		f := &files[i]
		f.Refs = refs[f.StorageKey]
		if f.ReleaseID != 0 {
			f.Holds = holds[f.ReleaseID]
			if len(f.Holds) == 0 && !freed.Contains(f.StorageKey) {
				// The release goes, but another row still references the bytes.
				f.Holds = []string{HoldSharedBlob}
			}
		}
		if len(f.Holds) > 0 {
			f.Hold = f.Holds[0]
		}
		f.Reclaimable = f.Hold == HoldNone && freed.Contains(f.StorageKey)
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].Size != files[j].Size {
			return files[i].Size > files[j].Size
		}
		if files[i].StorageKey != files[j].StorageKey {
			return files[i].StorageKey < files[j].StorageKey
		}
		return files[i].Role < files[j].Role
	})
	inv.Files = files

	inv.ByHold, inv.ByRole = groupFiles(files)
	inv.Totals = summarize(inv.Totals, files)
	return inv, nil
}

// releaseHolds gives every pin that keeps a release, and an empty slice when
// eviction takes it. It mirrors ListEvictableReleases and
func releaseHolds(f db.ListReleaseRetentionFactsRow, keepN int, cutoff time.Time) []string {
	var holds []string
	inGuard := !f.CreatedAt.Before(cutoff)

	if !f.Published {
		if f.Draft {
			return []string{HoldDraft}
		}
		if inGuard {
			return []string{HoldRecency}
		}
		return nil
	}

	if f.OciTagCount > 0 {
		holds = append(holds, HoldOCITag)
	}
	if f.DockerArtifactCount > 0 {
		holds = append(holds, HoldDocker)
	}
	keep := int64(keepN)
	if keep < 1 {
		keep = 1
	}
	if f.NewerPublishedOnBranch < keep {
		if f.NewerPublishedOnBranch == 0 {
			holds = append(holds, HoldBranchTip)
		} else {
			holds = append(holds, HoldKeepN)
		}
	}
	if inGuard {
		holds = append(holds, HoldRecency)
	}
	return holds
}

func (r *Retention) listFiles(ctx context.Context) ([]FileEntry, error) {
	var out []FileEntry

	artifacts, err := r.db.ListArtifactFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list artifact files: %w", err)
	}
	for _, a := range artifacts {
		base := FileEntry{
			CreatedAt: a.CreatedAt,
			Project:   a.ProjectName,
			ProjectID: a.ProjectID,
			ReleaseID: a.ReleaseID,
			Version:   a.Version,
			Branch:    a.GitBranch,
			OS:        string(a.OS),
			Arch:      string(a.Arch),
			Kind:      string(a.Kind),
			Filename:  a.Filename,
			Published: a.Published,
			Draft:     a.Draft,
		}
		out = appendFile(out, base, RoleArtifact, a.StorageKey, a.Size, a.SHA256)
		out = appendFile(out, base, RoleStripped, a.StrippedStorageKey, a.StrippedSize, a.StrippedSHA256)
		out = appendFile(out, base, RoleDebug, a.DebugStorageKey, a.DebugSize, "")
	}

	packaged, err := r.db.ListPackagedFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list packaged files: %w", err)
	}
	for _, p := range packaged {
		base := FileEntry{
			CreatedAt: p.CreatedAt,
			Project:   p.ProjectName,
			ProjectID: p.ProjectID,
			ReleaseID: p.ReleaseID,
			Version:   p.Version,
			Branch:    p.GitBranch,
			OS:        string(p.OS),
			Arch:      string(p.Arch),
			Kind:      string(p.Kind),
			Format:    p.Format,
			Filename:  p.Filename,
			Published: p.Published,
			Draft:     p.Draft,
		}
		out = appendFile(out, base, RolePackaged, p.StorageKey, p.Size, p.SHA256)
	}

	sites, err := r.db.ListSiteDetails(ctx)
	if err != nil {
		return nil, fmt.Errorf("list site files: %w", err)
	}
	for _, s := range sites {
		base := FileEntry{
			CreatedAt: s.CreatedAt,
			Project:   s.ProjectName,
			ProjectID: s.ProjectID,
			Branch:    s.Branch,
			Published: true,
			Holds:     []string{HoldSite},
		}
		out = appendFile(out, base, RoleSite, s.StorageKey, s.Size, s.SHA256)
	}

	ociBlobs, err := r.db.ListOCIBlobFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list oci blob files: %w", err)
	}
	for _, o := range ociBlobs {
		base := FileEntry{
			CreatedAt: o.CreatedAt,
			Project:   o.ProjectName,
			ProjectID: o.ProjectID,
			MediaType: o.MediaType,
			Published: true,
			Holds:     []string{HoldOCIBlob},
		}
		out = appendFile(out, base, RoleOCIBlob, o.StorageKey, o.Size, "")
	}

	modules, err := r.db.ListGoproxyBlobFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list goproxy files: %w", err)
	}
	for _, m := range modules {
		base := FileEntry{
			CreatedAt: m.FetchedAt,
			Module:    m.ModulePath,
			Version:   m.Version,
			Published: true,
			Holds:     []string{HoldGoproxy},
		}
		out = appendFile(out, base, RoleGoproxy, m.ZipStorageKey, m.ZipSize, "")
	}

	return out, nil
}

func appendFile(out []FileEntry, base FileEntry, role, key string, size int64, sha string) []FileEntry {
	if key == "" {
		return out
	}
	base.Role = role
	base.StorageKey = key
	base.Size = size
	base.SHA256 = sha
	return append(out, base)
}

func groupFiles(files []FileEntry) (byHold, byRole []InventoryGroup) {
	holds := make(map[string]*InventoryGroup)
	roles := make(map[string]*InventoryGroup)
	seen := set.New[string](len(files))

	group := func(m map[string]*InventoryGroup, name string) *InventoryGroup {
		g, ok := m[name]
		if !ok {
			g = &InventoryGroup{Name: name}
			m[name] = g
		}
		return g
	}

	for _, f := range files {
		hold := f.Hold
		if f.Reclaimable {
			hold = "reclaimable"
		}
		hg, rg := group(holds, hold), group(roles, f.Role)
		hg.Files++
		hg.Bytes += f.Size
		rg.Files++
		rg.Bytes += f.Size
		if seen.Add(f.StorageKey) {
			hg.Blobs++
			hg.BlobBytes += f.Size
			rg.Blobs++
			rg.BlobBytes += f.Size
		}
	}
	return sortedGroups(holds), sortedGroups(roles)
}

func sortedGroups(m map[string]*InventoryGroup) []InventoryGroup {
	out := make([]InventoryGroup, 0, len(m))
	for _, g := range m {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// summarize fills the whole-server counters. Blob counters count each storage
func summarize(t InventoryTotals, files []FileEntry) InventoryTotals {
	seen := set.New[string](len(files))
	for _, f := range files {
		t.Files++
		t.Bytes += f.Size
		if !seen.Add(f.StorageKey) {
			continue
		}
		t.Blobs++
		t.BlobBytes += f.Size
		if f.Reclaimable {
			t.ReclaimableBlobs++
			t.ReclaimableBytes += f.Size
			continue
		}
		t.HeldBlobs++
		t.HeldBytes += f.Size
	}
	return t
}

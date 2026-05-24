package version

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

func Resolve(d *db.DB, project *db.Project, spec string, releases []db.Release) (*db.Release, error) {
	if len(releases) == 0 {
		return nil, fmt.Errorf("no published releases")
	}

	spec = strings.TrimPrefix(spec, "v")

	if spec == "latest" || spec == "" {
		return &releases[0], nil
	}

	if project.Versioning == db.VersioningAuto {
		num, err := strconv.ParseInt(spec, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid version %q for auto-versioned project", spec)
		}
		for i := range releases {
			if releases[i].VersionNum == num {
				return &releases[i], nil
			}
		}
		return nil, fmt.Errorf("version %d not found", num)
	}

	for i := range releases {
		v := strings.TrimPrefix(releases[i].Version, "v")
		if v == spec {
			return &releases[i], nil
		}
	}

	parts := strings.Split(spec, ".")
	switch len(parts) {
	case 1:
		prefix := parts[0] + "."
		for i := range releases {
			v := strings.TrimPrefix(releases[i].Version, "v")
			if strings.HasPrefix(v, prefix) && !releases[i].IsPrerelease() {
				return &releases[i], nil
			}
		}
	case 2:
		prefix := spec + "."
		for i := range releases {
			v := strings.TrimPrefix(releases[i].Version, "v")
			if strings.HasPrefix(v, prefix) && !releases[i].IsPrerelease() {
				return &releases[i], nil
			}
		}
	}

	return nil, fmt.Errorf("version %q not found", spec)
}

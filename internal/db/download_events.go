package db

import "context"

// RecordDownloadEvent appends one download-attribution row: which artifact and
// format were served, and who fetched them (client IP + User-Agent always; the
// authenticated principal too, when the request carried one).
func (d *DB) RecordDownloadEvent(ctx context.Context, artifactID int64, fmt, clientIP, userAgent, principal string) error {
	return d.q.RecordDownloadEvent(ctx, RecordDownloadEventParams{
		ArtifactID: artifactID,
		Fmt:        fmt,
		ClientIp:   clientIP,
		UserAgent:  userAgent,
		Principal:  principal,
	})
}

// ListDownloadEventsByProject returns the project's most recent download events,
// newest first, capped at limit.
func (d *DB) ListDownloadEventsByProject(ctx context.Context, projectID, limit int64) ([]ListDownloadEventsByProjectRow, error) {
	return d.q.ListDownloadEventsByProject(ctx, ListDownloadEventsByProjectParams{
		ProjectID: projectID,
		Limit:     limit,
	})
}

// ListDownloadEventsByRelease returns a single release's most recent download
// events, newest first, capped at limit.
func (d *DB) ListDownloadEventsByRelease(ctx context.Context, releaseID, limit int64) ([]ListDownloadEventsByReleaseRow, error) {
	return d.q.ListDownloadEventsByRelease(ctx, ListDownloadEventsByReleaseParams{
		ReleaseID: releaseID,
		Limit:     limit,
	})
}

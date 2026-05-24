package sites

import (
	"errors"
	"net/http"

	"go.opentelemetry.io/otel/attribute"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx, span := sitesTracer.Start(r.Context(), "sites.delete")
	defer span.End()

	project := auth.ProjectFrom(ctx)
	rt := routeFrom(ctx)

	span.SetAttributes(
		attribute.String("sites.project", project.Name),
		attribute.String("sites.branch", rt.branch),
	)

	storageKey, err := h.DB.DeleteSite(ctx, project.ID, rt.branch)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	if storageKey != "" {
		_ = h.Store.Delete(ctx, storageKey)
	}

	w.WriteHeader(http.StatusNoContent)
}

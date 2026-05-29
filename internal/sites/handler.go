package sites

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var sitesTracer = otel.Tracer("buildhost.sites")

var handler Handler

func init() {
	auth.OnReady(func() {
		handler.DB = auth.DB()
		handler.Store = auth.Store()
<<<<<<< HEAD

		auth.Handle(auth.ServiceRoute("sites", "PUT /{project}/branch/{branch}"), parseRoute, handler.Upload)
		auth.Handle(auth.ServiceRoute("sites", "DELETE /{project}/branch/{branch}"), parseRoute, handler.Delete)
		auth.Handle(auth.ServiceRoute("sites", "GET /{project}/branch/{branch}/{path...}"), parseRoute, handler.Serve)
		auth.Handle(auth.ServiceRoute("sites", "GET /{project}/branch/{branch}"), parseRoute, handler.ServeRedirect)
		auth.Handle(auth.ServiceRoute("sites", "GET /{project}/branches"), parseRoute, handler.List)
=======
		handler.FetchDomains = auth.SiteFetchDomains()
>>>>>>> 58a52bf902b3ef2bcb2522afb3b76b4031a29b22
	})
}

type route struct {
	project string
	branch  string
	path    string
	write   bool
}

func (r route) ProjectName() string { return r.project }
func (r route) Access() auth.AccessLevel {
	if r.write {
		return auth.WriteAccess
	}
	return auth.ReadAccess
}

func parseRoute(r *http.Request) auth.RouteInfo {
	return route{
		project: r.PathValue("project"),
		branch:  r.PathValue("branch"),
		path:    r.PathValue("path"),
		write:   r.Method == "PUT" || r.Method == "DELETE",
	}
}

func routeFrom(ctx context.Context) route {
	return auth.RouteInfoFrom(ctx).(route)
}

type Handler struct {
	DB           *db.DB
	Store        storage.Storage
	FetchDomains []string
}

package server

import "net/http"

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("POST /api/v1/projects", s.apiHandler.CreateProject)
	mux.HandleFunc("GET /api/v1/projects", s.apiHandler.ListProjects)
	mux.HandleFunc("GET /api/v1/projects/{project}", s.apiHandler.GetProject)

	mux.HandleFunc("POST /api/v1/projects/{project}/releases", s.apiHandler.CreateRelease)
	mux.HandleFunc("GET /api/v1/projects/{project}/releases", s.apiHandler.ListReleases)
	mux.HandleFunc("GET /api/v1/projects/{project}/releases/{version}", s.apiHandler.GetRelease)

	mux.HandleFunc("PUT /api/v1/projects/{project}/releases/{version}/artifacts/{os}/{arch}", s.apiHandler.UploadArtifact)
	mux.HandleFunc("POST /api/v1/projects/{project}/releases/{version}/publish", s.apiHandler.PublishRelease)

	mux.HandleFunc("POST /api/v1/tokens", s.apiHandler.CreateToken)
	mux.HandleFunc("GET /api/v1/tokens", s.apiHandler.ListTokens)
	mux.HandleFunc("DELETE /api/v1/tokens/{id}", s.apiHandler.DeleteToken)

	mux.HandleFunc("GET /dl/{project}/{version}/{os}/{arch}", s.dlHandler.Download)
	mux.HandleFunc("GET /dl/{project}/{version}/debug/{os}/{arch}", s.dlHandler.DownloadDebug)
	mux.HandleFunc("GET /dl/{project}/branch/{branch}/{os}/{arch}", s.dlHandler.DownloadBranch)
	mux.HandleFunc("GET /dl/{project}/latest/{os}/{arch}", s.dlHandler.DownloadLatest)

	mux.Handle("/apt/", http.StripPrefix("/apt", s.aptHandler))
	mux.Handle("/brew/", http.StripPrefix("/brew", s.brewHandler))
	mux.Handle("/npm/", http.StripPrefix("/npm", s.npmHandler))
	mux.Handle("/v2/", s.ociHandler)

	return mux
}

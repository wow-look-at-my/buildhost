// The Settings page: every BUILDHOST_* variable, where its value comes from,
// and a stored value that takes precedence over the environment. It never
// sends a secret's value back.

package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/wow-look-at-my/buildhost/internal/config"
)

const maxSettingBody = 64 << 10

type settingRow struct {
	config.Setting
	// Value is the effective value. It is empty for a secret.
	Value string `json:"value"`
	// Set reports a non-empty effective value. It is the only signal for a secret.
	Set    bool   `json:"set"`
	Source string `json:"source"`
	// Fingerprint identifies a stored key without its value, as GitHub shows it.
	Fingerprint string `json:"fingerprint,omitempty"`
	// Problem says why a set value cannot work.
	Problem string `json:"problem,omitempty"`
}

func settingRows() []settingRow {
	rows := make([]settingRow, 0, len(config.Settings))
	for _, st := range config.Settings {
		v := config.Getenv(st.Name)
		row := settingRow{Setting: st, Set: v != "", Source: config.Source(st.Name)}
		if !st.Secret() {
			row.Value = v
		}
		if st.Name == config.GitHubAppKeySetting && strings.TrimSpace(v) != "" {
			if key, err := config.ParseGitHubAppKey(v); err != nil {
				row.Problem = err.Error()
			} else {
				row.Fingerprint = config.KeyFingerprint(key)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func (s *Server) writeSettings(w http.ResponseWriter) {
	s.writeJSON(w, map[string]any{"settings": settingRows(), "can_restart": s.restart != nil})
}

// apiSettings (GET /api/settings).
func (s *Server) apiSettings(w http.ResponseWriter, _ *http.Request) {
	s.writeSettings(w)
}

// apiSetSetting (PUT /api/settings/{name}) stores {"value": "..."}. An empty
// value is stored too, and overrides the environment. DELETE falls back to the environment.
func (s *Server) apiSetSetting(w http.ResponseWriter, r *http.Request) {
	st, ok := config.LookupSetting(r.PathValue("name"))
	if !ok {
		http.Error(w, "unknown setting", http.StatusNotFound)
		return
	}
	var body struct {
		Value *string `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSettingBody)).Decode(&body); err != nil || body.Value == nil {
		http.Error(w, `want {"value": "..."}`, http.StatusBadRequest)
		return
	}
	value := storedForm(st, *body.Value)
	settingsMu.Lock()
	defer settingsMu.Unlock()
	if _, err := config.CheckChange(st.Name, &value); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.db.SetServerSetting(r.Context(), st.Name, value); err != nil {
		s.settingsError(w, r, err)
		return
	}
	s.reloadSettings(w, r)
}

// storedForm returns value as a save stores it.
func storedForm(st config.Setting, value string) string {
	if !st.Secret() || st.Kind == config.KindPEM {
		value = strings.TrimSpace(value)
	}
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return value
}

// apiValidateSetting (POST /api/settings/{name}/validate) checks {"value":
// "..."} as a save does, and stores nothing. A null value checks a Clear. The
// page calls it while the operator types.
func (s *Server) apiValidateSetting(w http.ResponseWriter, r *http.Request) {
	st, ok := config.LookupSetting(r.PathValue("name"))
	if !ok {
		http.Error(w, "unknown setting", http.StatusNotFound)
		return
	}
	var body struct {
		Value *string `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSettingBody)).Decode(&body); err != nil {
		http.Error(w, `want {"value": "..."} or {"value": null}`, http.StatusBadRequest)
		return
	}
	if body.Value != nil {
		v := storedForm(st, *body.Value)
		body.Value = &v
	}
	res := struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error,omitempty"`
		Warning string `json:"warning,omitempty"`
	}{}
	warning, err := config.CheckChange(st.Name, body.Value)
	if err != nil {
		res.Error = err.Error()
	} else {
		res.OK, res.Warning = true, warning
	}
	s.writeJSON(w, res)
}

// apiDeleteSetting (DELETE /api/settings/{name}) falls back to the environment, then the default.
func (s *Server) apiDeleteSetting(w http.ResponseWriter, r *http.Request) {
	st, ok := config.LookupSetting(r.PathValue("name"))
	if !ok {
		http.Error(w, "unknown setting", http.StatusNotFound)
		return
	}
	s.deleteSetting(w, r, st)
}

func (s *Server) deleteSetting(w http.ResponseWriter, r *http.Request, st config.Setting) {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	if _, err := config.CheckChange(st.Name, nil); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.db.DeleteServerSetting(r.Context(), st.Name); err != nil {
		s.settingsError(w, r, err)
		return
	}
	s.reloadSettings(w, r)
}

// reloadSettings puts the stored values in use. config.Reload gives the new
// configuration to each part of the running server, so no restart is needed.
func (s *Server) reloadSettings(w http.ResponseWriter, r *http.Request) {
	stored, err := s.db.ServerSettings(r.Context())
	if err != nil {
		s.settingsError(w, r, err)
		return
	}
	config.SetStored(stored)
	c := config.Reload()
	s.live.Store(&c)
	slog.Info("settings changed from the admin dashboard", "path", r.URL.Path)
	s.writeSettings(w)
}

// settingsMu makes the check, the store and the reload of one change a single step.
var settingsMu sync.Mutex

func (s *Server) settingsError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("admin api error", "err", err, "path", r.URL.Path)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// apiRestart (POST /api/restart) shuts the server down gracefully and exits
// non-zero, so the container restart policy starts it with the new settings.
func (s *Server) apiRestart(w http.ResponseWriter, _ *http.Request) {
	if s.restart == nil {
		http.Error(w, "this server was not started with a restart hook", http.StatusConflict)
		return
	}
	slog.Warn("restart requested from the admin dashboard")
	w.WriteHeader(http.StatusAccepted)
	go s.restart()
}

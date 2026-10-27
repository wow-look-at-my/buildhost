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
	Set            bool   `json:"set"`
	Source         string `json:"source"`
	RestartPending bool   `json:"restart_pending"`
}

func settingRows() []settingRow {
	rows := make([]settingRow, 0, len(config.Settings))
	for _, st := range config.Settings {
		v := config.Getenv(st.Name)
		row := settingRow{Setting: st, Set: v != "", Source: config.Source(st.Name), RestartPending: config.RestartPending(st)}
		if st.Kind != config.KindSecret {
			row.Value = v
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
// value removes the stored value, as DELETE does.
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
	if err := config.CheckValue(st, *body.Value); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	value := *body.Value
	if st.Kind != config.KindSecret {
		value = strings.TrimSpace(value)
	}
	if strings.TrimSpace(value) == "" {
		s.deleteSetting(w, r, st)
		return
	}
	if err := s.checkStartup(r, st.Name, &value); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.db.SetServerSetting(r.Context(), st.Name, value); err != nil {
		s.settingsError(w, r, err)
		return
	}
	s.reloadSettings(w, r)
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
	if err := s.checkStartup(r, st.Name, nil); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.db.DeleteServerSetting(r.Context(), st.Name); err != nil {
		s.settingsError(w, r, err)
		return
	}
	s.reloadSettings(w, r)
}

// reloadSettings makes the stored values visible to config.Getenv. A Live
// setting applies at once. The others apply at the next start.
func (s *Server) reloadSettings(w http.ResponseWriter, r *http.Request) {
	stored, err := s.db.ServerSettings(r.Context())
	if err != nil {
		s.settingsError(w, r, err)
		return
	}
	config.SetStored(stored)
	s.writeSettings(w)
}

// checkStartup refuses a change after which the server would not start. A
// server that cannot start also takes the dashboard down with it. A nil value
// removes the stored value.
func (s *Server) checkStartup(r *http.Request, name string, value *string) error {
	stored, err := s.db.ServerSettings(r.Context())
	if err != nil {
		return err
	}
	if value == nil {
		delete(stored, name)
	} else {
		stored[name] = *value
	}
	settingsMu.Lock()
	defer settingsMu.Unlock()
	config.SetStored(stored)
	err = config.Load().Validate()
	prev, _ := s.db.ServerSettings(r.Context())
	config.SetStored(prev)
	return err
}

// settingsMu serializes the trial swap of the stored values in checkStartup.
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

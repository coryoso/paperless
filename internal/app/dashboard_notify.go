package app

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// The service publishes locally; independent CLI processors relay the same
// explicit pipeline notification over HTTP. No database/file changes are watched.
// The private discovery file associates a service with this exact state directory.
type dashboardRelay struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

var dashboardRelayClient = &http.Client{
	Timeout:       300 * time.Millisecond,
	Transport:     &http.Transport{}, // Local service traffic must not use an environment proxy.
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
}

func (p *Processor) dashboardRelayPath() string {
	return filepath.Join(p.cfg.Paths.StateDir, ".dashboard-notify.json")
}

// Called after binding the HTTP listener so a failed second server cannot
// replace the running service's discovery record.
func (p *Processor) startDashboardRelay(url string) (func(), error) {
	relay := dashboardRelay{URL: url + "/api/internal/dashboard/notify", Token: randomID() + randomID()}
	data, err := json.Marshal(relay)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(p.cfg.Paths.StateDir, ".dashboard-notify-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(file.Name(), p.dashboardRelayPath()); err != nil {
		return nil, err
	}
	p.dashboardRelayToken = relay.Token
	p.servingDashboard.Store(true)
	return func() {
		p.servingDashboard.Store(false)
		// Only remove our own record if another service has since replaced it.
		current, _ := os.ReadFile(p.dashboardRelayPath())
		if string(current) == string(data) {
			_ = os.Remove(p.dashboardRelayPath())
		}
	}, nil
}

// Call only after a successful state change. Notification failure must never
// turn a successfully processed/archived document into a failed job. When no
// service is running, its next SSE connection will fetch the latest snapshot.
func (p *Processor) notifyDashboard() {
	p.dashboardEvents.publish()
	if p.servingDashboard.Load() {
		return
	}
	data, err := os.ReadFile(p.dashboardRelayPath())
	if err != nil {
		return
	}
	var relay dashboardRelay
	if json.Unmarshal(data, &relay) != nil || relay.Token == "" {
		return
	}
	request, err := http.NewRequest(http.MethodPost, relay.URL, nil)
	if err != nil {
		return
	}
	request.Header.Set("Authorization", "Bearer "+relay.Token)
	response, err := dashboardRelayClient.Do(request)
	if err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		response.Body.Close()
	}
}

func (p *Processor) handleDashboardNotification(w http.ResponseWriter, r *http.Request) {
	if p.dashboardRelayToken == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+p.dashboardRelayToken)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	p.dashboardEvents.publish()
	w.WriteHeader(http.StatusNoContent)
}

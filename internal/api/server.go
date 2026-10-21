// Package api exposes the local HTTP and WebSocket interface used by the
// browser interface and the CLI.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gorilla/websocket"

	"github.com/apot4711-web/sawt-plus/internal/config"
	"github.com/apot4711-web/sawt-plus/internal/daemon"
	"github.com/apot4711-web/sawt-plus/internal/web"
)

const (
	authCookie = "sawt_api"
	authPrefix = "Bearer "
)

type Server struct {
	daemon   *daemon.Daemon
	http     *http.Server
	log      *log.Logger
	assets   fs.FS
	token    string
	shutdown func()
}

// SetShutdown registers the function that stops the daemon process. It is
// used by the /api/shutdown endpoint.
func (s *Server) SetShutdown(fn func()) { s.shutdown = fn }

func New(d *daemon.Daemon, logger *log.Logger, token string) *Server {
	assets, err := web.FS()
	if err != nil {
		assets = nil
	}
	return &Server{daemon: d, log: logger, assets: assets, token: token}
}

func (s *Server) Start(ctx context.Context, addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/devices", s.handleDevices)
	mux.HandleFunc("/api/saved", s.handleSaved)
	mux.HandleFunc("/api/logs", s.handleLogs)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/stream", s.handleStream)
	mux.HandleFunc("/api/connect", s.handleConnect)
	mux.HandleFunc("/api/disconnect", s.handleDisconnect)
	mux.HandleFunc("/api/pair", s.handlePair)
	mux.HandleFunc("/api/unpair", s.handleUnpair)
	mux.HandleFunc("/api/gain", s.handleGain)
	mux.HandleFunc("/api/mute", s.handleMute)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/shutdown", s.handleShutdown)
	mux.HandleFunc("/", s.handleStatic)

	s.http = &http.Server{
		Handler:           withMiddleware(s.token, mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.log.Info("http interface listening", "addr", listener.Addr().String())

	errCh := make(chan error, 1)
	go func() { errCh <- s.http.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.http.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// withMiddleware requires the local API token for every /api/ request, keeps
// the API same-origin only, and requires JSON for state-changing requests.
// This prevents another local user or a random website from listening to the
// microphone through 127.0.0.1.
func withMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// `sawt web` opens the UI with ?token=...; exchange it for an HttpOnly
		// cookie and remove it from the address bar.
		if r.URL.Path == "/" {
			if supplied := r.URL.Query().Get("token"); supplied != "" {
				if !tokensEqual(supplied, token) {
					http.Error(w, "invalid API token; run: sawt web", http.StatusUnauthorized)
					return
				}
				http.SetCookie(w, &http.Cookie{
					Name:     authCookie,
					Value:    token,
					Path:     "/",
					HttpOnly: true,
					SameSite: http.SameSiteStrictMode,
				})
				target := "/"
				if lang := r.URL.Query().Get("lang"); lang != "" {
					target += "?lang=" + url.QueryEscape(lang)
				}
				http.Redirect(w, r, target, http.StatusSeeOther)
				return
			}
		}

		if strings.HasPrefix(r.URL.Path, "/api/") {
			if !requestAuthorized(r, token) {
				writeError(w, http.StatusUnauthorized, errors.New("unauthorized; open the UI with: sawt web"))
				return
			}
			w.Header().Set("Cache-Control", "no-store")
		}

		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !strings.EqualFold(u.Host, r.Host) {
				http.Error(w, "cross-origin requests are not allowed", http.StatusForbidden)
				return
			}
		}
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func requestAuthorized(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	supplied := ""
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, authPrefix) {
		supplied = strings.TrimSpace(strings.TrimPrefix(header, authPrefix))
	}
	if supplied == "" {
		if cookie, err := r.Cookie(authCookie); err == nil {
			supplied = cookie.Value
		}
	}
	return tokensEqual(supplied, token)
}

func tokensEqual(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.daemon.Status())
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.daemon.Logs())
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	timeout := 3000 * time.Millisecond
	if v := r.URL.Query().Get("timeout"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			timeout = d
		}
	}
	devices, err := s.daemon.Discover(r.Context(), timeout)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"devices": []struct{}{}, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

func (s *Server) handleSaved(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"devices": s.daemon.SavedDevices()})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	events, cancel := s.daemon.Subscribe()
	defer cancel()

	send := func(ev daemon.Event) {
		data, err := json.Marshal(ev)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
	send(daemon.Event{Type: "status", Status: s.daemon.Status()})

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			send(ev)
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			// Non-browser local clients still need the API token.
			return true
		}
		u, err := url.Parse(origin)
		return err == nil && strings.EqualFold(u.Host, r.Host)
	},
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	feed, cancel := s.daemon.Feed()
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-done:
			return
		case <-r.Context().Done():
			return
		case frame, ok := <-feed:
			if !ok {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
				return
			}
		}
	}
}

type jsonBody map[string]any

func decodeBody(r *http.Request, out any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	return dec.Decode(out)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target string `json:"target"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.daemon.StartStream(body.Target); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	s.daemon.Disconnect()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target string `json:"target"`
		Code   string `json:"code"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	entry, err := s.daemon.Pair(r.Context(), body.Target, body.Code)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) handleUnpair(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target string `json:"target"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.Target) == "" {
		writeError(w, http.StatusBadRequest, errors.New("target is required"))
		return
	}
	if err := s.daemon.Unpair(body.Target); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleGain(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Gain float64 `json:"gain"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.daemon.SetGain(body.Gain)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMute(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mute bool `json:"mute"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.daemon.SetMute(body.Mute)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, publicConfig(s.daemon.Config()))
		return
	}
	if r.Method == http.MethodPut {
		var incoming config.Config
		if err := decodeBody(r, &incoming); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := s.daemon.UpdateConfig(incoming); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, publicConfig(s.daemon.Config()))
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

// publicConfig strips pairing tokens before a configuration is sent to a
// browser or any other API caller.
func publicConfig(cfg config.Config) config.Config {
	devices := make([]config.DeviceEntry, len(cfg.Devices))
	copy(devices, cfg.Devices)
	for i := range devices {
		devices[i].Token = ""
	}
	cfg.Devices = devices
	return cfg
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	if s.shutdown == nil {
		return
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		s.shutdown()
	}()
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; script-src 'self' 'unsafe-inline'; "+
			"style-src 'self' 'unsafe-inline'; img-src 'self' data:; "+
			"media-src 'self'; connect-src 'self' ws:")
	if s.assets == nil {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if _, err := fs.Stat(s.assets, path); err != nil {
		path = "index.html"
	}
	http.ServeFileFS(w, r, s.assets, path)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

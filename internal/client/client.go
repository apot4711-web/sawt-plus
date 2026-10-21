// Package client talks to a running Sawt+ daemon over its local HTTP
// API. The CLI uses it so every command controls the same headless daemon.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/apot4711-web/sawt-plus/internal/config"
	"github.com/apot4711-web/sawt-plus/internal/daemon"
	"github.com/apot4711-web/sawt-plus/internal/discovery"
)

type Client struct {
	base  string
	token string
	http  *http.Client
}

// New returns a client for a daemon listening on addr (host:port). token is
// the local API token read from the config directory.
func New(addr, token string) *Client {
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimPrefix(addr, "https://")
	addr = strings.TrimSuffix(addr, "/")
	return &Client{
		base:  "http://" + addr,
		token: token,
		http:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *Client) Addr() string { return strings.TrimPrefix(c.base, "http://") }

func (c *Client) Token() string { return c.token }

func (c *Client) authorize(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("daemon returned HTTP %d", e.Status)
	}
	return e.Message
}

func (c *Client) Alive(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/status", nil)
	if err != nil {
		return false
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach daemon at %s: %w", c.Addr(), err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &payload)
		return &Error{Status: resp.StatusCode, Message: payload.Error}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) Status(ctx context.Context) (daemon.Status, error) {
	var st daemon.Status
	err := c.do(ctx, http.MethodGet, "/api/status", nil, &st)
	return st, err
}

func (c *Client) Logs(ctx context.Context) ([]daemon.LogEntry, error) {
	var logs []daemon.LogEntry
	err := c.do(ctx, http.MethodGet, "/api/logs", nil, &logs)
	return logs, err
}

func (c *Client) Devices(ctx context.Context, timeout time.Duration) ([]discovery.Device, error) {
	var payload struct {
		Devices []discovery.Device `json:"devices"`
		Error   string             `json:"error"`
	}
	path := "/api/devices"
	if timeout > 0 {
		path += "?timeout=" + timeout.String()
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &payload); err != nil {
		return nil, err
	}
	if payload.Error != "" {
		return payload.Devices, fmt.Errorf("%s", payload.Error)
	}
	return payload.Devices, nil
}

func (c *Client) Connect(ctx context.Context, target string) error {
	return c.do(ctx, http.MethodPost, "/api/connect", map[string]string{"target": target}, nil)
}

func (c *Client) Disconnect(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/api/disconnect", map[string]any{}, nil)
}

func (c *Client) Pair(ctx context.Context, target, code string) (config.DeviceEntry, error) {
	var entry config.DeviceEntry
	err := c.do(ctx, http.MethodPost, "/api/pair",
		map[string]string{"target": target, "code": code}, &entry)
	return entry, err
}

func (c *Client) Unpair(ctx context.Context, target string) error {
	return c.do(ctx, http.MethodPost, "/api/unpair", map[string]string{"target": target}, nil)
}

func (c *Client) Gain(ctx context.Context, gain float64) error {
	return c.do(ctx, http.MethodPost, "/api/gain", map[string]float64{"gain": gain}, nil)
}

func (c *Client) Mute(ctx context.Context, mute bool) error {
	return c.do(ctx, http.MethodPost, "/api/mute", map[string]bool{"mute": mute}, nil)
}

func (c *Client) Shutdown(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/api/shutdown", map[string]any{}, nil)
}

func (c *Client) Config(ctx context.Context) (*config.Config, error) {
	var cfg config.Config
	err := c.do(ctx, http.MethodGet, "/api/config", nil, &cfg)
	return &cfg, err
}

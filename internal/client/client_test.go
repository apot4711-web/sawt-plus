package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apot4711-web/sawt-plus/internal/daemon"
)

func TestStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(daemon.Status{
			State: daemon.StateStreaming,
			Gain:  1.5,
			Mute:  true,
		})
	}))
	defer srv.Close()

	c := New(strings.TrimPrefix(srv.URL, "http://"))
	if !c.Alive(context.Background()) {
		t.Fatal("daemon should be alive")
	}
	st, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != daemon.StateStreaming || st.Gain != 1.5 || !st.Mute {
		t.Fatalf("unexpected status: %+v", st)
	}
}

func TestErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "no device selected"})
	}))
	defer srv.Close()

	c := New(strings.TrimPrefix(srv.URL, "http://"))
	err := c.Connect(context.Background(), "")
	if err == nil {
		t.Fatal("expected an error")
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("got %T, want *client.Error", err)
	}
	if apiErr.Status != http.StatusBadRequest || apiErr.Message != "no device selected" {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
}

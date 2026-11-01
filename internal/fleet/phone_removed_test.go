package fleet

import (
	"context"
	"github.com/labstack/echo/v5"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRemovedPhoneFeaturesCannotBeInvokedByOldFirmware(t *testing.T) {
	m := testManager(t)
	transport := addSerial(t, m, "A")
	if err := m.DB.Model(&Device{}).Where("id = ?", "A").Updates(map[string]any{
		"features": `{"model":"Air724UG","call_control":true,"call_audio":true,"call_audio_live":true,"call_audio_duplex_probe":true}`,
		"status":   `{"call":{"state":"connected"}}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"dial_call", "hangup_call", "capture_call_audio", "start_call_audio"} {
		if _, err := m.Command(context.Background(), "A", action, map[string]any{"to": "10086", "duration_seconds": 30}, ""); err == nil {
			t.Fatalf("removed action accepted: %s", action)
		}
	}
	if len(transport.messages) != 0 {
		t.Fatal("removed action was dispatched")
	}
	e := echo.New()
	m.Register(e.Group("/api"))
	for _, probe := range []struct{ method, path string }{
		{"POST", "/api/devices/A/call-audio/live"},
		{"GET", "/api/devices/A/call-audio/example/wav"},
		{"GET", "/api/call-audio-ws/example/device"},
	} {
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httptest.NewRequest(probe.method, probe.path, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("removed route %s: %d", probe.path, w.Code)
		}
	}
}

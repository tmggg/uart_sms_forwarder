package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dushixiang/uart_sms_forwarder/internal/middleware"
	"github.com/dushixiang/uart_sms_forwarder/internal/service"
	"github.com/dushixiang/uart_sms_forwarder/internal/util"
	"github.com/labstack/echo/v5"
	"go.uber.org/zap"
)

func TestTTSSettingDefaultsValidationAndRoundTrip(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	s, err := m.GetSetting(ctx, "A", service.PropertyIDTTSText)
	if err != nil {
		t.Fatal(err)
	}
	if s.Value != `{"text":""}` {
		t.Fatalf("unexpected default %q", s.Value)
	}
	// The limit is bytes, so Chinese text must be measured after UTF-8 encoding.
	oversized := `{"text":"` + strings.Repeat("好", 667) + `"}`
	if err := m.SetSetting(ctx, "A", service.PropertyIDTTSText, "来电播报", json.RawMessage(oversized)); err == nil {
		t.Fatal("accepted oversized TTS text")
	}
	if err := m.SetSetting(ctx, "A", service.PropertyIDTTSText, "来电播报", json.RawMessage(`"oops"`)); err == nil {
		t.Fatal("accepted non-object value")
	}
	if err := m.SetSetting(ctx, "A", service.PropertyIDTTSText, "来电播报", json.RawMessage(`{"text":"您好，此次接听为自动接听"}`)); err != nil {
		t.Fatal(err)
	}
	s, err = m.GetSetting(ctx, "A", service.PropertyIDTTSText)
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(s.Value), &value); err != nil {
		t.Fatal(err)
	}
	if value.Text != "您好，此次接听为自动接听" || s.Name != "来电播报" {
		t.Fatalf("round trip %+v", s)
	}
}

func TestTTSCommandValidatesTextAndReachesDevice(t *testing.T) {
	m := testManager(t)
	a := addSerial(t, m, "A")
	ctx := context.Background()
	cmd, err := m.Command(ctx, "A", "set_tts", map[string]any{"text": "您好"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.messages) != 1 {
		t.Fatalf("delivered %d commands", len(a.messages))
	}
	sent := a.messages[0]
	if sent["action"] != "set_tts" || sent["text"] != "您好" || sent["request_id"] != cmd.ID || sent["device_id"] != "A" {
		t.Fatalf("unexpected body %v", sent)
	}
	if _, ok := sent["expires_at"].(int64); !ok {
		t.Fatalf("missing expiry: %v", sent)
	}
	for _, args := range []map[string]any{
		{"text": 42},
		{"text": strings.Repeat("a", 2001)},
		{},
	} {
		if _, err := m.Command(ctx, "A", "set_tts", args, ""); err == nil {
			t.Fatalf("accepted invalid args %v", args)
		}
	}
	if len(a.messages) != 1 {
		t.Fatal("invalid command was still delivered")
	}
}

func TestTTSReplayOnRegisterIncludesClearedText(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	if err := m.SetSetting(ctx, "deviceA", service.PropertyIDTTSText, "来电播报", json.RawMessage(`{"text":"欢迎致电"}`)); err != nil {
		t.Fatal(err)
	}
	token, err := m.Provision(ctx, "deviceA")
	if err != nil {
		t.Fatal(err)
	}
	var replies [][]byte
	m.mqtt = &mqttTransport{publish: func(_ string, b []byte) error {
		replies = append(replies, append([]byte(nil), b...))
		return nil
	}}
	register := func(s *testSession) {
		t.Helper()
		request := map[string]any{"registration_token": token, "key_id": "server-key-1", "features": map[string]any{"device_id": "deviceA", "name": "A"}}
		if err := m.HandleMQTT("sms/v1/devices/deviceA/register", s.packet(t, "register", request)); err != nil {
			t.Fatal(err)
		}
	}
	s := newTestSession(t, m, "deviceA")
	register(s)
	if len(replies) != 2 {
		t.Fatalf("published %d messages", len(replies))
	}
	if got := s.down(t, replies[0]); got.Kind != "register_result" {
		t.Fatalf("first reply %s", got.Kind)
	}
	command := s.down(t, replies[1])
	if command.Kind != "command" {
		t.Fatalf("replay kind %s", command.Kind)
	}
	var body map[string]any
	if err := json.Unmarshal(command.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["action"] != "set_tts" || body["text"] != "欢迎致电" || body["device_id"] != "deviceA" {
		t.Fatalf("unexpected replay %v", body)
	}
	if id, _ := body["request_id"].(string); id == "" {
		t.Fatalf("missing request id: %v", body)
	}
	// The empty text must be replayed too, otherwise a device that persisted a
	// custom text would keep announcing it after the operator restored the default.
	if err := m.SetSetting(ctx, "deviceA", service.PropertyIDTTSText, "来电播报", json.RawMessage(`{"text":""}`)); err != nil {
		t.Fatal(err)
	}
	s2 := newTestSession(t, m, "deviceA")
	register(s2)
	if len(replies) != 4 {
		t.Fatalf("published %d messages after second register", len(replies))
	}
	cleared := s2.down(t, replies[3])
	var clearedBody map[string]any
	if err := json.Unmarshal(cleared.Body, &clearedBody); err != nil {
		t.Fatal(err)
	}
	if clearedBody["action"] != "set_tts" || clearedBody["text"] != "" {
		t.Fatalf("unexpected cleared replay %v", clearedBody)
	}
}

func TestTTSReplayWithoutSavedSettingPublishesNothing(t *testing.T) {
	m := testManager(t)
	token, err := m.Provision(context.Background(), "deviceA")
	if err != nil {
		t.Fatal(err)
	}
	var replies [][]byte
	m.mqtt = &mqttTransport{publish: func(_ string, b []byte) error {
		replies = append(replies, append([]byte(nil), b...))
		return nil
	}}
	s := newTestSession(t, m, "deviceA")
	request := map[string]any{"registration_token": token, "key_id": "server-key-1", "features": map[string]any{"device_id": "deviceA", "name": "A"}}
	if err := m.HandleMQTT("sms/v1/devices/deviceA/register", s.packet(t, "register", request)); err != nil {
		t.Fatal(err)
	}
	// A device that never had a remote text configured keeps its firmware default.
	if len(replies) != 1 {
		t.Fatalf("published %d messages", len(replies))
	}
}

func TestTTSPropertyHTTPReportsPushState(t *testing.T) {
	m := testManager(t)
	a := addSerial(t, m, "A")
	offline := Device{ID: "B", Name: "B", AccessMode: "serial", Online: false}
	if err := m.DB.Create(&offline).Error; err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	api := e.Group("/api")
	api.Use(middleware.JWTMiddleware("test-secret", zap.NewNop()))
	m.Register(api)
	token, _, err := util.GenerateToken("operator", "test-secret", 1)
	if err != nil {
		t.Fatal(err)
	}
	put := func(device, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PUT", "/api/devices/"+device+"/properties/"+service.PropertyIDTTSText, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}
	rec := put("A", `{"name":"来电播报","value":{"text":"您好"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("online device: %d %s", rec.Code, rec.Body.String())
	}
	var pushed struct {
		Saved     bool   `json:"saved"`
		Pushed    bool   `json:"pushed"`
		CommandID string `json:"commandId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pushed); err != nil {
		t.Fatal(err)
	}
	if !pushed.Saved || !pushed.Pushed || pushed.CommandID == "" {
		t.Fatalf("unexpected push result %+v", pushed)
	}
	if len(a.messages) != 1 || a.messages[0]["text"] != "您好" {
		t.Fatalf("device did not receive the text: %v", a.messages)
	}
	rec = put("B", `{"name":"来电播报","value":{"text":"您好"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("offline device: %d %s", rec.Code, rec.Body.String())
	}
	var queued struct {
		Saved   bool   `json:"saved"`
		Pushed  bool   `json:"pushed"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &queued); err != nil {
		t.Fatal(err)
	}
	// Offline devices must still persist the text so it can be replayed later.
	if !queued.Saved || queued.Pushed || queued.Message == "" {
		t.Fatalf("unexpected offline result %+v", queued)
	}
	if s, err := m.GetSetting(context.Background(), "B", service.PropertyIDTTSText); err != nil || s.Value != `{"text":"您好"}` {
		t.Fatalf("offline text not saved: %+v %v", s, err)
	}
	if rec := put("A", `{"name":"来电播报","value":{"text":"`+strings.Repeat("a", 2001)+`"}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized text: %d %s", rec.Code, rec.Body.String())
	}
	// The generic property response must not change for other properties.
	req := httptest.NewRequest("PUT", "/api/devices/A/properties/notification_channels", strings.NewReader(`{"value":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("notification channels: %d", rec.Code)
	}
}

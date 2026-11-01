package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dushixiang/uart_sms_forwarder/internal/models"
	"github.com/dushixiang/uart_sms_forwarder/internal/service"
	"github.com/labstack/echo/v5"
)

func TestBarkDeviceSettingAndDelivery(t *testing.T) {
	m := testManager(t)
	addSerial(t, m, "A")
	addSerial(t, m, "B")
	ctx := context.Background()
	if err := m.DB.Model(&Device{}).Where("id = ?", "A").Update("name", "Bark测试设备").Error; err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/bark/push" || body["device_key"] != "test-only-key" || !strings.Contains(body["body"], "设备：Bark测试设备") {
			t.Error("incorrect Bark payload")
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200}`))
	}))
	defer server.Close()
	channel := models.NotificationChannelConfig{Type: "bark", Enabled: true, Config: map[string]any{"serverType": "custom", "serverUrl": server.URL + "/bark", "key": "test-only-key"}}
	e := echo.New()
	m.Register(e.Group("/api"))
	request := httptest.NewRequest(http.MethodPut, "/api/devices/A/properties/notification_channels", strings.NewReader(string(mustJSON(t, map[string]any{"name": "通知渠道", "value": []models.NotificationChannelConfig{channel}}))))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatal(response.Code, response.Body.String())
	}
	saved, err := m.GetSetting(ctx, "A", service.PropertyIDNotificationChannels)
	if err != nil || !strings.Contains(saved.Value, "bark") {
		t.Fatal("Bark configuration not persisted", err)
	}
	other, err := m.GetSetting(ctx, "B", service.PropertyIDNotificationChannels)
	if err != nil || other.Value != "[]" {
		t.Fatal("device settings leaked", err)
	}
	if err := m.TestNotification(ctx, "A", "bark"); err != nil {
		t.Fatal(err)
	}
	job := notificationJob{Channel: channel, Message: service.NotificationMessage{Type: "sms", Content: "offline test"}}
	if err := m.deliverNotification(ctx, "A", job); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("got %d requests", calls)
	}
	channel.Config["serverUrl"] = "ftp://example.invalid"
	if err := m.SetSetting(ctx, "A", service.PropertyIDNotificationChannels, "", mustJSON(t, []models.NotificationChannelConfig{channel})); err == nil {
		t.Fatal("invalid Bark URL accepted")
	}
}

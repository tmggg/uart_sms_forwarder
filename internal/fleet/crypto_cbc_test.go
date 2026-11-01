package fleet

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"github.com/dushixiang/uart_sms_forwarder/internal/models"
	"github.com/labstack/echo/v5"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func cbcClientPacket(t *testing.T, s *testSession, kind string, body any) []byte {
	t.Helper()
	s.seq++
	clear := mustJSON(t, innerPacket{V: 2, DeviceID: s.id, SID: s.sid, Direction: "up", Seq: s.seq, Kind: kind, Body: mustJSON(t, body)})
	pad := 16 - len(clear)%16
	clear = append(clear, bytes.Repeat([]byte{byte(pad)}, pad)...)
	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(deriveCBC(s.root, s.sid, "up", "enc"))
	if err != nil {
		t.Fatal(err)
	}
	ct := make([]byte, len(clear))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, clear)
	p := Envelope{V: 2, DeviceID: s.id, SID: s.sid, Direction: "up", Seq: s.seq, Kind: kind, IV: hex.EncodeToString(iv), Ciphertext: hex.EncodeToString(ct)}
	if kind == "register" {
		p.KeyID = "server-key-1"
		p.WrappedKey = s.wrapped
	}
	p.Tag = hex.EncodeToString(cbcMAC(s.root, p))
	return mustJSON(t, p)
}
func TestCBCRegistrationReplayTamperRestartAndDownlink(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	token, err := m.ProvisionProtocol(ctx, "deviceA", protocolCBC)
	if err != nil {
		t.Fatal(err)
	}
	var replies [][]byte
	m.mqtt = &mqttTransport{publish: func(_ string, b []byte) error { replies = append(replies, b); return nil }}
	s := newTestSession(t, m, "deviceA")
	registration := cbcClientPacket(t, s, "register", map[string]any{"registration_token": token, "key_id": "server-key-1", "features": map[string]any{"device_id": "deviceA", "model": "Air724UG"}})
	var original Envelope
	_ = json.Unmarshal(registration, &original)
	for _, field := range []string{"tag", "ciphertext", "iv", "device_id", "sid", "direction", "seq", "kind", "key_id", "wrapped_key", "v"} {
		data := map[string]any{}
		_ = json.Unmarshal(registration, &data)
		switch field {
		case "v":
			data[field] = 1
		case "seq":
			data[field] = 2
		default:
			data[field] = "tampered"
		}
		if m.HandleMQTT("sms/v1/devices/deviceA/register", mustJSON(t, data)) == nil {
			t.Fatal("accepted tampered", field)
		}
	}
	if len(replies) != 0 {
		t.Fatal("replied before authentication")
	}
	if err = m.HandleMQTT("sms/v1/devices/deviceA/register", registration); err != nil {
		t.Fatal(err)
	}
	if err = m.HandleMQTT("sms/v1/devices/deviceA/register", registration); err != nil {
		t.Fatal("registration retransmit", err)
	}
	var p Envelope
	_ = json.Unmarshal(replies[len(replies)-1], &p)
	body, err := decryptCBC(s.root, p, "down")
	if err != nil || !strings.Contains(string(body), `"accepted":true`) {
		t.Fatal("registration response", err)
	}
	var stored Session
	m.DB.First(&stored, "id = ?", s.sid)
	if stored.Protocol != protocolCBC {
		t.Fatal("protocol not stored")
	}
	restart, err := New(m.DB, m.Config, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	restart.mqtt = m.mqtt
	event := map[string]any{"message_id": "cbc-sms1", "data": map[string]any{"device_id": "deviceA", "type": "incoming_sms", "from": "10086", "content": "中文"}}
	packet := cbcClientPacket(t, s, "event", event)
	if err = restart.HandleMQTT("sms/v1/devices/deviceA/events", packet); err != nil {
		t.Fatal(err)
	}
	if restart.HandleMQTT("sms/v1/devices/deviceA/events", packet) == nil {
		t.Fatal("accepted replay")
	}
	if err = restart.HandleMQTT("sms/v1/devices/deviceA/events", cbcClientPacket(t, s, "event", event)); err != nil {
		t.Fatal(err)
	}
	var count int64
	m.DB.Model(&models.TextMessage{}).Where("device_id = ?", "deviceA").Count(&count)
	if count != 1 {
		t.Fatal("SMS dedup", count)
	}
	if restart.HandleMQTT("sms/v1/devices/deviceA/status", s.packet(t, "event", event)) == nil {
		t.Fatal("switched established CBC session to GCM")
	}
	statusEvent := map[string]any{"message_id": "cbc-status", "data": map[string]any{"type": "status_response", "device_id": "deviceA", "features": map[string]any{"model": "Air724UG", "crypto_protocol": protocolGCM}}}
	if err = restart.HandleMQTT("sms/v1/devices/deviceA/status", cbcClientPacket(t, s, "event", statusEvent)); err != nil {
		t.Fatal(err)
	}
	d, err := restart.Device(ctx, "deviceA")
	if err != nil {
		t.Fatal(err)
	}
	var features map[string]any
	_ = json.Unmarshal([]byte(d.Features), &features)
	if features["crypto_protocol"] != protocolCBC {
		t.Fatal("device report changed authenticated protocol")
	}
	if _, err = restart.Command(ctx, "deviceA", "get_status", map[string]any{}, ""); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(replies[len(replies)-1], &p)
	body, err = decryptCBC(s.root, p, "down")
	if err != nil || !strings.Contains(string(body), "get_status") {
		t.Fatal("downlink", err)
	}
	if err = restart.Revoke(ctx, "deviceA"); err != nil {
		t.Fatal(err)
	}
	if restart.HandleMQTT("sms/v1/devices/deviceA/events", cbcClientPacket(t, s, "event", event)) == nil {
		t.Fatal("accepted revoked session")
	}
}
func TestCBCRequiresExplicitEnrollmentAndRejectsDowngrade(t *testing.T) {
	m := testManager(t)
	token, _ := m.Provision(context.Background(), "deviceA")
	s := newTestSession(t, m, "deviceA")
	req := map[string]any{"registration_token": token, "key_id": "server-key-1", "features": map[string]any{"device_id": "deviceA"}}
	if m.HandleMQTT("sms/v1/devices/deviceA/register", cbcClientPacket(t, s, "register", req)) == nil {
		t.Fatal("CBC accepted for GCM credential")
	}
	token, _ = m.ProvisionProtocol(context.Background(), "deviceA", protocolCBC)
	req["registration_token"] = token
	if m.HandleMQTT("sms/v1/devices/deviceA/register", s.packet(t, "register", req)) == nil {
		t.Fatal("GCM accepted for CBC credential")
	}
}
func TestEnrollmentDeviceTypeAndConfiguration(t *testing.T) {
	m := testManager(t)
	e := echo.New()
	m.Register(e.Group("/api"))
	for _, kind := range []string{"air780", "air724ug", "unsupported"} {
		req := httptest.NewRequest("POST", "/api/devices/enroll", strings.NewReader(`{"deviceId":"A","deviceType":"`+kind+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if kind == "unsupported" {
			if rec.Code != 400 {
				t.Fatal(rec.Code)
			}
			continue
		}
		if rec.Code != http.StatusCreated {
			t.Fatal(rec.Code, rec.Body.String())
		}
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		expected := protocolGCM
		if kind == "air724ug" {
			expected = protocolCBC
		}
		if body["cryptoProtocol"] != expected || body["deviceType"] != kind {
			t.Fatal("wrong profile", body["cryptoProtocol"])
		}
	}
}

func TestCBCIndependentPythonVectorsAndValidHexTampering(t *testing.T) {
	data, err := os.ReadFile("testdata/cbc_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Root   string
		Packet Envelope
		Body   json.RawMessage
	}
	if err = json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		root, _ := hex.DecodeString(v.Root)
		body, err := decryptCBC(root, v.Packet, v.Packet.Direction)
		var actual, want any
		_ = json.Unmarshal(body, &actual)
		_ = json.Unmarshal(v.Body, &want)
		if err != nil || string(mustJSON(t, actual)) != string(mustJSON(t, want)) {
			t.Fatal("independent vector", err)
		}
		for _, field := range []string{"iv", "ciphertext", "tag"} {
			bad := v.Packet
			mutate := func(s string) string { b, _ := hex.DecodeString(s); b[len(b)-1] ^= 1; return hex.EncodeToString(b) }
			switch field {
			case "iv":
				bad.IV = mutate(bad.IV)
			case "ciphertext":
				bad.Ciphertext = mutate(bad.Ciphertext)
			case "tag":
				bad.Tag = mutate(bad.Tag)
			}
			if _, err = decryptCBC(root, bad, bad.Direction); err == nil {
				t.Fatal("accepted valid-hex tamper", field)
			}
		}
		if _, err = decryptCBC(root, v.Packet, map[string]string{"up": "down", "down": "up"}[v.Packet.Direction]); err == nil {
			t.Fatal("reflected packet")
		}
	}
}

package fleet

import (
	"encoding/json"
	"testing"
)

func TestVoltageStatusMerge(t *testing.T) {
	status := `{"mobile":{"battery_mv":3900,"imsi":"test"}}`
	for _, tc := range []struct {
		name, event string
		want        any
	}{
		{"heartbeat", `{"type":"heartbeat","mobile":{"battery_mv":3850}}`, float64(3850)},
		{"features", `{"features":{"mobile":{"battery_mv":4100}}}`, float64(4100)},
		{"unrelated", `{"locating":true}`, float64(3900)},
		{"failed read clears old value", `{"mobile":{"battery_mv":0}}`, nil},
		{"null", `{"mobile":{"battery_mv":null}}`, nil},
		{"wrong type", `{"mobile":{"battery_mv":"3850"}}`, nil},
		{"negative", `{"mobile":{"battery_mv":-1}}`, nil},
		{"out of range", `{"mobile":{"battery_mv":10001}}`, nil},
		{"fractional", `{"mobile":{"battery_mv":3850.5}}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var event, got map[string]any
			if err := json.Unmarshal([]byte(tc.event), &event); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(mergeStatus(status, event)), &got); err != nil {
				t.Fatal(err)
			}
			mobile := got["mobile"].(map[string]any)
			if mobile["battery_mv"] != tc.want {
				t.Fatalf("battery_mv = %v, want %v", mobile["battery_mv"], tc.want)
			}
			if mobile["imsi"] != "test" {
				t.Fatal("unrelated mobile field lost")
			}
		})
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(mergeStatus(`{}`, map[string]any{"type": "heartbeat"})), &got)
	if _, exists := got["mobile"].(map[string]any)["battery_mv"]; exists {
		t.Fatal("legacy device gained a voltage reading")
	}
}

package fleet

import (
	"encoding/json"
	"testing"
)

func TestTemperatureStatusMerge(t *testing.T) {
	for _, tc := range []struct {
		event string
		want  any
	}{
		{`{"mobile":{"temperature_mc":29770}}`, float64(29770)},
		{`{"features":{"mobile":{"temperature_mc":-1250}}}`, float64(-1250)},
		{`{"temperature_mc":0}`, float64(0)},
		{`{"locating":true}`, float64(42000)},
		{`{"mobile":{"temperature_mc":false}}`, nil},
		{`{"mobile":{"temperature_mc":null}}`, nil},
		{`{"mobile":{"temperature_mc":"29.77"}}`, nil},
		{`{"mobile":{"temperature_mc":150001}}`, nil},
		{`{"mobile":{"temperature_mc":-50001}}`, nil},
		{`{"mobile":{"temperature_mc":1.5}}`, nil},
	} {
		var event, result map[string]any
		if err := json.Unmarshal([]byte(tc.event), &event); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(mergeStatus(`{"mobile":{"temperature_mc":42000,"imsi":"keep"}}`, event)), &result); err != nil {
			t.Fatal(err)
		}
		mobile := result["mobile"].(map[string]any)
		if mobile["temperature_mc"] != tc.want || mobile["imsi"] != "keep" {
			t.Fatalf("event %s: got %v, want temperature %v", tc.event, mobile, tc.want)
		}
	}
}

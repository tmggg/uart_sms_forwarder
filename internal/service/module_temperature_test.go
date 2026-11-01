package service

import (
	"encoding/json"
	"testing"
)

func TestModuleTemperatureStatusDecode(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"29770", "29770"}, {"-1250", "-1250"}, {"0", "0"},
		{"false", "null"}, {"null", "null"}, {"150001", "null"},
		{"\"bad\"", "null"}, {"1.5", "null"},
	} {
		var status StatusData
		if err := json.Unmarshal([]byte(`{"mobile":{"temperature_mc":`+tc.input+`,"imsi":"keep"}}`), &status); err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(status.Mobile.TemperatureMC)
		if err != nil || string(got) != tc.want || status.Mobile.Imsi != "keep" {
			t.Fatalf("input %s: got %s, want %s, err %v", tc.input, got, tc.want, err)
		}
	}
}

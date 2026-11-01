package service

import "encoding/json"

// ModuleTemperature accepts the Lua false sentinel without rejecting the status.
type ModuleTemperature struct {
	value *int
}

func (t *ModuleTemperature) UnmarshalJSON(data []byte) error {
	t.value = nil
	var mc int
	if json.Unmarshal(data, &mc) == nil && string(data) != "null" && mc >= -50000 && mc <= 150000 {
		t.value = &mc
	}
	return nil
}

func (t ModuleTemperature) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.value)
}

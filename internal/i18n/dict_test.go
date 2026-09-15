package i18n

import "testing"

// TestDictParity ensures every EN key has an ID translation and vice versa.
// T() falls back to English on missing keys, so a missing ID entry would
// silently render English — this test makes that visible.
func TestDictParity(t *testing.T) {
	for k := range dict[EN] {
		if _, ok := dict[ID][k]; !ok {
			t.Errorf("missing id translation for key %q", k)
		}
	}
	for k := range dict[ID] {
		if _, ok := dict[EN][k]; !ok {
			t.Errorf("missing en translation for key %q", k)
		}
	}
	if len(dict[EN]) == 0 {
		t.Error("empty EN dict")
	}
}

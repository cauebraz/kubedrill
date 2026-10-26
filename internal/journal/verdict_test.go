package journal

import (
	"encoding/json"
	"testing"
)

func TestVerdictJSONRoundTrip(t *testing.T) {
	tests := []struct {
		verdict Verdict
		want    string
	}{
		{VerdictRootCause, `"root-cause"`},
		{VerdictSymptomOnly, `"symptom-only"`},
		{VerdictNoWrites, `"no-writes"`},
		{VerdictUnknown, `"unknown"`},
	}
	for _, tc := range tests {
		data, err := json.Marshal(Telemetry{Source: "audit", Verdict: tc.verdict})
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
		if got := string(raw["verdict"]); got != tc.want {
			t.Errorf("encode %s: got verdict %s, want %s", tc.verdict, got, tc.want)
		}
		var back Telemetry
		if err := json.Unmarshal([]byte(`{"source":"audit","verdict":`+tc.want+`}`), &back); err != nil {
			t.Fatal(err)
		}
		if back.Verdict != tc.verdict {
			t.Errorf("decode %s: got %q, want %q", tc.want, back.Verdict, tc.verdict)
		}
	}
}

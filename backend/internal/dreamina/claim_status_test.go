package dreamina

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestClaimDecisionScalarCompatibility(t *testing.T) {
	for name, truth := range map[string]any{
		"bool": true, "float": float64(1), "int": 1, "int64": int64(1), "json": json.Number("1"), "string-number": "1", "string-true": "true", "string-case-space": " TRUE ",
	} {
		for _, nested := range []bool{false, true} {
			for _, field := range []string{"received", "success"} {
				t.Run(name+"/"+field+map[bool]string{true: "/nested", false: "/top"}[nested], func(t *testing.T) {
					raw := map[string]any{field: truth}
					if nested {
						raw = map[string]any{"data": raw}
					}
					decision := normalizeClaimDecision(raw)
					if decision.Status != ClaimConfirmed || decision.Reason != "confirmed" {
						t.Fatalf("decision=%+v", decision)
					}
				})
			}
		}
	}
	for name, falsehood := range map[string]any{
		"bool": false, "float": float64(0), "int": 0, "int64": int64(0), "json": json.Number("0"), "string-number": "0", "string-false": "false", "string-case-space": " FALSE ",
	} {
		t.Run(name+"/false", func(t *testing.T) {
			decision := normalizeClaimDecision(map[string]any{"success": falsehood})
			if decision.Status != ClaimRejected {
				t.Fatalf("explicit false success: %+v", decision)
			}
			decision = normalizeClaimDecision(map[string]any{"received": falsehood})
			if decision.Status != ClaimUnconfirmed {
				t.Fatalf("false received alone: %+v", decision)
			}
			decision = normalizeClaimDecision(map[string]any{"ret": 3001, "data": map[string]any{"received": falsehood}})
			if decision.Status != ClaimAlreadyReceived {
				t.Fatalf("legacy already claimed: %+v", decision)
			}
		})
	}
}

func TestClaimDecisionBusinessCodesAndRejections(t *testing.T) {
	tests := []struct {
		name         string
		raw          any
		status       ClaimStatus
		code, reason string
	}{
		{"legacy success", map[string]any{"ret": 0}, ClaimConfirmed, "0", "confirmed"},
		{"legacy success string", map[string]any{"ret": " 0 "}, ClaimConfirmed, "0", "confirmed"},
		{"legacy already", map[string]any{"ret": float64(3001), "message": "already received", "data": map[string]any{"received": false}}, ClaimAlreadyReceived, "3001", "already_claimed"},
		{"duplicate already code", map[string]any{"ret": 3001, "code": 3001}, ClaimAlreadyReceived, "3001", "already_claimed"},
		{"alternate success", map[string]any{"code": json.Number("0")}, ClaimConfirmed, "0", "confirmed"},
		{"nullable primary code", map[string]any{"ret": nil, "code": 0}, ClaimConfirmed, "0", "confirmed"},
		{"empty primary code", map[string]any{"ret": "", "code": "0"}, ClaimConfirmed, "0", "confirmed"},
		{"duplicate zero", map[string]any{"ret": 0, "code": 0}, ClaimConfirmed, "0", "confirmed"},
		{"nonzero primary", map[string]any{"ret": 9999}, ClaimRejected, "9999", "explicit_rejection"},
		{"nonzero alternate", map[string]any{"code": -1}, ClaimRejected, "-1", "explicit_rejection"},
		{"already code only retains code semantics", map[string]any{"code": 3001}, ClaimRejected, "3001", "explicit_rejection"},
		{"explicit failure", map[string]any{"success": false}, ClaimRejected, "", "explicit_rejection"},
		{"symbolic code and explicit failure", map[string]any{"code": "PERMISSION_DENIED", "success": false}, ClaimRejected, "PERMISSION_DENIED", "explicit_rejection"},
		{"symbolic ret and explicit error", map[string]any{"ret": "ERR_X", "error": "private rejection detail"}, ClaimRejected, "ERR_X", "explicit_rejection"},
		{"nested explicit failure", map[string]any{"data": map[string]any{"success": "0"}}, ClaimRejected, "", "explicit_rejection"},
		{"error string", map[string]any{"error": "private provider error"}, ClaimRejected, "", "explicit_rejection"},
		{"error object", map[string]any{"error": map[string]any{"message": "private"}}, ClaimRejected, "", "explicit_rejection"},
		{"error array", map[string]any{"error": []any{"private"}}, ClaimRejected, "", "explicit_rejection"},
		{"error boolean", map[string]any{"error": true}, ClaimRejected, "", "explicit_rejection"},
		{"error code", map[string]any{"error": int64(42)}, ClaimRejected, "", "explicit_rejection"},
		{"nested error", map[string]any{"data": map[string]any{"error": "private"}}, ClaimRejected, "", "explicit_rejection"},
		{"message not evidence", map[string]any{"message": "success"}, ClaimUnconfirmed, "", "unrecognized_response"},
		{"balance not evidence", map[string]any{"data": map[string]any{"balance": 200, "previous_balance": 100}}, ClaimUnconfirmed, "", "unrecognized_response"},
		{"unknown symbolic code", map[string]any{"ret": "UNRECOGNIZED_1"}, ClaimUnconfirmed, "UNRECOGNIZED_1", "unrecognized_response"},
		{"malformed code", map[string]any{"ret": map[string]any{"code": 0}}, ClaimUnconfirmed, "", "unrecognized_response"},
		{"malformed flag", map[string]any{"received": "yes"}, ClaimUnconfirmed, "", "unrecognized_response"},
		{"nonbinary flag", map[string]any{"received": 2}, ClaimUnconfirmed, "", "unrecognized_response"},
		{"empty", map[string]any{}, ClaimUnconfirmed, "", "unrecognized_response"},
		{"nil map", map[string]any(nil), ClaimUnconfirmed, "", "invalid_shape"},
		{"nil", nil, ClaimUnconfirmed, "", "invalid_shape"},
		{"array", []any{}, ClaimUnconfirmed, "", "invalid_shape"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := normalizeClaimDecision(test.raw)
			want := ClaimDecision{Status: test.status, ProviderCode: test.code, Reason: test.reason}
			if got != want {
				t.Fatalf("got=%+v want=%+v", got, want)
			}
		})
	}
	for name, value := range map[string]any{"absent": nil, "false": false, "zero": 0, "string false": "false", "empty string": "", "object": map[string]any{}, "array": []any{}} {
		t.Run("empty error/"+name, func(t *testing.T) {
			decision := normalizeClaimDecision(map[string]any{"ret": 0, "error": value})
			if decision.Status != ClaimConfirmed {
				t.Fatalf("empty error treated as rejection: %+v", decision)
			}
		})
	}
}

func TestClaimDecisionConflictsRemainUnconfirmed(t *testing.T) {
	cases := map[string]map[string]any{
		"received cannot override nonzero ret":                    {"ret": 42, "received": true},
		"received cannot override nonzero code":                   {"code": 42, "data": map[string]any{"received": "1"}},
		"zero ret cannot override false received":                 {"ret": 0, "received": false},
		"zero code cannot override false received":                {"code": 0, "data": map[string]any{"received": "0"}},
		"ret code conflict":                                       {"ret": 0, "code": 42},
		"code ret conflict":                                       {"ret": 42, "code": 0},
		"zero ret explicit failure":                               {"ret": 0, "success": false},
		"zero ret error":                                          {"ret": 0, "error": "private provider error"},
		"symbolic code explicit rejection conflicts with success": {"code": "PERMISSION_DENIED", "success": false, "received": true},
		"received success conflict":                               {"received": true, "success": false},
		"nested flag conflict":                                    {"received": true, "data": map[string]any{"received": false}},
		"nested success conflict":                                 {"success": true, "data": map[string]any{"success": "false"}},
		"already received conflict":                               {"ret": 3001, "received": true},
		"already rejected conflict":                               {"ret": 3001, "error": true},
		"already zero code conflict":                              {"ret": 3001, "code": 0},
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			decision := normalizeClaimDecision(raw)
			if decision.Status != ClaimUnconfirmed || decision.Reason != "conflicting_signals" {
				t.Fatalf("conflicting signals collapsed: %+v", decision)
			}
		})
	}
	for _, raw := range []map[string]any{
		{"ret": 0, "success": "unknown"}, {"ret": "UNRECOGNIZED", "success": true}, {"ret": 0.5}, {"ret": math.Inf(1)}, {"received": math.NaN()},
	} {
		decision := normalizeClaimDecision(raw)
		if decision.Status != ClaimUnconfirmed {
			t.Fatalf("unknown encoding confirmed: %+v", decision)
		}
	}
}

func TestClaimDecisionDiagnosticsAreSafeAndBounded(t *testing.T) {
	for _, code := range []string{"private secret", "error<xml>", "中文", "line\nbreak", strings.Repeat("A", 65), "access=private"} {
		decision := normalizeClaimDecision(map[string]any{"ret": code, "message": "private provider body"})
		if decision.ProviderCode != "" {
			t.Fatalf("unsafe code exposed: %q", decision.ProviderCode)
		}
		encoded, err := json.Marshal(decision)
		if err != nil || strings.Contains(string(encoded), "private") {
			t.Fatalf("unsafe diagnostics %s err=%v", encoded, err)
		}
	}
	raw := map[string]any{"ret": "ERR_1-2", "data": map[string]any{"received": "0"}, "message": "private provider body"}
	before, _ := json.Marshal(raw)
	decision := normalizeClaimDecision(raw)
	after, _ := json.Marshal(raw)
	if decision.ProviderCode != "ERR_1-2" || !reflect.DeepEqual(before, after) {
		t.Fatalf("diagnostics changed provider payload: %+v", decision)
	}
	for _, code := range []string{strings.Repeat("A", 64), "-42"} {
		if decision := normalizeClaimDecision(map[string]any{"ret": code}); decision.ProviderCode != code {
			t.Fatalf("safe code lost: %+v", decision)
		}
	}
}

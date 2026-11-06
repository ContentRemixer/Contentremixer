package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestSlogLoggerFields(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, nil))
	logger := NewSlogLogger(l)

	logger.Log(context.Background(), Entry{
		Subject:      "sub-alice",
		Role:         "DevRel",
		Verb:         "update",
		ResourceKind: "ModelProfile",
		ResourceName: "m1",
		Decision:     DecisionDeny,
	})

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("decode log line: %v", err)
	}
	for k, want := range map[string]string{
		"subject": "sub-alice", "role": "DevRel", "verb": "update",
		"resourceKind": "ModelProfile", "resourceName": "m1", "decision": DecisionDeny,
		"level": "WARN", "msg": "audit",
	} {
		if got, _ := rec[k].(string); got != want {
			t.Errorf("field %q = %q, want %q", k, got, want)
		}
	}
}

func TestSlogLoggerAllowIsInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := NewSlogLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
	logger.Log(context.Background(), Entry{Decision: DecisionAllow, Verb: "get"})

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["level"] != "INFO" {
		t.Errorf("allow decision should log at INFO, got %v", rec["level"])
	}
}

func TestNewSlogLoggerNilUsesDefault(t *testing.T) {
	if NewSlogLogger(nil) == nil {
		t.Fatal("NewSlogLogger(nil) should fall back to the default logger, not nil")
	}
}

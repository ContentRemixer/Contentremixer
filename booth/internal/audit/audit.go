// Package audit implements the design §6.4 audit record. Every Booth API
// mutation logs {subject, role, verb, resourceKind, resourceName, decision};
// forbidden reads are logged too, since a denied access attempt is exactly what
// an audit trail exists to capture.
package audit

import (
	"context"
	"log/slog"
)

// Decision values recorded in the audit log.
const (
	DecisionAllow = "allow"
	DecisionDeny  = "deny"
)

// Entry is one audit record, matching the §6.4 field list exactly.
type Entry struct {
	Subject      string
	Role         string
	Verb         string // "get", "list", "create", "update", "delete", "approve"
	ResourceKind string
	ResourceName string // "" for collection-level verbs such as list/create
	Decision     string // DecisionAllow or DecisionDeny
}

// Logger records audit entries. It is an interface so the enforcement layer is
// not coupled to a sink (slog now, a tamper-evident store later).
type Logger interface {
	Log(ctx context.Context, e Entry)
}

// SlogLogger writes audit entries as structured slog records under a stable
// "audit" marker so they can be filtered out of the operational log stream.
type SlogLogger struct {
	l *slog.Logger
}

// NewSlogLogger wraps a *slog.Logger. If l is nil, slog.Default() is used.
func NewSlogLogger(l *slog.Logger) *SlogLogger {
	if l == nil {
		l = slog.Default()
	}
	return &SlogLogger{l: l}
}

// Log emits the entry at Info level (allow) or Warn level (deny), so denied
// attempts stand out without a separate pipeline.
func (s *SlogLogger) Log(ctx context.Context, e Entry) {
	level := slog.LevelInfo
	if e.Decision == DecisionDeny {
		level = slog.LevelWarn
	}
	s.l.LogAttrs(ctx, level, "audit",
		slog.String("subject", e.Subject),
		slog.String("role", e.Role),
		slog.String("verb", e.Verb),
		slog.String("resourceKind", e.ResourceKind),
		slog.String("resourceName", e.ResourceName),
		slog.String("decision", e.Decision),
	)
}

var _ Logger = (*SlogLogger)(nil)

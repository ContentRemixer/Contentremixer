package content

import (
	"context"
	"errors"
	"testing"
)

// These cover the ingest seam's input invariants, which fail BEFORE any DB
// call, so they run against a zero-value Store (nil pool) with no Postgres —
// the live insert/round-trip path is exercised by store_integration_test.go.
func TestDepositValidation(t *testing.T) {
	s := &Store{}
	ctx := context.Background()
	cases := []struct {
		name string
		req  DepositRequest
	}{
		{"empty owner", DepositRequest{Title: "t", Source: SourceRecycle}},
		{"empty title", DepositRequest{Owner: "system:recycle", Source: SourceRecycle}},
		{"unknown source", DepositRequest{Owner: "system:recycle", Title: "t", Source: "bogus"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Deposit(ctx, tc.req); !errors.Is(err, ErrBadRequest) {
				t.Fatalf("Deposit(%+v) err = %v, want ErrBadRequest", tc.req, err)
			}
		})
	}
}

func TestBackfillRejectsUnknownStatus(t *testing.T) {
	s := &Store{}
	bogus := ContentStatus("nope")
	if _, err := s.Backfill(context.Background(), "id", BackfillRequest{Status: &bogus}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("Backfill with bad status err = %v, want ErrBadRequest", err)
	}
}

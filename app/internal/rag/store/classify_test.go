package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ContentRemixer/Contentremixer/app/internal/telemetry"
)

func TestClassifyStoreErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want telemetry.StoreErrorReason
	}{
		{"nil", nil, telemetry.StoreErrorOther},
		{"deadline", context.DeadlineExceeded, telemetry.StoreErrorTimeout},
		{"canceled", context.Canceled, telemetry.StoreErrorTimeout},
		{"constraint 23505", &pgconn.PgError{Code: "23505"}, telemetry.StoreErrorConstraint},
		{"other pg error", &pgconn.PgError{Code: "42P01"}, telemetry.StoreErrorOther},
		{"connect error", &pgconn.ConnectError{}, telemetry.StoreErrorConn},
		{"timeout message", errors.New("read tcp: i/o timeout"), telemetry.StoreErrorTimeout},
		{"connection refused message", errors.New("dial tcp: connection refused"), telemetry.StoreErrorConn},
		{"wrapped constraint", fmt.Errorf("store: inserting chunk: %w", &pgconn.PgError{Code: "23503"}), telemetry.StoreErrorConstraint},
		{"generic", errors.New("some other failure"), telemetry.StoreErrorOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyStoreErr(c.err); got != c.want {
				t.Errorf("classifyStoreErr(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

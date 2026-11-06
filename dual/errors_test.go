package dual

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/internal/sqlstate"
	"testing"
)

// framedError has SQLState but no Message field; its text is a driver framing.
type framedError struct{ code, text string }

func (e framedError) Error() string    { return e.text }
func (e framedError) SQLState() string { return e.code }

func TestDualErrorFields(t *testing.T) {
	o := &Options{}
	for _, tc := range []struct {
		name          string
		err           error
		class         errClass
		code, message string
	}{
		{"stuntgres", &stuntgres.Error{Code: "42P01", Message: `relation "x" does not exist`}, classSQL, "42P01", `relation "x" does not exist`},
		{"decorated", sqlstate.WithDiagnostic(&sqlstate.Error{Code: "22012", Message: "cause"}, &sqlstate.Error{Code: "42501", Message: "permission denied for table t"}), classSQL, "42501", "permission denied for table t"},
		{"pq", &pq.Error{Code: "23505", Message: "duplicate key value violates unique constraint"}, classSQL, "23505", "duplicate key value violates unique constraint"},
		{"pq wrapped", fmt.Errorf("x: %w", &pq.Error{Code: "23505", Message: "duplicate key value violates unique constraint"}), classSQL, "23505", "duplicate key value violates unique constraint"},
		{"pgconn", &pgconn.PgError{Severity: "ERROR", Code: "42601", Message: "syntax error"}, classSQL, "42601", "syntax error"},
		{"pq framing", framedError{"XX000", "pq: boom (XX000)"}, classSQL, "XX000", "boom"},
		{"pgx framing", framedError{"XX000", "ERROR: boom (SQLSTATE XX000)"}, classSQL, "XX000", "boom"},
		{"unknown framing", framedError{"XX000", "boom"}, classSQL, "XX000", "boom"},
		{"ordinary", errors.New("x"), classOrdinary, "", "x"},
		{"canceled", context.Canceled, classCanceled, "", "context canceled"},
		{"deadline", fmt.Errorf("%w", context.DeadlineExceeded), classCanceled, "", "context deadline exceeded"},
	} {
		got := classify(tc.err, o)
		if got.class != tc.class || got.code != tc.code || got.message != tc.message {
			t.Errorf("%s: class %d code %q message %q, want %d %q %q", tc.name, got.class, got.code, got.message, tc.class, tc.code, tc.message)
		}
	}

	hooked := &Options{ErrorFields: func(err error) (string, string, bool) {
		if err.Error() == "custom" {
			return "P0001", "raised", true
		}
		return "", "", false
	}}
	if got := classify(errors.New("custom"), hooked); got.class != classSQL || got.code != "P0001" || got.message != "raised" {
		t.Errorf("hook: %+v", got)
	}
	if got := classify(errors.New("other"), hooked); got.class != classOrdinary {
		t.Errorf("hook fallback: %+v", got)
	}

	pqPos := classify(&pq.Error{Code: "42601", Message: "syntax error", Position: "8"}, o)
	pgxPos := classify(&pgconn.PgError{Code: "42601", Message: "syntax error", Position: 8}, o)
	if pqPos.position != "8" || pgxPos.position != "8" {
		t.Errorf("positions: pq %q pgconn %q", pqPos.position, pgxPos.position)
	}
	var d diffLines
	compareErrors(&d, pqPos, pgxPos, true)
	if got := d.finish(); got != nil {
		t.Errorf("equal positions: %q", got)
	}
	if got := classify(&pgconn.PgError{Code: "42601", Message: "syntax error"}, o); got.position != "" {
		t.Errorf("pgconn zero position: %q", got.position)
	}
}

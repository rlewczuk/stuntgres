package engine

import (
	"context"
	"testing"
)

// Inspect the existing statement-owned immutable capture after real execution,
// rather than asserting implementation wiring or incidental source text.
func TestVisibilityInquiryCaptureDemand(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{
		"CREATE TABLE capture_ids(id OID)",
		"INSERT INTO capture_ids VALUES(23)",
		"CREATE VIEW capture_inner AS SELECT pg_type_is_visible(id) AS visible FROM capture_ids",
		"CREATE VIEW capture_outer AS SELECT visible FROM capture_inner",
		"CREATE VIEW capture_plain AS SELECT id FROM capture_ids",
		"CREATE TABLE capture_checked(id OID CHECK(pg_type_is_visible(id) IS NOT NULL))",
		"INSERT INTO capture_checked VALUES(23)",
		"CREATE TABLE capture_destination(visible BOOL)",
	} {
		sessionSQL(t, s, q)
	}
	for _, tc := range []struct {
		sql      string
		captured bool
	}{
		{"SELECT id FROM capture_ids", false},
		{"SELECT id FROM capture_plain", false},
		{"SELECT pg_type_is_visible(NULL)", false},
		{"SELECT pg_type_is_visible(id) FROM capture_ids", true},
		{"SELECT TRUE OR pg_type_is_visible(id) FROM capture_ids", true},
		{"SELECT pg_type_is_visible(id) AND FALSE FROM capture_ids", true},
		{"SELECT id FROM capture_ids WHERE pg_type_is_visible(id)", true},
		{"SELECT TRUE OR pg_type_is_visible(NULL)", false},
		{"SELECT visible FROM capture_outer", true},
		{"UPDATE capture_ids SET id=23 RETURNING id", false},
		{"UPDATE capture_ids SET id=23 RETURNING pg_type_is_visible(id)", true},
		{"UPDATE capture_checked SET id=26 RETURNING id", true},
		{"INSERT INTO capture_checked VALUES(23)", true},
		{"INSERT INTO capture_destination SELECT pg_type_is_visible(id) FROM capture_ids", true},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			result, err := scope.Execute(statement(t, tc.sql), 0, nil)
			if result.Rows != nil {
				defer result.Rows.Close()
			}
			defer scope.Finish(err)
			if err != nil {
				t.Fatal(err)
			}
			if got := scope.Inquiry.visibilityCatalog.State() != nil; got != tc.captured {
				t.Fatalf("visibility capture=%v, want %v", got, tc.captured)
			}
			if !tc.captured && (scope.Inquiry.visibilityRelationPath != nil || scope.Inquiry.visibilityOtherPath != nil) {
				t.Fatal("unrelated statement retained visibility paths")
			}
		})
	}
}

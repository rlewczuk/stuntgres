package core_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqltype"
)

// Inspect the existing statement-owned immutable capture after real execution,
// rather than asserting implementation wiring or incidental source text.
func TestVisibilityInquiryCaptureDemand(t *testing.T) {
	for _, name := range []string{"pg_type_is_visible", "pg_ts_config_is_visible", "pg_ts_dict_is_visible", "pg_ts_parser_is_visible", "pg_ts_template_is_visible", "pg_statistics_obj_is_visible"} {
		t.Run(name, func(t *testing.T) { visibilityInquiryCaptureDemand(t, name) })
	}
}

func visibilityInquiryCaptureDemand(t *testing.T, name string) {
	query := func(q string) string {
		q = strings.ReplaceAll(q, "pg_type_is_visible", name)
		if name != "pg_type_is_visible" {
			q = strings.ReplaceAll(q, "IS NOT NULL", "IS NULL")
		}
		return q
	}
	e := engine.New()
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
		sessionSQL(t, s, query(q))
	}
	for _, tc := range []struct {
		sql      string
		captured bool
	}{
		{"SELECT id FROM capture_ids", false},
		{"SELECT id FROM capture_plain", false},
		{"SELECT pg_type_is_visible(NULL)", false},
		{"SELECT pg_type_is_visible($1)", true},
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
			count := 0
			if strings.Contains(tc.sql, "$1") {
				count = 1
			}
			result, err := scope.Execute(enginetest.Statement(t, query(tc.sql)), count, func(ctx context.Context, info analyze.ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
				if count == 0 {
					return nil
				}
				value, err := sqltype.OIDValue(info.Types[0].Type, 0)
				frame[0] = value
				return err
			})
			if result.Rows != nil {
				defer result.Rows.Close()
			}
			defer scope.Finish(err)
			if err != nil {
				t.Fatal(err)
			}
			if got := core.InquiryVisibilityCatalog(&scope.Inquiry).State() != nil; got != tc.captured {
				t.Fatalf("visibility capture=%v, want %v", got, tc.captured)
			}
			if !tc.captured && (core.InquiryVisibilityRelationPath(&scope.Inquiry) != nil || core.InquiryVisibilityOtherPath(&scope.Inquiry) != nil) {
				t.Fatal("unrelated statement retained visibility paths")
			}
		})
	}
}

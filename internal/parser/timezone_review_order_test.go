package parser

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestTimezoneReviewOrderParser(t *testing.T) {
	query := `SELECT c::timestamp AT TIME ZONE 'UTC'::text AT TIME ZONE 'America/New_York' converted, c AT LOCAL AS local_value FROM t ORDER BY c AT TIME ZONE 'UTC' ASC NULLS LAST, id DESC NULLS FIRST`
	statements, err := Parse(query)
	if err != nil {
		t.Fatal(err)
	}
	s := statements[0].(*Select)
	if len(s.Targets) != 2 || s.Labels[0].Text != "converted" || s.Labels[1].Text != "local_value" {
		t.Fatalf("projection: %#v", s)
	}
	outer, ok := s.Targets[0].(TimeZoneExpr)
	if !ok {
		t.Fatalf("outer conversion: %T", s.Targets[0])
	}
	inner, ok := outer.Value.(TimeZoneExpr)
	if !ok {
		t.Fatalf("AT must associate left: %T", outer.Value)
	}
	if _, ok := inner.Value.(CastExpr); !ok {
		t.Fatalf("value cast must bind before AT: %T", inner.Value)
	}
	if _, ok := inner.Zone.(CastExpr); !ok {
		t.Fatalf("zone cast must bind before AT: %T", inner.Zone)
	}
	local, ok := s.Targets[1].(TimeZoneExpr)
	if !ok || !local.Local {
		t.Fatalf("AT LOCAL: %#v", s.Targets[1])
	}
	if s.Order == nil || len(s.Order.Keys) != 2 {
		t.Fatalf("order keys: %#v", s.Order)
	}
	first, second := s.Order.Keys[0], s.Order.Keys[1]
	if _, ok := first.Expr.(TimeZoneExpr); !ok || first.Desc || first.Nulls != NullsLast || !second.Desc || second.Nulls != NullsFirst {
		t.Fatalf("order expressions: %#v", s.Order.Keys)
	}
}

func TestTimezoneReviewOrderQuotedLiterals(t *testing.T) {
	for _, query := range []string{
		`SELECT "timestamptz" '2004-07-01 12:00+00' AT TIME ZONE 'UTC' converted FROM t ORDER BY converted,id`,
		`SELECT pg_catalog."timestamptz"(3) '2004-07-01 12:00+00' AT LOCAL AS converted FROM t ORDER BY converted DESC NULLS LAST,id`,
		`SELECT pg_catalog."timetz" '05:34:17-05' AT TIME ZONE 'UTC' converted FROM t ORDER BY converted,id`,
		`SELECT c AT TIME ZONE 'UTC' = "timestamptz" '2004-07-01 12:00+00' AS equal_value FROM t ORDER BY c AT TIME ZONE 'UTC' IS NULL,id`,
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal(query, err)
		}
	}
}

func TestTimezoneReviewOrderWholeInput(t *testing.T) {
	for _, query := range []string{
		`SELECT c AT TIME ZONE 'UTC' converted FROM t ORDER BY converted,id; garbage`,
		`SELECT c AT TIME ZONE 'UTC' converted FROM t ORDER BY c AT TIME ZONE 'UTC' NULLS LAST,id,`,
		`SELECT "timestamptz" '2004-07-01 12:00+00' AT LOCAL AS converted FROM t ORDER BY converted; SELECT FROM`,
	} {
		_, err := Parse(query)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatalf("whole input %q: %v", query, err)
		}
	}
}

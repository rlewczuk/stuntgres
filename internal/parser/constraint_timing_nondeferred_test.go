package parser

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestConstraintTimingNondeferrableKinds(t *testing.T) {
	for _, tc := range []struct{ clause, kind string }{
		{"CHECK(v>0)", "CHECK"},
		{"NOT NULL v", "NOT NULL"},
	} {
		for _, property := range []string{"NOT DEFERRABLE", "INITIALLY IMMEDIATE", "NOT DEFERRABLE INITIALLY IMMEDIATE"} {
			q := "CREATE TABLE t(v INT,CONSTRAINT c " + tc.clause + " " + property + ")"
			if _, err := Parse(q); err != nil {
				t.Fatal(q, err)
			}
		}
		for _, property := range []string{"DEFERRABLE", "INITIALLY DEFERRED"} {
			for _, prefix := range []string{"CREATE TABLE t(v INT,CONSTRAINT c ", "ALTER TABLE t ADD CONSTRAINT c "} {
				q := prefix + tc.clause + " " + property
				if strings.HasPrefix(q, "CREATE") {
					q += ")"
				}
				statements, err := Parse(q)
				var diagnostic *sqlstate.Error
				if statements != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != tc.kind+" constraints cannot be marked DEFERRABLE" || diagnostic.Position != strconv.Itoa(strings.LastIndex(q, property)+1) {
					t.Fatal(q, statements, diagnostic, err)
				}
			}
		}
	}
}

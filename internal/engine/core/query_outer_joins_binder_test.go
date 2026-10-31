package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqlstate"
)

func TestQueryOuterJoinsFullLegality(t *testing.T) {
	for _, tc := range []struct {
		condition string
		legal     bool
	}{
		{"b.k=a.k", true}, {"a.k+1=b.k+1", true}, {"CAST(a.k AS bigint)=b.k", true},
		{"a.k=a.k", false}, {"a.k=1", false}, {"a.k+b.k=b.k", false}, {"a.k<b.k", false},
		{"NOT NOT (a.k=b.k)", true}, {"NOT (NOT (a.k=b.k) OR FALSE)", true},
		{"(a.k=b.k AND a.q=9) OR (a.k=b.k AND b.q=8)", true},
		{"a.k=b.k OR a.q=9", false}, {"(a.k<b.k) AND FALSE", true},
		{"(a.k<b.k) OR (1+1=2)", true}, {"NULL::boolean AND a.k<b.k", false},
		{"CASE WHEN 1+1=2 THEN a.k=b.k ELSE a.k<b.k END", true},
		{"(a.k<b.k) AND (2=3)", true}, {"NOT (TRUE OR a.k<b.k)", true},
		{"CASE WHEN 2=3 THEN a.k<b.k ELSE FALSE END", true},
		{"COALESCE(NULL::boolean,TRUE)", true}, {"COALESCE(a.k=b.k,FALSE)", false},
		{"COALESCE(NULL::boolean,a.k=b.k)", false},
		{"COALESCE(NULL::boolean,CASE WHEN TRUE THEN a.k=b.k ELSE FALSE END)", false},
	} {
		t.Run(tc.condition, func(t *testing.T) {
			e, query, _ := queryJoinsFixture(t, "SELECT a.k FROM a FULL JOIN b ON "+tc.condition)
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			root, err := view.PreflightQuery(&evaluation, query.Root)
			if err != nil {
				t.Fatal(err)
			}
			err = view.ValidateQueryJoinExecution(&evaluation, root, e.CatalogRead(), 0)
			if tc.legal && err != nil || !tc.legal && err == nil {
				t.Fatal(tc, err)
			}
			if !tc.legal {
				enginetest.State(t, err, "0A000")
			}
		})
	}
}

func TestQueryOuterJoinsNestedMergedKeyDependencies(t *testing.T) {
	for _, queryText := range []string{
		"SELECT j.k FROM (a FULL JOIN wide USING(k)) j FULL JOIN b ON j.k=b.k",
		"SELECT j.k FROM b FULL JOIN (a FULL JOIN wide USING(k)) j ON b.k=j.k",
		"SELECT a.k FROM a FULL JOIN b ON a.l||current_user=b.r",
		"SELECT a.k FROM a FULL JOIN b ON CASE WHEN a.q=9 THEN a.k ELSE a.k+1 END=b.k",
	} {
		e, query, _ := queryJoinsFixture(t, queryText)
		c := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		root, err := view.PreflightQuery(&c, query.Root)
		if err != nil {
			t.Fatal(queryText, err)
		}
		if err := view.ValidateQueryJoinExecution(&c, root, e.CatalogRead(), 0); err != nil {
			t.Fatal(queryText, err)
		}
	}
}

func TestQueryOuterJoinsExecutionRowmarkModes(t *testing.T) {
	for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
		for _, clause := range []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE"} {
			e, query, _ := queryJoinsFixture(t, "SELECT a.k FROM a "+kind+" JOIN b ON a.k=b.k LIMIT 0 "+clause)
			c := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			root, err := view.PreflightQuery(&c, query.Root)
			if err != nil {
				t.Fatal(err)
			}
			err = view.ValidateQueryJoinExecution(&c, root, e.CatalogRead(), 0)
			enginetest.State(t, err, "0A000")
			var diagnostic *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: clause + " cannot be applied to the nullable side of an outer join"}
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal(err)
			}
		}
	}
}

package types_test

import (
	"context"
	"testing"

	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Begin is the real planning operation: an ineligible definition prunes root
// syntax instead of lending its accepted backing to the next attempt.
func foldRootsReusable(owner *core.PreparedFolds) bool {
	if owner == nil || !owner.Live() {
		return false
	}
	current := owner.Current()
	next := owner.Begin()
	for i, entry := range current.Entries {
		if entry.Origin.Owner == (ids.ObjectRef{}) {
			return len(next.Candidate.Entries) == len(current.Entries) && &next.Candidate.Entries[i] == &current.Entries[i]
		}
	}
	return false
}

// Exercise the eligibility transition, including empty accepted groups that
// cannot demonstrate reuse by lending a root entry. Call only after all other
// observations: this deliberately retires the definition's next-use rights.
func assertFoldReuseRetirement(t *testing.T, owner *core.PreparedFolds, reusable bool) {
	t.Helper()
	before := owner.Current().Generation
	owner.Invalidate(false)
	want := before
	if reusable {
		want++
	}
	if owner.Current().Generation != want {
		t.Fatal("reuse retirement changed the wrong generation", before, owner.Current().Generation, want)
	}
	if foldRootsReusable(owner) {
		t.Fatal("retired root syntax remains reusable")
	}
}

func TestPreparedFoldTypedOwnership(t *testing.T) {
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	cases := []struct {
		expression string
		id         sqltype.ID
		value      string
	}{
		{"abs(-7::integer)", sqltype.Int4, "7"},
		{"COALESCE(-(7::integer),0)", sqltype.Int4, "-7"},
		{"round(-12.56::numeric)", sqltype.Numeric, "-13"},
		{"lpad('xy',4)", sqltype.Text, "  xy"},
		{"length((point '0.10000000000000002,1')::text)", sqltype.Int4, "23"},
		{"greatest(NULL::integer,NULL::integer)", sqltype.Int4, "NULL"},
		{"array_append('[0:1]={2,3}'::int[],4)", 1007, "[0:2]={2,3,4}"},
		{"ARRAY[1,NULL,3]", 1007, "{1,NULL,3}"},
		{"COALESCE(ROW(7,'owned'),NULL)", 2249, "(7,owned)"},
	}
	for _, tc := range cases {
		t.Run(tc.expression, func(t *testing.T) {
			ast, d := enginetest.PreparedDescription(t, e, s, "SELECT "+tc.expression+" AS v")
			if d.Folds == nil || d.Folds.Current().Entries != nil {
				t.Fatal("Describe computed a fold")
			}
			r, err := enginetest.PreparedExecute(e, s, ast, &d)
			if err != nil {
				t.Fatal(err)
			}
			r.Rows.Close()
			entry := enginetest.PreparedFoldEntry(t, d, tc.id, tc.value)
			if entry.Value.Type() != entry.Result.Type || entry.Result != d.Results[0].Usage {
				t.Fatal("lost exact result usage/provenance")
			}
			if tc.expression == "COALESCE(-(7::integer),0)" {
				if len(entry.Inputs) < 2 || entry.Inputs[len(entry.Inputs)-1].Usage.Type != nil || entry.Inputs[len(entry.Inputs)-1].Value.Type() != nil {
					t.Fatal("omitted unary input lost its ordered empty slot")
				}
			}
			before := enginetest.SnapshotFoldDescription(d)
			r, err = enginetest.PreparedExecute(e, s, ast, &d)
			if err != nil {
				t.Fatal(err)
			}
			r.Rows.Close()
			if &before.Accepted.Entries[0] != &d.Folds.Current().Entries[0] || !enginetest.SamePublishedFold(entry, enginetest.PreparedFoldEntry(t, d, tc.id, tc.value)) {
				t.Fatal("valid use replaced immutable backing")
			}
			if tc.id == 1007 {
				v := entry.Value
				dimensions := v.ArrayDimensions()
				dimensions[0].Lower = 99
				if v.ArrayRank() != 1 || v.ArrayDimension(0).Lower == 99 || v.ArrayLen() != 3 {
					t.Fatal("array dimension alias")
				}
				if tc.value == "{1,NULL,3}" && (!v.ArrayElement(1).IsNull() || v.ArrayElement(1).Type().ID() != sqltype.Int4) {
					t.Fatal("lost typed member NULL")
				}
			}
		})
	}
	// Bare arithmetic and ROW projections are execution-only under ordinary
	// preflight demand. COALESCE above demands their immutable typed results;
	// the unary operator has a cast child, not a parser-negated input literal.
	// Direct selected calls such as -abs(...) cross the parser's existing
	// selected-call arithmetic frontier; this witness needs no grammar change.
	// Real immutable composite construction retains exact catalog-owned leaves.
	enginetest.SessionSQL(t, s, "CREATE TYPE fold_pair AS (i integer,n text)")
	ast, d := enginetest.PreparedDescription(t, e, s, "SELECT COALESCE(ROW(7,'owned')::fold_pair,NULL::fold_pair) AS v")
	r, err := enginetest.PreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	r.Rows.Close()
	entry := enginetest.PreparedFoldEntry(t, d, d.Results[0].Usage.Type.ID(), "(7,owned)")
	compound := entry.Value
	if entry.Result != d.Results[0].Usage || compound.Type() != entry.Result.Type ||
		!compound.Type().IsComposite() || compound.CompositeLen() != 2 ||
		compound.CompositeField(0).Type().ID() != sqltype.Int4 ||
		compound.CompositeField(1).Type().ID() != sqltype.Text ||
		len(entry.Inputs) != 2 {
		t.Fatal("missing typed compound constructor artifact")
	}
	before := enginetest.SnapshotFoldDescription(d)
	r, err = enginetest.PreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	r.Rows.Close()
	if &before.Accepted.Entries[0] != &d.Folds.Current().Entries[0] {
		t.Fatal("compound hit copied datum/group")
	}

}

func TestPreparedFoldOccurrenceProvenance(t *testing.T) {
	t.Run("StoredOrigins", func(t *testing.T) {
		e := rootengine.New()
		s := enginetest.StartSession(t, e, nil)
		constant := "(point '0.10000000000000002,1')::text"
		for _, q := range []string{
			"CREATE TABLE fold_defaults(v text DEFAULT " + constant + ")",
			"CREATE VIEW fold_view AS SELECT " + constant + " AS p",
			"CREATE FUNCTION fold_default_call(v text DEFAULT " + constant + ") RETURNS text LANGUAGE SQL AS $$ SELECT v $$",
		} {
			enginetest.SessionSQL(t, s, q)
		}
		for _, tc := range []struct {
			query string
			class ids.OID
			count int
		}{
			{"INSERT INTO fold_defaults DEFAULT VALUES", ids.RelationClassID, 1},
			{"SELECT p FROM fold_view", ids.RelationClassID, 1},
			{"SELECT fold_default_call() AS a,fold_default_call() AS b", ids.ProcedureClassID, 2},
		} {
			enginetest.SessionSQL(t, s, "SET extra_float_digits=3")
			ast, d := enginetest.PreparedDescription(t, e, s, tc.query)
			enginetest.PreparedFoldExecute(t, e, s, ast, &d)
			origins := make(map[exec.FoldOrigin]bool)
			for _, entry := range d.Folds.Current().Entries {
				if entry.Origin.Owner.Address.ClassID != tc.class {
					t.Fatal("stored fold lost its defining owner", entry.Origin)
				}
				origins[entry.Origin] = true
			}
			if len(origins) != tc.count {
				t.Fatal("stored defaults/view/invocation sites aliased", tc.query, origins)
			}
			enginetest.PreparedFoldEntry(t, d, sqltype.Text, "(0.10000000000000002,1)")
			before := enginetest.SnapshotFoldDescription(d)
			enginetest.SessionSQL(t, s, "SET extra_float_digits=0")
			enginetest.PreparedFoldExecute(t, e, s, ast, &d)
			if &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
				t.Fatal("unchanged stored origin missed its retained backing", tc.query)
			}
		}
	})
}
func TestPreparedFoldDemandBarriers(t *testing.T) {
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, query := range []string{
		"SELECT current_timestamp AS v",
		"SELECT random() AS v",
	} {
		ast, d := enginetest.PreparedDescription(t, e, s, query)
		r, err := enginetest.PreparedExecute(e, s, ast, &d)
		if err != nil {
			t.Fatal(err)
		}
		r.Rows.Close()
		if len(d.Folds.Current().Entries) != 0 {
			t.Fatal("stable/volatile value retained")
		}
	}
	enginetest.SessionSQL(t, s, "CREATE TABLE fold_rows(v int)")
	enginetest.SessionSQL(t, s, "INSERT INTO fold_rows VALUES(-7)")
	ast, d := enginetest.PreparedDescription(t, e, s, "SELECT abs(v) AS v FROM fold_rows")
	r, err := enginetest.PreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	r.Rows.Close()
	if len(d.Folds.Current().Entries) != 0 {
		t.Fatal("row-dependent result retained")
	}
	// Conditional demand skips an unreached provider, without filling its key.
	conditional, conditionalDescription := enginetest.PreparedDescription(t, e, s, "SELECT coalesce('safe',(1/0)::text) AS v")
	enginetest.PreparedFoldExecute(t, e, s, conditional, &conditionalDescription)
	for _, entry := range conditionalDescription.Folds.Current().Entries {
		if entry.Result.Type.ID() != sqltype.Text {
			t.Fatal("skipped arithmetic provider was demanded")
		}
	}
	enginetest.SessionSQL(t, s, "CREATE DOMAIN fold_positive AS integer CHECK(VALUE>0)")
	domainStatement, domainDescription := enginetest.PreparedDescription(t, e, s, "SELECT (-1)::fold_positive AS v")
	if _, err := enginetest.PreparedExecute(e, s, domainStatement, &domainDescription); err == nil {
		t.Fatal("runtime domain truth was elided")
	}
	for _, entry := range domainDescription.Folds.Current().Entries {
		if entry.Result.Type.IsDomain() {
			t.Fatal("runtime-checked domain result persisted")
		}
	}
	// Execute a genuine fixed formal frame, including a constant sibling. The
	// entire definition group stays untouched rather than persisting either arm.
	ast, d = enginetest.PreparedDescription(t, e, s, "SELECT ($1::point)::text AS p,abs(-7) AS n")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = &d
	r, err = scope.Execute(ast, 1, func(ctx context.Context, info analyze.ParameterInfo, frame []sqltype.Value, env sqltype.InputEnvironment) error {
		value, err := info.Types[0].Type.InputWithEnvironment(ctx, "0.10000000000000002,1", -1, env)
		frame[0] = value
		return err
	})
	if err = scope.Finish(err); err != nil {
		t.Fatal(err)
	}
	r.Rows.Close()
	if d.Folds.Current().Entries != nil || d.Folds.Current().Generation != 0 {
		t.Fatal("parameter or constant sibling persisted")
	}
	assertFoldReuseRetirement(t, d.Folds, false)
}

package core_test

import (
	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Save actual immutable accepted payloads, never a shallow copy of a mutable owner.
type savedFoldDescription struct {
	core.Description
	Accepted exec.AcceptedFolds
	Environment core.PreparedEnvironment
	Live, ReusableBacking bool
}

func snapshotFoldDescription(d core.Description) savedFoldDescription {
	return savedFoldDescription{Description: d, Accepted: d.Folds.Current(), Environment: d.Folds.Environment, Live: d.Folds.Live(), ReusableBacking: foldBackingReusable(d.Folds)}
}

// Stored origins can remain operationally reusable after root invalidation.
// Observe the candidate actually lent to planning, including stored-only groups;
// private eligibility itself is asserted in the owner's state-machine tests.
func foldBackingReusable(owner *core.PreparedFolds) bool {
	if owner == nil || !owner.Live() {
		return false
	}
	current := owner.Current()
	next := owner.Begin()
	return len(current.Entries) != 0 && len(next.Candidate.Entries) == len(current.Entries) && &next.Candidate.Entries[0] == &current.Entries[0]
}

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

func cloneFoldDescription(t *testing.T, d core.Description) core.Description {
	t.Helper()
	owner := core.NewPreparedFolds()
	owner.Analysis, owner.Environment, owner.RoleSensitive = d.Folds.Analysis, d.Folds.Environment, d.Folds.RoleSensitive
	attempt := owner.Begin()
	attempt.Candidate.Entries = d.Folds.Current().Entries
	attempt.Candidate.Dependencies = d.Folds.Current().Dependencies
	attempt.Candidate.Private, attempt.Candidate.DependenciesPrivate = true, true
	if _, err := owner.Accept(&attempt); err != nil { t.Fatal(err) }
	d.Folds = owner
	return d
}

func preparedFoldEntry(t *testing.T, description any, id sqltype.ID, text string) exec.FoldConstant {
	t.Helper()
	var entries []exec.FoldConstant
	switch d := description.(type) {
	case core.Description:
		if !foldBackingReusable(d.Folds) {
			t.Fatal("no accepted live fold group")
		}
		entries = d.Folds.Current().Entries
	case savedFoldDescription:
		if !d.Live || !d.ReusableBacking {
			t.Fatal("no saved accepted live fold group")
		}
		entries = d.Accepted.Entries
	default:
		t.Fatalf("unexpected description %T", description)
	}
	for _, entry := range entries {
		if entry.Result.Type.ID() != id { continue }
		if entry.Value.IsNull() && text == "NULL" { return entry }
		got, err := entry.Result.Type.Output(context.Background(), entry.Value)
		if err == nil && got == text { return entry }
	}
	t.Fatalf("missing owned %d datum %q in %+v", id, text, entries)
	return exec.FoldConstant{}
}

// Compare the public planning payload, not private input representation. Real
// second-use backing/hit checks accompany these comparisons; exec's owner-local
// tests distinguish provider markers from omitted and invalid literal inputs.
func samePublishedFold(a, b exec.FoldConstant) bool {
	callable := a.Callable == b.Callable
	if !callable {
		left, lok := a.Callable.(*catalog.Procedure)
		right, rok := b.Callable.(*catalog.Procedure)
		callable = lok && rok && exec.SameRoutineDeclaration(left, right)
	}
	if a.Offset != b.Offset || a.Occurrence != b.Occurrence || a.Source != b.Source || a.Origin != b.Origin || a.Frame != b.Frame || a.Provider != b.Provider || !callable || a.Operator != b.Operator || a.Result != b.Result || !sqltype.SameDatum(a.Value, b.Value) || !a.Coercion.Equal(b.Coercion) || len(a.Inputs) != len(b.Inputs) {
		return false
	}
	for i, input := range a.Inputs {
		other := b.Inputs[i]
		if input.Usage != other.Usage || input.UnknownNull != other.UnknownNull {
			return false
		}
		if input.Value.Type() == nil && other.Value.Type() == nil {
			if input.Value != other.Value {
				return false
			}
		} else if !sqltype.SameDatum(input.Value, other.Value) {
			return false
		}
	}
	return true
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
			ast, d := sequencePreparedDescription(t, e, s, "SELECT "+tc.expression+" AS v")
			if d.Folds == nil || d.Folds.Current().Entries != nil {
				t.Fatal("Describe computed a fold")
			}
			r, err := sequencePreparedExecute(e, s, ast, &d)
			if err != nil {
				t.Fatal(err)
			}
			r.Rows.Close()
			entry := preparedFoldEntry(t, d, tc.id, tc.value)
			if entry.Value.Type() != entry.Result.Type || entry.Result != d.Results[0].Usage {
				t.Fatal("lost exact result usage/provenance")
			}
			if tc.expression == "COALESCE(-(7::integer),0)" {
				if len(entry.Inputs) < 2 || entry.Inputs[len(entry.Inputs)-1].Usage.Type != nil || entry.Inputs[len(entry.Inputs)-1].Value.Type() != nil {
					t.Fatal("omitted unary input lost its ordered empty slot")
				}
			}
			before := snapshotFoldDescription(d)
			r, err = sequencePreparedExecute(e, s, ast, &d)
			if err != nil {
				t.Fatal(err)
			}
			r.Rows.Close()
			if &before.Accepted.Entries[0] != &d.Folds.Current().Entries[0] || !samePublishedFold(entry, preparedFoldEntry(t, d, tc.id, tc.value)) {
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
	ast, d := sequencePreparedDescription(t, e, s, "SELECT COALESCE(ROW(7,'owned')::fold_pair,NULL::fold_pair) AS v")
	r, err := sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	r.Rows.Close()
	entry := preparedFoldEntry(t, d, d.Results[0].Usage.Type.ID(), "(7,owned)")
	compound := entry.Value
	if entry.Result != d.Results[0].Usage || compound.Type() != entry.Result.Type ||
		!compound.Type().IsComposite() || compound.CompositeLen() != 2 ||
		compound.CompositeField(0).Type().ID() != sqltype.Int4 ||
		compound.CompositeField(1).Type().ID() != sqltype.Text ||
		len(entry.Inputs) != 2 {
		t.Fatal("missing typed compound constructor artifact")
	}
	before := snapshotFoldDescription(d)
	r, err = sequencePreparedExecute(e, s, ast, &d)
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
			ast, d := sequencePreparedDescription(t, e, s, tc.query)
			preparedFoldExecute(t, e, s, ast, &d)
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
			preparedFoldEntry(t, d, sqltype.Text, "(0.10000000000000002,1)")
			before := snapshotFoldDescription(d)
			enginetest.SessionSQL(t, s, "SET extra_float_digits=0")
			preparedFoldExecute(t, e, s, ast, &d)
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
		ast, d := sequencePreparedDescription(t, e, s, query)
		r, err := sequencePreparedExecute(e, s, ast, &d)
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
	ast, d := sequencePreparedDescription(t, e, s, "SELECT abs(v) AS v FROM fold_rows")
	r, err := sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	r.Rows.Close()
	if len(d.Folds.Current().Entries) != 0 {
		t.Fatal("row-dependent result retained")
	}
	// Conditional demand skips an unreached provider, without filling its key.
	conditional, conditionalDescription := sequencePreparedDescription(t, e, s, "SELECT coalesce('safe',(1/0)::text) AS v")
	preparedFoldExecute(t, e, s, conditional, &conditionalDescription)
	for _, entry := range conditionalDescription.Folds.Current().Entries {
		if entry.Result.Type.ID() != sqltype.Text {
			t.Fatal("skipped arithmetic provider was demanded")
		}
	}
	enginetest.SessionSQL(t, s, "CREATE DOMAIN fold_positive AS integer CHECK(VALUE>0)")
	domainStatement, domainDescription := sequencePreparedDescription(t, e, s, "SELECT (-1)::fold_positive AS v")
	if _, err := sequencePreparedExecute(e, s, domainStatement, &domainDescription); err == nil {
		t.Fatal("runtime domain truth was elided")
	}
	for _, entry := range domainDescription.Folds.Current().Entries {
		if entry.Result.Type.IsDomain() {
			t.Fatal("runtime-checked domain result persisted")
		}
	}
	// Execute a genuine fixed formal frame, including a constant sibling. The
	// entire definition group stays untouched rather than persisting either arm.
	ast, d = sequencePreparedDescription(t, e, s, "SELECT ($1::point)::text AS p,abs(-7) AS n")
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

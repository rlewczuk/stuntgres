package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func geometryAlignmentStoredChecks(t *testing.T, b fixture.Backend, run func(fixture.Step)) {
	t.Helper()
	columns := []string{"lh", "sv", "lp", "sp", "ba", "bs", "ca", "cs", "lv", "sh", "ll", "ss", "absent", "nan_same"}
	types := []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}
	values := []any{true, true, true, true, true, false, true, false, false, false, true, false, nil, true}
	nulls := []any{nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, true}
	for _, suffix := range []string{" ORDER BY id", " WHERE FALSE"} {
		rows := [][]any{values, nulls}
		if suffix == " WHERE FALSE" {
			rows = nil
		}
		s := boxSelect(b, "SELECT lh,sv,lp,sp,ba,bs,ca,cs,lv,sh,ll,ss,absent,nan_same FROM geom_alignment_view"+suffix, columns, types, rows)
		run(s)
		run(fixture.Prepared(s))
	}
	// Execute reconstructed providers on a new physical row on every target.
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("INSERT INTO geom_alignment_smoke VALUES(7,'{0,1,0}','[(0,0),(0,4)]','(2,2),(0,0)','<(0,0),2>')", 1))
	s := boxSelect(b, "SELECT lh,sv,lp,sp,ba,bs,ca,cs,lv,sh,ll,ss,absent,nan_same FROM geom_alignment_view WHERE id=7", columns, types, [][]any{values})
	run(s)
	run(fixture.Prepared(s))
	run(fixture.Exec("ROLLBACK", 0))
	geometryAlignmentScalarChecks(t, b, run)
}

func TestReferenceGeometryAlignmentFeatureSmoke(t *testing.T) {
	geometryAlignmentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE geom_alignment_smoke(id integer,l line,s lseg,b box,c circle)", 0))
		run(fixture.Exec("INSERT INTO geom_alignment_smoke VALUES(1,'{0,1,0}','[(0,0),(0,4)]','(2,2),(0,0)','<(0,0),2>'),(2,NULL,NULL,NULL,NULL)", 2))
		run(fixture.Exec("CREATE VIEW geom_alignment_view AS SELECT id,?- l AS lh,?| s AS sv,l ?-| line '{1,0,7}' AS lp,s ?|| lseg '[(3,0),(3,9)]' AS sp,b = box '(14,11),(10,10)' AS ba,b ~= box '(14,11),(10,10)' AS bs,c = circle '<(9,9),2>' AS ca,c ~= circle '<(9,9),2>' AS cs,?| l AS lv,?- s AS sh,l ?|| line '{0,1,7}' AS ll,s ?-| lseg '[(3,0),(3,9)]' AS ss,?- NULL::line AS absent,box '(NaN,1),(0,0)' ~= box '(NaN,1),(0,0)' AS nan_same FROM geom_alignment_smoke", 0))
		run(fixture.Exec("CREATE TABLE geom_alignment_scalar(id integer,l line,s lseg,lh boolean GENERATED ALWAYS AS (?- l) STORED,lv boolean GENERATED ALWAYS AS (?| l) STORED,sh boolean GENERATED ALWAYS AS (?- s) STORED,sv boolean GENERATED ALWAYS AS (?| s) STORED,CONSTRAINT alignment_axes CHECK ((?- l OR ?| l) AND (?- s OR ?| s)))", 0))
		run(fixture.Exec("INSERT INTO geom_alignment_scalar(id,l,s) VALUES(1,'{0,1,0}','[(0,0),(0,4)]'),(2,'{1,0,7}','[(0,0),(4,0)]'),(3,NULL,NULL)", 3))
		geometryAlignmentStoredChecks(t, b, run)
		q := "SELECT $1::line ?|| $2::line AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			a, z     any
			want     any
			overflow bool
		}{
			{"{0,1,0}", "{0,1,7}", true, false}, {"{0,1,0}", "{1,0,7}", false, false},
			{"{1,1,1e308}", "{1,-1,1e308}", nil, true}, {"{0,1,0}", "{0,1,7}", true, false}, {nil, "{0,1,0}", nil, false},
		} {
			s := boxSelect(b, q, []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.a, tc.z)
			if tc.overflow {
				s = boxError(q, "22003", "value out of range: overflow", "", "", false, true, tc.a, tc.z)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		stmt.Close()
		run(boxError("SELECT NULL::line ?|| NULL::lseg", "42883", "operator does not exist: line ?|| lseg", "19", "No operator matches the given name and argument types. You might need to add explicit type casts.", false, false))
		run(fixture.Exec("CREATE TABLE geom_alignment_atomic(id integer,s lseg,flag boolean)", 0))
		run(fixture.Exec("INSERT INTO geom_alignment_atomic VALUES(1,'[(0,0),(1,1)]',false),(2,'[(1e308,1),(-1e308,2)]',false)", 2))
		for _, prepared := range []bool{false, true} {
			run(boxError("UPDATE geom_alignment_atomic SET flag=s ?|| lseg '[(0,0),(1,1)]'", "22003", "value out of range: overflow", "", "", prepared, true))
			run(fixture.Select("SELECT id,flag FROM geom_alignment_atomic ORDER BY id", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), false}, {int64(2), false}}))
		}
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			geometryAlignmentStoredChecks(t, b, run)
			target := fixture.NewFixture(t, false, true)
			if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			geometryAlignmentStoredChecks(t, b, func(s fixture.Step) { fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s) })
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal(err)
				}
				geometryAlignmentStoredChecks(t, fixture.Backend{PG: true}, func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) })
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}

func geometryAlignmentScalarChecks(t *testing.T, b fixture.Backend, run func(fixture.Step)) {
	t.Helper()
	columns := []string{"id", "lh", "lv", "sh", "sv"}
	types := []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL"}
	want := [][]any{{int64(1), true, false, false, true}, {int64(2), false, true, true, false}, {int64(3), nil, nil, nil, nil}}
	for _, suffix := range []string{" ORDER BY id", " WHERE FALSE"} {
		rows := want
		if suffix == " WHERE FALSE" {
			rows = nil
		}
		s := boxSelect(b, "SELECT id,lh,lv,sh,sv FROM geom_alignment_scalar"+suffix, columns, types, rows)
		run(s)
		run(fixture.Prepared(s))
	}
	// Each restored target must execute all four reconstructed scalar providers
	// for newly written rows, rather than only retaining generated values.
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Prepared(fixture.Exec("INSERT INTO geom_alignment_scalar(id,l,s) VALUES($1,$2,$3)", 1, 7, "{0,1,0}", "[(0,0),(0,4)]")))
	s := boxSelect(b, "SELECT id,lh,lv,sh,sv FROM geom_alignment_scalar WHERE id=7", columns, types, [][]any{{int64(7), true, false, false, true}})
	run(s)
	run(fixture.Prepared(s))
	run(fixture.Exec("UPDATE geom_alignment_scalar SET l='{1,0,7}',s='[(0,0),(4,0)]' WHERE id=7", 1))
	s = boxSelect(b, "SELECT id,lh,lv,sh,sv FROM geom_alignment_scalar WHERE id=7", columns, types, [][]any{{int64(7), false, true, true, false}})
	run(s)
	run(fixture.Prepared(s))
	run(fixture.Exec("ROLLBACK", 0))
	d := fixture.ControlsReferenceDiagnostic("23514", "new row for relation \"geom_alignment_scalar\" violates check constraint \"alignment_axes\"")
	d.Schema, d.Table, d.Constraint = "public", "geom_alignment_scalar", "alignment_axes"
	d.Detail = "Failing row contains (9, {1,1,0}, [(0,0),(1,0)], f, f, t, f)."
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic("INSERT INTO geom_alignment_scalar(id,l,s) VALUES(9,'{1,1,0}','[(0,0),(1,0)]')", d)
		s.Prepared, s.ErrorStage = prepared, "execute"
		run(s)
		run(boxSelect(b, "SELECT id,lh,lv,sh,sv FROM geom_alignment_scalar ORDER BY id", columns, types, want))
	}
}

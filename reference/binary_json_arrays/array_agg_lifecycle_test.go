package reference_test

import (
	"bytes"
	"context"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func arrayAggAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
	t.Helper()
	run(fixture.Exec("CREATE TABLE aa_target(v INT[])", 0))
	run(fixture.Exec("INSERT INTO aa_target VALUES('{99}')", 1))
	run(fixture.Exec("CREATE TABLE aa_late(k INT,id INT,v INT[])", 0))
	run(fixture.Exec("INSERT INTO aa_late VALUES(1,1,'{1,2}'),(2,2,'{3,4}'),(2,3,NULL)", 3))
	for _, prepared := range []bool{false, true} {
		arrayAggRuntimeError(t, p, b, "SELECT array_agg(v ORDER BY id) FROM aa_late GROUP BY k ORDER BY k", prepared, fixture.Diagnostic{Code: "22004", Message: "cannot accumulate null arrays"}, "{{1,2}}")
		s := fixture.ImplicitError("INSERT INTO aa_target SELECT array_agg(v ORDER BY id) FROM aa_late GROUP BY k", fixture.Diagnostic{Code: "22004", Message: "cannot accumulate null arrays"})
		s.Prepared = prepared
		s.ErrorStage = "execute"
		run(s)
		run(fixture.ArrayReferenceSelect("SELECT v FROM aa_target", []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{99}"}}, b.PG))
	}
	run(fixture.Exec("UPDATE aa_late SET v='{3}' WHERE id=3", 1))
	for _, prepared := range []bool{false, true} {
		arrayAggRuntimeError(t, p, b, "SELECT array_agg(v ORDER BY id) FROM aa_late GROUP BY k ORDER BY k", prepared, fixture.Diagnostic{Code: "2202E", Message: "cannot accumulate arrays of different dimensionality"}, "{{1,2}}")
		s := fixture.ImplicitError("INSERT INTO aa_target SELECT array_agg(v ORDER BY id) FROM aa_late GROUP BY k", fixture.Diagnostic{Code: "2202E", Message: "cannot accumulate arrays of different dimensionality"})
		s.Prepared = prepared
		s.ErrorStage = "execute"
		run(s)
		run(fixture.ArrayReferenceSelect("SELECT v FROM aa_target", []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{99}"}}, b.PG))
	}
	arrayAggBoth(run, fixture.Exec("INSERT INTO aa_target SELECT array_agg(v ORDER BY id) FROM aa_scalar", 1))
	arrayAggBoth(run, fixture.Exec("INSERT INTO aa_target SELECT array_agg(v) FROM aa_scalar WHERE FALSE GROUP BY id", 0))
	arrayAggBoth(run, fixture.Exec("INSERT INTO aa_target SELECT array_agg(v) FROM aa_scalar WHERE FALSE", 1))
	run(fixture.Exec("CREATE TABLE aa_group_target(v INT[])", 0))
	arrayAggBoth(run, fixture.Exec("INSERT INTO aa_group_target SELECT array_agg(v ORDER BY id) FROM aa_scalar GROUP BY v", 3))
	run(fixture.ArrayReferenceSelect("SELECT v FROM aa_group_target ORDER BY v::text", []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{1}"}, {"{1}"}, {"{3,3}"}, {"{3,3}"}, {"{NULL,NULL}"}, {"{NULL,NULL}"}}, b.PG))
	run(fixture.ArrayReferenceSelect("SELECT v FROM aa_target ORDER BY v::text NULLS LAST", []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{3,NULL,1,NULL,3}"}, {"{3,NULL,1,NULL,3}"}, {"{99}"}, {nil}, {nil}}, b.PG))
}

func TestReferenceArrayAggLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			arrayAggSetup(run)
			arrayAggExecutionOwnership(t, p, b)
			q := "SELECT array_agg(v ORDER BY id) FROM aa_scalar WHERE id>$1::integer"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				arg  int64
				want any
			}{{5, nil}, {0, "{3,NULL,1,NULL,3}"}, {2, "{1,NULL,3}"}, {0, "{3,NULL,1,NULL,3}"}} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, arrayAggSelect(q, "_INT4", [][]any{{tc.want}}, b.PG, tc.arg))
			}
			arrayAggAtomicity(t, p, b, run)
			arrayAggDomainSetup(run)
			domain := arrayAggDomainSelect(b.PG)
			run(fixture.Exec("CREATE VIEW aa_view AS "+domain.Query, 0))
			run(fixture.Exec("ALTER TABLE aa_types RENAME TO aa_renamed", 0))
			domain.Query = "SELECT i,j,a,c,d FROM aa_view"
			arrayAggBoth(run, domain)
			if b.PG {
				return
			}
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			fixture.ArrayReferenceNativeRoundtrip(t, p, conn, b, domain)
			// Failed staged publication after a valid aggregate view preserves data,
			// settings and the already restored descriptor graph.
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("CREATE TABLE aa_sentinel(v TEXT)", 0))
			run(fixture.Exec("INSERT INTO aa_sentinel VALUES('kept')", 1))
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET application_name='array-agg-kept'", 0))
			bad := append(append([]byte(nil), dump.Bytes()...), []byte("\nCREATE VIEW aa_invalid AS SELECT missing FROM aa_renamed;\n")...)
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad)); err == nil {
				t.Fatal("invalid staged Restore succeeded")
			}
			run(fixture.Select("SELECT v FROM aa_sentinel", []string{"v"}, []string{"TEXT"}, [][]any{{"kept"}}))
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"array-agg-kept"}}))
			run(domain)
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := b.DB.QueryContext(ctx, "SELECT array_agg(v ORDER BY id) FROM aa_scalar")
			if rows != nil {
				rows.Close()
				t.Fatal("precanceled aggregate exposed cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel=%v", err)
			}
			result, err := b.DB.ExecContext(ctx, "INSERT INTO aa_target SELECT array_agg(v ORDER BY id) FROM aa_scalar")
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel insert=%v/%v", result, err)
			}
			run(fixture.ArrayReferenceSelect("SELECT v FROM aa_target ORDER BY v::text NULLS LAST", []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{3,NULL,1,NULL,3}"}, {"{3,NULL,1,NULL,3}"}, {"{99}"}, {nil}, {nil}}, false))
		})
	}
}

package reference_test

import (
	"bytes"
	"context"
	"errors"
	"stuntgres"
	"testing"
)

func arrayAggAtomicity(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
	t.Helper()
	run(referenceExec("CREATE TABLE aa_target(v INT[])", 0))
	run(referenceExec("INSERT INTO aa_target VALUES('{99}')", 1))
	run(referenceExec("CREATE TABLE aa_late(k INT,id INT,v INT[])", 0))
	run(referenceExec("INSERT INTO aa_late VALUES(1,1,'{1,2}'),(2,2,'{3,4}'),(2,3,NULL)", 3))
	for _, prepared := range []bool{false, true} {
		arrayAggRuntimeError(t, p, b, "SELECT array_agg(v ORDER BY id) FROM aa_late GROUP BY k ORDER BY k", prepared, referenceDiagnostic{Code: "22004", Message: "cannot accumulate null arrays"}, "{{1,2}}")
		s := referenceImplicitError("INSERT INTO aa_target SELECT array_agg(v ORDER BY id) FROM aa_late GROUP BY k", referenceDiagnostic{Code: "22004", Message: "cannot accumulate null arrays"})
		s.prepared = prepared
		s.errorStage = "execute"
		run(s)
		run(arrayReferenceSelect("SELECT v FROM aa_target", []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{99}"}}, b.pg))
	}
	run(referenceExec("UPDATE aa_late SET v='{3}' WHERE id=3", 1))
	for _, prepared := range []bool{false, true} {
		arrayAggRuntimeError(t, p, b, "SELECT array_agg(v ORDER BY id) FROM aa_late GROUP BY k ORDER BY k", prepared, referenceDiagnostic{Code: "2202E", Message: "cannot accumulate arrays of different dimensionality"}, "{{1,2}}")
		s := referenceImplicitError("INSERT INTO aa_target SELECT array_agg(v ORDER BY id) FROM aa_late GROUP BY k", referenceDiagnostic{Code: "2202E", Message: "cannot accumulate arrays of different dimensionality"})
		s.prepared = prepared
		s.errorStage = "execute"
		run(s)
		run(arrayReferenceSelect("SELECT v FROM aa_target", []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{99}"}}, b.pg))
	}
	arrayAggBoth(run, referenceExec("INSERT INTO aa_target SELECT array_agg(v ORDER BY id) FROM aa_scalar", 1))
	arrayAggBoth(run, referenceExec("INSERT INTO aa_target SELECT array_agg(v) FROM aa_scalar WHERE FALSE GROUP BY id", 0))
	arrayAggBoth(run, referenceExec("INSERT INTO aa_target SELECT array_agg(v) FROM aa_scalar WHERE FALSE", 1))
	run(referenceExec("CREATE TABLE aa_group_target(v INT[])", 0))
	arrayAggBoth(run, referenceExec("INSERT INTO aa_group_target SELECT array_agg(v ORDER BY id) FROM aa_scalar GROUP BY v", 3))
	run(arrayReferenceSelect("SELECT v FROM aa_group_target ORDER BY v::text", []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{1}"}, {"{1}"}, {"{3,3}"}, {"{3,3}"}, {"{NULL,NULL}"}, {"{NULL,NULL}"}}, b.pg))
	run(arrayReferenceSelect("SELECT v FROM aa_target ORDER BY v::text NULLS LAST", []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{3,NULL,1,NULL,3}"}, {"{3,NULL,1,NULL,3}"}, {"{99}"}, {nil}, {nil}}, b.pg))
}

func TestReferenceArrayAggLifecycle(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			arrayAggSetup(run)
			arrayAggExecutionOwnership(t, p, b)
			q := "SELECT array_agg(v ORDER BY id) FROM aa_scalar WHERE id>$1::integer"
			stmt, err := b.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}
			for _, tc := range []struct {
				arg  int64
				want any
			}{{5, nil}, {0, "{3,NULL,1,NULL,3}"}, {2, "{1,NULL,3}"}, {0, "{3,NULL,1,NULL,3}"}} {
				referenceRunStep(t, p.ctx, held, b.recorder, arrayAggSelect(q, "_INT4", [][]any{{tc.want}}, b.pg, tc.arg))
			}
			arrayAggAtomicity(t, p, b, run)
			arrayAggDomainSetup(run)
			domain := arrayAggDomainSelect(b.pg)
			run(referenceExec("CREATE VIEW aa_view AS "+domain.query, 0))
			run(referenceExec("ALTER TABLE aa_types RENAME TO aa_renamed", 0))
			domain.query = "SELECT i,j,a,c,d FROM aa_view"
			arrayAggBoth(run, domain)
			if b.pg {
				return
			}
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			arrayReferenceNativeRoundtrip(t, p, conn, b, domain)
			// Failed staged publication after a valid aggregate view preserves data,
			// settings and the already restored descriptor graph.
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			run(referenceExec("CREATE TABLE aa_sentinel(v TEXT)", 0))
			run(referenceExec("INSERT INTO aa_sentinel VALUES('kept')", 1))
			referenceRunStep(t, p.ctx, conn, b.recorder, referenceExec("SET application_name='array-agg-kept'", 0))
			bad := append(append([]byte(nil), dump.Bytes()...), []byte("\nCREATE VIEW aa_invalid AS SELECT missing FROM aa_renamed;\n")...)
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(bad)); err == nil {
				t.Fatal("invalid staged Restore succeeded")
			}
			run(referenceSelect("SELECT v FROM aa_sentinel", []string{"v"}, []string{"TEXT"}, [][]any{{"kept"}}))
			referenceRunStep(t, p.ctx, conn, b.recorder, referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"array-agg-kept"}}))
			run(domain)
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			rows, err := b.db.QueryContext(ctx, "SELECT array_agg(v ORDER BY id) FROM aa_scalar")
			if rows != nil {
				rows.Close()
				t.Fatal("precanceled aggregate exposed cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel=%v", err)
			}
			result, err := b.db.ExecContext(ctx, "INSERT INTO aa_target SELECT array_agg(v ORDER BY id) FROM aa_scalar")
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel insert=%v/%v", result, err)
			}
			run(arrayReferenceSelect("SELECT v FROM aa_target ORDER BY v::text NULLS LAST", []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{3,NULL,1,NULL,3}"}, {"{3,NULL,1,NULL,3}"}, {"{99}"}, {nil}, {nil}}, false))
		})
	}
}

package reference_test

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
)

func partialIndexDump(t *testing.T, p *referencePair) string {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	return dump.String()
}

// One shared SQL continuation follows replacement in both modes. Native emits
// the stream; PostgreSQL replays that actual stream, never a simulated Restore.
func partialIndexReplacement(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep), stream *string, table, predicate string, ids []int64) {
	t.Helper()
	if b.pg {
		if *stream == "" {
			t.Fatal("missing actual native Dump")
		}
		run(referenceExec("DROP SCHEMA public CASCADE", 0))
		run(referenceExec("CREATE SCHEMA public", 0))
		if _, err := b.db.ExecContext(p.ctx, *stream); err != nil {
			t.Fatal("real PostgreSQL replay", err)
		}
		return
	}
	*stream = partialIndexDump(t, p)
	if !strings.Contains(*stream, " WHERE ") || !strings.Contains(*stream, `"`+table+`"`) || !strings.Contains(*stream, `"`+predicate+`"`) {
		t.Fatal("missing current predicate definition", *stream)
	}
	foreign := newReferenceFixture(t, false, true)
	// Supported TEMP sequences shift retained destination identities before
	// replay, exercising the existing collision mapper without TEMP-table DDL.
	foreignConn, err := foreign.memory.Conn(foreign.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer foreignConn.Close()
	for _, name := range []string{"partial_retained_one", "partial_retained_two"} {
		referenceRunStep(t, foreign.ctx, foreignConn, foreign.memoryRecord, referenceExec("CREATE TEMP SEQUENCE "+name+" CACHE 10", 0))
		referenceRunStep(t, foreign.ctx, foreignConn, foreign.memoryRecord, referenceSelect("SELECT nextval('pg_temp."+name+"') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
	}
	if err := stuntgres.Restore(foreign.ctx, foreign.native, strings.NewReader(*stream)); err != nil {
		t.Fatal("foreign Restore", err)
	}
	for _, name := range []string{"partial_retained_one", "partial_retained_two"} {
		referenceRunStep(t, foreign.ctx, foreignConn, foreign.memoryRecord, referenceSelect("SELECT nextval('pg_temp."+name+"') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(2)}}))
	}
	query := partialIndexIDs("SELECT id FROM public."+table+" WHERE "+predicate+" ORDER BY id", ids...)
	referenceRunStep(t, foreign.ctx, foreign.memory, foreign.memoryRecord, query)
	var redump bytes.Buffer
	if err := stuntgres.Dump(foreign.ctx, foreign.native, &redump); err != nil || redump.String() != *stream {
		t.Fatal("foreign re-Dump", err, redump.String())
	}
	referenceRunStep(t, foreign.ctx, foreign.memory, foreign.memoryRecord, referenceExec("INSERT INTO public."+table+" VALUES(99,2,TRUE)", 1))
	more := append(append([]int64(nil), ids...), 99)
	referenceRunStep(t, foreign.ctx, foreign.memory, foreign.memoryRecord, partialIndexIDs("SELECT id FROM public."+table+" WHERE "+predicate+" ORDER BY id", more...))
	conn, err := b.db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	referenceRunStep(t, p.ctx, conn, b.recorder, referenceExec("CREATE TEMP SEQUENCE "+table+" START 777 CACHE 10", 0))
	nextTemporary := func(want int64) {
		referenceRunStep(t, p.ctx, conn, b.recorder, referenceSelect("SELECT nextval('pg_temp."+table+"') AS v", []string{"v"}, []string{"INT8"}, [][]any{{want}}))
	}
	nextTemporary(777)
	retained, err := b.db.QueryContext(p.ctx, query.query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	run(referenceSelect("SELECT relname FROM pg_class WHERE relname='pi'", []string{"relname"}, []string{"NAME"}, nil))
	nextTemporary(778)
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(*stream)); err != nil {
		t.Fatal(err)
	}
	deleteReferenceConsume(t, false, retained, nil, query)
	nextTemporary(779)
	before := partialIndexDump(t, p)
	err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(*stream+"CREATE INDEX broken ON public."+table+"(id) WHERE missing;"))
	want := controlsReferenceDiagnostic("42703", `column "missing" does not exist`)
	// Restore parses the entire input; diagnostic positions count characters.
	want.Position = referencePosition(utf8.RuneCountInString(*stream+"CREATE INDEX broken ON public."+table+"(id) WHERE ") + 1)
	referenceAssertDiagnostic(t, err, &want)
	if after := partialIndexDump(t, p); after != before {
		t.Fatal("failed Restore replaced destination", after)
	}
}

func TestReferencePartialIndexControls(t *testing.T) {
	var stream string
	partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		partialIndexSeed(run)
		run(referenceExec("CREATE INDEX pi ON t(k) INCLUDE(id) WHERE active", 0))
		for _, tc := range []struct{ call, want string }{
			{"pg_get_indexdef('pi'::regclass)", "CREATE INDEX pi ON public.t USING btree (k) INCLUDE (id) WHERE active"},
			{"pg_get_indexdef('pi'::regclass,1,false)", "k"},
			{"pg_get_indexdef('pi'::regclass,2,false)", "id"},
			{"pg_get_indexdef('pi'::regclass,3,false)", ""},
		} {
			s := deparseRelationsTextStep(t, b, "SELECT "+tc.call+" AS definition", tc.want)
			run(s)
			run(referencePrepared(s))
		}
		run(referenceExec("ALTER TABLE t RENAME active TO enabled", 0))
		run(referenceExec("ALTER TABLE t RENAME TO renamed", 0))
		run(deparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('pi'::regclass) AS definition", "CREATE INDEX pi ON public.renamed USING btree (k) INCLUDE (id) WHERE enabled"))
		partialIndexReplacement(t, p, b, run, &stream, "renamed", "enabled", []int64{1, 4, 5})
		run(partialIndexIDs("SELECT id FROM public.renamed ORDER BY id", 1, 2, 3, 4, 5))
		run(referencePrepared(referenceExec("INSERT INTO public.renamed VALUES(6,60,TRUE),(7,70,FALSE)", 2)))
		run(partialIndexIDs("SELECT id FROM public.renamed WHERE enabled ORDER BY id", 1, 4, 5, 6))
	})
}

func TestReferencePartialIndexLifecycle(t *testing.T) {
	schemaReferenceCleanup(t, "partial_lifecycle")
	partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE t(extra INT,id INT,n INT,active BOOL)", 0))
		run(referenceExec("INSERT INTO t VALUES(9,1,1,TRUE),(9,2,-1,FALSE),(9,3,NULL,NULL)", 3))
		run(referenceExec("CREATE INDEX pi ON t(id) WHERE n>0", 0))
		run(referenceExec("ALTER TABLE t DROP extra", 0))
		run(referenceExec("ALTER TABLE t ALTER n TYPE BIGINT USING n::bigint", 0))
		run(referenceSelect("SELECT id,n FROM t ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(-1)}, {int64(3), nil}}))
		before := ""
		if !b.pg {
			before = partialIndexDump(t, p)
		}
		run(referenceImplicitError("ALTER TABLE t ALTER n TYPE TEXT USING n::text", referenceDiagnostic{Code: "42883", Message: "operator does not exist: text > integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "55"}))
		if !b.pg && before != partialIndexDump(t, p) {
			t.Fatal("failed predicate-only TYPE changed Dump")
		}
		run(partialIndexIDs("SELECT id FROM t WHERE n>0 ORDER BY id", 1))
		tx, err := b.db.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		trun := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, b.recorder, s) }
		trun(referenceExec("SAVEPOINT s", 0))
		trun(referenceExec("ALTER TABLE t ALTER n TYPE INT USING n::int", 0))
		trun(referenceExec("UPDATE t SET n=2 WHERE id=2", 1))
		trun(partialIndexIDs("SELECT id FROM t WHERE n>0 ORDER BY id", 1, 2))
		trun(referenceExec("ROLLBACK TO s", 0))
		trun(referenceSelect("SELECT n FROM t WHERE id=2", []string{"n"}, []string{"INT8"}, [][]any{{int64(-1)}}))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		run(referenceExec("ALTER TABLE t RENAME n TO number", 0))
		run(referenceExec("CREATE SCHEMA partial_lifecycle", 0))
		run(referenceExec("ALTER TABLE t SET SCHEMA partial_lifecycle", 0))
		run(deparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('partial_lifecycle.pi'::regclass) AS definition", "CREATE INDEX pi ON partial_lifecycle.t USING btree (id) WHERE (number > 0)"))
		run(referenceExec("ALTER TABLE partial_lifecycle.t DROP number", 0))
		run(referenceSelect("SELECT relname FROM pg_class WHERE relname='pi'", []string{"relname"}, []string{"NAME"}, nil))
		run(referenceExec("CREATE INDEX pi ON partial_lifecycle.t(id) WHERE active", 0))
		run(referenceExec("DROP TABLE partial_lifecycle.t", 0))
		run(referenceSelect("SELECT relname FROM pg_class WHERE relname='pi'", []string{"relname"}, []string{"NAME"}, nil))
	})
}

func TestReferencePartialIndexFeatureSmoke(t *testing.T) {
	var stream string
	partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE t(id INT,n INT,active BOOL)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,2,TRUE),(2,0,FALSE),(3,0,NULL),(4,2,FALSE)", 4))
		run(referencePrepared(referenceExec("CREATE INDEX pi ON t((10/n)) WHERE active", 0)))
		run(partialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 1))
		run(partialIndexIDs("SELECT id FROM t ORDER BY id", 1, 2, 3, 4))
		run(referenceExec("UPDATE t SET active=TRUE WHERE id=4", 1))
		run(referenceExec("UPDATE t SET active=FALSE WHERE id=1", 1))
		q := "SELECT id FROM t WHERE n=$1 ORDER BY id"
		stmt, err := b.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			arg int64
			ids []int64
		}{{2, []int64{1, 4}}, {0, []int64{2, 3}}, {2, []int64{1, 4}}} {
			s := partialIndexIDs(q, tc.ids...)
			s.args = []any{tc.arg}
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}, b.recorder, s)
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		run(referenceImplicitError("UPDATE t SET active=TRUE WHERE id=2", referenceDiagnostic{Code: "22012", Message: "division by zero"}))
		run(partialIndexIDs("SELECT id FROM t WHERE active ORDER BY id", 4))
		run(referenceExec("ALTER TABLE t RENAME active TO enabled", 0))
		run(deparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('pi'::regclass) AS definition", "CREATE INDEX pi ON public.t USING btree (((10 / n))) WHERE enabled"))
		partialIndexReplacement(t, p, b, run, &stream, "t", "enabled", []int64{4})
		run(referenceExec("INSERT INTO public.t VALUES(5,2,TRUE),(6,0,FALSE)", 2))
		run(partialIndexIDs("SELECT id FROM public.t WHERE enabled ORDER BY id", 4, 5))
		run(partialIndexIDs("SELECT id FROM public.t ORDER BY id", 1, 2, 3, 4, 5, 6))
	})
}

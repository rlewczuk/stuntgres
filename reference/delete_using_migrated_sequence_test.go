package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres"
)

func TestReferenceDeleteUsingMigratedSequenceBoundary(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		peer, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		state := func(name string, n int64, called bool) {
			run(referenceSelect("SELECT last_value,is_called FROM "+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, called}}))
		}
		value := func(q string, n int64) {
			var got int64
			if err := c.QueryRowContext(p.ctx, q).Scan(&got); err != nil || got != n {
				t.Fatal(q, got, err, n)
			}
		}
		unchanged := func() {
			run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}}))
		}
		for _, q := range []string{"CREATE SEQUENCE s START 31 CACHE 1", "CREATE SEQUENCE nested START 47 CACHE 1", "CREATE TABLE empty_t(id INT)", "CREATE TABLE t(id INT)"} {
			run(referenceExec(q, 0))
		}
		run(referenceExec("INSERT INTO t VALUES(1),(1),(2)", 3))
		for _, prepared := range []bool{false, true} {
			for _, target := range []string{"empty_t", "t"} {
				for _, tc := range []struct{ q, arg, code, message, context string }{
					{"DELETE FROM " + target + " WHERE setval('s',$1::text::bigint)>0", "bad", "22P02", `invalid input syntax for type bigint: "bad"`, ""},
					{"DELETE FROM " + target + " WHERE setval($1,nextval('nested'))>0", "missing", "42P01", `relation "missing" does not exist`, "unnamed portal parameter $1 = '...'"},
				} {
					want := controlsReferenceDiagnostic(tc.code, tc.message)
					want.Context = tc.context
					step := referenceErrorDiagnostic(tc.q, want, tc.arg)
					if prepared {
						step = referencePrepared(step)
					}
					run(step)
					unchanged()
					run(referenceSelect("SELECT id FROM empty_t", []string{"id"}, []string{"INT4"}, nil))
					for _, seq := range []struct {
						name  string
						start int64
					}{{"s", 31}, {"nested", 47}} {
						state(seq.name, seq.start, false)
						run(referenceErrorDiagnostic("SELECT currval('"+seq.name+"')", controlsReferenceDiagnostic("55000", `currval of sequence "`+seq.name+`" is not yet defined in this session`)))
					}
					run(referenceErrorDiagnostic("SELECT lastval()", controlsReferenceDiagnostic("55000", "lastval is not yet defined in this session")))
				}
			}
		}
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{"DELETE FROM empty_t WHERE nextval('s')>0", "DELETE FROM t WHERE false AND nextval(NULL::regclass)>0"} {
				run(referenceExec("BEGIN", 0))
				step := referenceExec(q, 0)
				if prepared {
					step = referencePrepared(step)
				}
				run(step)
				ctx, cancel := context.WithTimeout(p.ctx, 2*time.Second)
				result, err := peer.ExecContext(ctx, "ALTER SEQUENCE s RESTART 80")
				cancel()
				if err != nil {
					t.Fatal("undemanded DELETE retained a sequence lock", err)
				}
				referenceLockAffected(t, result, 0)
				run(referenceExec("COMMIT", 0))
				state("s", 80, false)
				run(referenceErrorDiagnostic("SELECT currval('s')", controlsReferenceDiagnostic("55000", `currval of sequence "s" is not yet defined in this session`)))
			}
		}
		unchanged()
		run(referenceExec("CREATE SEQUENCE decisive START 101 CACHE 1", 0))
		for i, prepared := range []bool{false, true} {
			run(referenceExec("BEGIN", 0))
			step := referenceExec("DELETE FROM t WHERE false AND nextval('decisive')>0", 0)
			if prepared {
				step = referencePrepared(step)
			}
			run(step)
			want := int64(103 + i*3)
			if b.pg {
				// PostgreSQL's decisive FALSE avoids demand; native deliberately
				// retains its documented eager logical-child boundary.
				state("decisive", 101, false)
				run(referenceExec("SAVEPOINT recovered_currval", 0))
				run(referenceErrorDiagnostic("SELECT currval('decisive')", controlsReferenceDiagnostic("55000", `currval of sequence "decisive" is not yet defined in this session`)))
				run(referenceExec("ROLLBACK TO SAVEPOINT recovered_currval", 0))
				run(referenceErrorDiagnostic("SELECT lastval()", controlsReferenceDiagnostic("55000", "lastval is not yet defined in this session")))
			} else {
				state("decisive", want, true)
				value("SELECT currval('decisive')", want)
				value("SELECT lastval()", want)
			}
			run(referenceExec("ROLLBACK", 0))
			unchanged()
			if b.pg {
				state("decisive", 101, false)
			} else {
				state("decisive", want, true)
			}
		}
		for _, prepared := range []bool{false, true} {
			run(referenceExec("CREATE SEQUENCE bounded MINVALUE 1 MAXVALUE 2 CACHE 1", 0))
			step := referenceErrorDiagnostic("DELETE FROM t WHERE nextval('bounded')>0", controlsReferenceDiagnostic("2200H", `nextval: reached maximum value of sequence "bounded" (2)`))
			if prepared {
				step = referencePrepared(step)
			}
			run(step)
			unchanged()
			state("bounded", 2, true)
			value("SELECT currval('bounded')", 2)
			value("SELECT lastval()", 2)
			run(referenceExec("DROP SEQUENCE bounded", 0))
		}
		deleteUsingMigratedHeldDependency(t, p, b, c)
		for _, prepared := range []bool{false, true} {
			step := referenceErrorDiagnostic("DELETE FROM s WHERE nextval('s')>0", controlsReferenceDiagnostic("42809", `cannot change sequence "s"`))
			if prepared {
				step = referencePrepared(step)
			}
			run(step)
			state("s", 80, false)
		}
		run(referenceExec("DELETE FROM t WHERE id=2", 1))
		run(referenceExec("CREATE SEQUENCE producer START 10 CACHE 1", 0))
		run(referenceExec("CREATE TABLE dst(id INT,n BIGINT)", 0))
		q := "INSERT INTO dst SELECT id,nextval('producer') FROM t ORDER BY id"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		state("producer", 10, false)
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}, b.recorder, referenceExec(q, 2))
		run(referenceSelect("SELECT id,n FROM dst ORDER BY n", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(11)}}))
		value("SELECT nextval('producer')", 12)
	})
}

func deleteUsingMigratedHeldDependency(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	t.Run("held_dependency", func(t *testing.T) {
		// Restore this connection's path even when a subtest assertion calls
		// Fatal, so the parent continues against its public fixture.
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := c.ExecContext(ctx, "SET search_path TO public"); err != nil {
				t.Errorf("restore held-dependency fixture search_path: %v", err)
			}
		}()
		// The PG fixture resets public only; own nonpublic fixture schemas must
		// neither collide with unrelated a/b objects nor survive repeated runs.
		rewrite := strings.NewReplacer("a.", "delete_using_a.", "b.", "delete_using_b.", "SCHEMA a", "SCHEMA delete_using_a", "SCHEMA b", "SCHEMA delete_using_b", "TO a,b", "TO delete_using_a,delete_using_b", "TO b", "TO delete_using_b").Replace
		if b.pg {
			for _, name := range []string{"delete_using_a", "delete_using_b"} {
				if _, err := c.ExecContext(p.ctx, "DROP SCHEMA IF EXISTS "+name+" CASCADE"); err != nil {
					t.Fatal(err)
				}
				defer c.ExecContext(p.ctx, "DROP SCHEMA IF EXISTS "+name+" CASCADE")
			}
		}
		baseRun := updateFromRun(t, p, b, c)
		run := func(step referenceStep) { step.query = rewrite(step.query); baseRun(step) }
		for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.x START 10 CACHE 1", "CREATE SEQUENCE b.y START 20 CACHE 1", "CREATE TABLE b.held(id INT)", "SET search_path TO a,b"} {
			run(referenceExec(q, 0))
		}
		run(referenceExec("INSERT INTO b.held VALUES(7)", 1))
		q := rewrite("DELETE FROM b.held WHERE setval('x',$1::bigint)>0 AND nextval('y')>0")
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		state := func(name string, n int64, called bool) {
			run(referenceSelect("SELECT last_value,is_called FROM "+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, called}}))
		}
		value := func(q string, n int64) {
			var got int64
			if err := c.QueryRowContext(p.ctx, rewrite(q)).Scan(&got); err != nil || got != n {
				t.Fatal(q, got, err, n)
			}
		}
		state("b.x", 10, false)
		state("b.y", 20, false)
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 1, int64(10)))
		run(referenceSelect("SELECT id FROM b.held", []string{"id"}, []string{"INT4"}, nil))
		run(referenceExec("INSERT INTO b.held VALUES(7)", 1))
		run(referenceExec("CREATE SEQUENCE a.x START 100 CACHE 1", 0))
		run(referenceExec("ALTER SEQUENCE b.y RENAME TO identity", 0))
		missing := func() {
			want := controlsReferenceDiagnostic("42P01", `relation "y" does not exist`)
			want.Position = updateFromPosition(q, "'y'")
			referenceRunStep(t, p.ctx, held, b.recorder, referenceErrorDiagnostic(q, want, int64(150)))
			run(referenceSelect("SELECT id FROM b.held", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
		// Pinned PostgreSQL observes the missing y after shadow creation and
		// rename invalidation too. Failure must resolve all dependencies before
		// drawing or applying setval, and retain the target occurrence.
		missing()
		state("a.x", 100, false)
		state("b.x", 10, true)
		state("b.identity", 20, true)
		value("SELECT currval('b.x')", 10)
		value("SELECT currval('b.identity')", 20)
		value("SELECT lastval()", 20)
		run(referenceErrorDiagnostic("SELECT currval('a.x')", controlsReferenceDiagnostic("55000", `currval of sequence "x" is not yet defined in this session`)))
		run(referenceExec("CREATE SEQUENCE a.y START 200 CACHE 1", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 1, int64(150)))
		run(referenceSelect("SELECT id FROM b.held", []string{"id"}, []string{"INT4"}, nil))
		run(referenceExec("INSERT INTO b.held VALUES(7)", 1))
		run(referenceExec("SET search_path TO b", 0))
		// A changed search_path forces PostgreSQL reanalysis and the same
		// missing-name failure as native; no target or sequence effects.
		missing()
		state("a.x", 150, true)
		state("a.y", 200, true)
		state("b.x", 10, true)
		state("b.identity", 20, true)
		value("SELECT currval('a.x')", 150)
		value("SELECT currval('a.y')", 200)
		value("SELECT lastval()", 200)
		run(referenceExec("SET search_path TO a,b", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 1, int64(151)))
		state("a.x", 151, true)
		state("a.y", 201, true)
		run(referenceExec("SET search_path TO public", 0))
	})
}

func TestReferenceDeleteUsingMigratedSequenceComposition(t *testing.T) {
	// The former nested consumer-boundary invocation is represented by the
	// independently selected MigratedSequenceBoundary entrypoint, without a
	// duplicate execution of the same permanent assertion body.
	deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		value := func(q string, n int64) {
			var got int64
			if err := c.QueryRowContext(p.ctx, q).Scan(&got); err != nil || got != n {
				t.Fatal(q, got, err, n)
			}
		}
		rows := func(values ...int64) {
			want := make([][]any, 0, len(values))
			for _, n := range values {
				want = append(want, []any{n})
			}
			run(referenceSelect("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, want))
		}
		run(referenceExec("CREATE TABLE t(v INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1),(1),(2)", 3))
		run(referenceExec("CREATE SEQUENCE s", 0))
		value("SELECT nextval('s')", 1)
		for _, commit := range []bool{false, true} {
			run(referenceExec("BEGIN", 0))
			run(referenceExec("ALTER SEQUENCE s RESTART 100", 0))
			run(referenceExec("DELETE FROM t WHERE v=1", 2))
			run(referenceExec("INSERT INTO t VALUES(3)", 1))
			rows(2, 3)
			if commit {
				run(referenceExec("COMMIT", 0))
				rows(2, 3)
				value("SELECT nextval('s')", 100)
			} else {
				run(referenceExec("ROLLBACK", 0))
				rows(1, 1, 2)
				value("SELECT nextval('s')", 2)
			}
		}
		run(referenceExec("BEGIN", 0))
		run(referenceExec("DELETE FROM t WHERE nextval('s')>0", 2))
		run(referenceExec("ROLLBACK", 0))
		rows(2, 3)
		value("SELECT currval('s')", 102)
		run(referencePrepared(referenceErrorDiagnostic("DELETE FROM s WHERE nextval('s')>0", controlsReferenceDiagnostic("42809", `cannot change sequence "s"`))))
		value("SELECT currval('s')", 102)
		if !b.pg {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
		}
		rows(2, 3)
		value("SELECT nextval('s')", 103)
	})
}

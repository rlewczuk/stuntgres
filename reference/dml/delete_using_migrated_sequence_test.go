package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDeleteUsingMigratedSequenceBoundary(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		state := func(name string, n int64, called bool) {
			run(fixture.Select("SELECT last_value,is_called FROM "+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, called}}))
		}
		value := func(q string, n int64) {
			var got int64
			if err := c.QueryRowContext(p.Ctx, q).Scan(&got); err != nil || got != n {
				t.Fatal(q, got, err, n)
			}
		}
		unchanged := func() {
			run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}}))
		}
		for _, q := range []string{"CREATE SEQUENCE s START 31 CACHE 1", "CREATE SEQUENCE nested START 47 CACHE 1", "CREATE TABLE empty_t(id INT)", "CREATE TABLE t(id INT)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO t VALUES(1),(1),(2)", 3))
		for _, prepared := range []bool{false, true} {
			for _, target := range []string{"empty_t", "t"} {
				for _, tc := range []struct{ q, arg, code, message, context string }{
					{"DELETE FROM " + target + " WHERE setval('s',$1::text::bigint)>0", "bad", "22P02", `invalid input syntax for type bigint: "bad"`, ""},
					{"DELETE FROM " + target + " WHERE setval($1,nextval('nested'))>0", "missing", "42P01", `relation "missing" does not exist`, "unnamed portal parameter $1 = '...'"},
				} {
					want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					want.Context = tc.context
					step := fixture.ErrorDiagnostic(tc.q, want, tc.arg)
					if prepared {
						step = fixture.Prepared(step)
					}
					run(step)
					unchanged()
					run(fixture.Select("SELECT id FROM empty_t", []string{"id"}, []string{"INT4"}, nil))
					for _, seq := range []struct {
						name  string
						start int64
					}{{"s", 31}, {"nested", 47}} {
						state(seq.name, seq.start, false)
						run(fixture.ErrorDiagnostic("SELECT currval('"+seq.name+"')", fixture.ControlsReferenceDiagnostic("55000", `currval of sequence "`+seq.name+`" is not yet defined in this session`)))
					}
					run(fixture.ErrorDiagnostic("SELECT lastval()", fixture.ControlsReferenceDiagnostic("55000", "lastval is not yet defined in this session")))
				}
			}
		}
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{"DELETE FROM empty_t WHERE nextval('s')>0", "DELETE FROM t WHERE false AND nextval(NULL::regclass)>0"} {
				run(fixture.Exec("BEGIN", 0))
				step := fixture.Exec(q, 0)
				if prepared {
					step = fixture.Prepared(step)
				}
				run(step)
				ctx, cancel := context.WithTimeout(p.Ctx, 2*time.Second)
				result, err := peer.ExecContext(ctx, "ALTER SEQUENCE s RESTART 80")
				cancel()
				if err != nil {
					t.Fatal("undemanded DELETE retained a sequence lock", err)
				}
				fixture.LockAffected(t, result, 0)
				run(fixture.Exec("COMMIT", 0))
				state("s", 80, false)
				run(fixture.ErrorDiagnostic("SELECT currval('s')", fixture.ControlsReferenceDiagnostic("55000", `currval of sequence "s" is not yet defined in this session`)))
			}
		}
		unchanged()
		run(fixture.Exec("CREATE SEQUENCE decisive START 101 CACHE 1", 0))
		for i, prepared := range []bool{false, true} {
			run(fixture.Exec("BEGIN", 0))
			step := fixture.Exec("DELETE FROM t WHERE false AND nextval('decisive')>0", 0)
			if prepared {
				step = fixture.Prepared(step)
			}
			run(step)
			want := int64(103 + i*3)
			if b.PG {
				// PostgreSQL's decisive FALSE avoids demand; native deliberately
				// retains its documented eager logical-child boundary.
				state("decisive", 101, false)
				run(fixture.Exec("SAVEPOINT recovered_currval", 0))
				run(fixture.ErrorDiagnostic("SELECT currval('decisive')", fixture.ControlsReferenceDiagnostic("55000", `currval of sequence "decisive" is not yet defined in this session`)))
				run(fixture.Exec("ROLLBACK TO SAVEPOINT recovered_currval", 0))
				run(fixture.ErrorDiagnostic("SELECT lastval()", fixture.ControlsReferenceDiagnostic("55000", "lastval is not yet defined in this session")))
			} else {
				state("decisive", want, true)
				value("SELECT currval('decisive')", want)
				value("SELECT lastval()", want)
			}
			run(fixture.Exec("ROLLBACK", 0))
			unchanged()
			if b.PG {
				state("decisive", 101, false)
			} else {
				state("decisive", want, true)
			}
		}
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec("CREATE SEQUENCE bounded MINVALUE 1 MAXVALUE 2 CACHE 1", 0))
			step := fixture.ErrorDiagnostic("DELETE FROM t WHERE nextval('bounded')>0", fixture.ControlsReferenceDiagnostic("2200H", `nextval: reached maximum value of sequence "bounded" (2)`))
			if prepared {
				step = fixture.Prepared(step)
			}
			run(step)
			unchanged()
			state("bounded", 2, true)
			value("SELECT currval('bounded')", 2)
			value("SELECT lastval()", 2)
			run(fixture.Exec("DROP SEQUENCE bounded", 0))
		}
		deleteUsingMigratedHeldDependency(t, p, b, c)
		for _, prepared := range []bool{false, true} {
			step := fixture.ErrorDiagnostic("DELETE FROM s WHERE nextval('s')>0", fixture.ControlsReferenceDiagnostic("42809", `cannot change sequence "s"`))
			if prepared {
				step = fixture.Prepared(step)
			}
			run(step)
			state("s", 80, false)
		}
		run(fixture.Exec("DELETE FROM t WHERE id=2", 1))
		run(fixture.Exec("CREATE SEQUENCE producer START 10 CACHE 1", 0))
		run(fixture.Exec("CREATE TABLE dst(id INT,n BIGINT)", 0))
		q := "INSERT INTO dst SELECT id,nextval('producer') FROM t ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		state("producer", 10, false)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 2))
		run(fixture.Select("SELECT id,n FROM dst ORDER BY n", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(11)}}))
		value("SELECT nextval('producer')", 12)
	})
}

func deleteUsingMigratedHeldDependency(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
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
		if b.PG {
			for _, name := range []string{"delete_using_a", "delete_using_b"} {
				if _, err := c.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS "+name+" CASCADE"); err != nil {
					t.Fatal(err)
				}
				defer c.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS "+name+" CASCADE")
			}
		}
		baseRun := updateFromRun(t, p, b, c)
		run := func(step fixture.Step) { step.Query = rewrite(step.Query); baseRun(step) }
		for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.x START 10 CACHE 1", "CREATE SEQUENCE b.y START 20 CACHE 1", "CREATE TABLE b.held(id INT)", "SET search_path TO a,b"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO b.held VALUES(7)", 1))
		q := rewrite("DELETE FROM b.held WHERE setval('x',$1::bigint)>0 AND nextval('y')>0")
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		state := func(name string, n int64, called bool) {
			run(fixture.Select("SELECT last_value,is_called FROM "+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, called}}))
		}
		value := func(q string, n int64) {
			var got int64
			if err := c.QueryRowContext(p.Ctx, rewrite(q)).Scan(&got); err != nil || got != n {
				t.Fatal(q, got, err, n)
			}
		}
		state("b.x", 10, false)
		state("b.y", 20, false)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, int64(10)))
		run(fixture.Select("SELECT id FROM b.held", []string{"id"}, []string{"INT4"}, nil))
		run(fixture.Exec("INSERT INTO b.held VALUES(7)", 1))
		run(fixture.Exec("CREATE SEQUENCE a.x START 100 CACHE 1", 0))
		run(fixture.Exec("ALTER SEQUENCE b.y RENAME TO identity", 0))
		missing := func() {
			want := fixture.ControlsReferenceDiagnostic("42P01", `relation "y" does not exist`)
			want.Position = updateFromPosition(q, "'y'")
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, want, int64(150)))
			run(fixture.Select("SELECT id FROM b.held", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
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
		run(fixture.ErrorDiagnostic("SELECT currval('a.x')", fixture.ControlsReferenceDiagnostic("55000", `currval of sequence "x" is not yet defined in this session`)))
		run(fixture.Exec("CREATE SEQUENCE a.y START 200 CACHE 1", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, int64(150)))
		run(fixture.Select("SELECT id FROM b.held", []string{"id"}, []string{"INT4"}, nil))
		run(fixture.Exec("INSERT INTO b.held VALUES(7)", 1))
		run(fixture.Exec("SET search_path TO b", 0))
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
		run(fixture.Exec("SET search_path TO a,b", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, int64(151)))
		state("a.x", 151, true)
		state("a.y", 201, true)
		run(fixture.Exec("SET search_path TO public", 0))
	})
}

func TestReferenceDeleteUsingMigratedSequenceComposition(t *testing.T) {
	// The former nested consumer-boundary invocation is represented by the
	// independently selected MigratedSequenceBoundary entrypoint, without a
	// duplicate execution of the same permanent assertion body.
	deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		value := func(q string, n int64) {
			var got int64
			if err := c.QueryRowContext(p.Ctx, q).Scan(&got); err != nil || got != n {
				t.Fatal(q, got, err, n)
			}
		}
		rows := func(values ...int64) {
			want := make([][]any, 0, len(values))
			for _, n := range values {
				want = append(want, []any{n})
			}
			run(fixture.Select("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, want))
		}
		run(fixture.Exec("CREATE TABLE t(v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1),(1),(2)", 3))
		run(fixture.Exec("CREATE SEQUENCE s", 0))
		value("SELECT nextval('s')", 1)
		for _, commit := range []bool{false, true} {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("ALTER SEQUENCE s RESTART 100", 0))
			run(fixture.Exec("DELETE FROM t WHERE v=1", 2))
			run(fixture.Exec("INSERT INTO t VALUES(3)", 1))
			rows(2, 3)
			if commit {
				run(fixture.Exec("COMMIT", 0))
				rows(2, 3)
				value("SELECT nextval('s')", 100)
			} else {
				run(fixture.Exec("ROLLBACK", 0))
				rows(1, 1, 2)
				value("SELECT nextval('s')", 2)
			}
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DELETE FROM t WHERE nextval('s')>0", 2))
		run(fixture.Exec("ROLLBACK", 0))
		rows(2, 3)
		value("SELECT currval('s')", 102)
		run(fixture.Prepared(fixture.ErrorDiagnostic("DELETE FROM s WHERE nextval('s')>0", fixture.ControlsReferenceDiagnostic("42809", `cannot change sequence "s"`))))
		value("SELECT currval('s')", 102)
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
		}
		rows(2, 3)
		value("SELECT nextval('s')", 103)
	})
}

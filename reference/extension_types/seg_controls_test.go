package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

const segControlInsert = "INSERT INTO seg_controls VALUES (1,'12.34567890123456'),(2,'1.0 .. 1.00'),(3,'<1.5 .. ~2.50'),(4,'0...'),(5,'...0'),(6,'1.0(+-)0.005'),(7,NULL)"

func segControlCheck(b fixture.Backend) fixture.Step {
	return segSelect(b, "SELECT id,v FROM seg_controls ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "12.3457"}, {int64(2), "1.0"}, {int64(3), "<1.5 .. ~2.50"}, {int64(4), "0 .."}, {int64(5), ".. 0"}, {int64(6), "0.995 .. 1.005"}, {int64(7), nil}})
}

func segNativeCycle(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
	t.Helper()
	segCatalog(t, p, b, c)
	var dump bytes.Buffer
	if _, e := c.ExecContext(p.Ctx, "BEGIN"); e != nil {
		t.Fatal(e)
	}
	if _, e := c.ExecContext(p.Ctx, "INSERT INTO seg_controls VALUES (99,'987654321')"); e != nil {
		t.Fatal(e)
	}
	e := stuntgres.Dump(p.Ctx, p.Native, &dump)
	_, rollback := c.ExecContext(p.Ctx, "ROLLBACK")
	if e != nil || rollback != nil {
		t.Fatalf("dump %v rollback %v", e, rollback)
	}
	if strings.Contains(dump.String(), "987654321") {
		t.Fatal("private row in dump")
	}
	if a, z := strings.Index(dump.String(), "CREATE EXTENSION"), strings.Index(dump.String(), "CREATE TABLE"); a < 0 || z < a {
		t.Fatal("extension ordering", dump.String())
	}
	old, e := b.DB.QueryContext(p.Ctx, check.Query)
	if e != nil {
		t.Fatal(e)
	}
	defer old.Close()
	cols, e := old.ColumnTypes()
	if e != nil {
		t.Fatal(e)
	}
	segMetadata(t, false, cols, check.Columns, []bool{false, true})
	if e = stuntgres.Reset(p.Ctx, p.Native); e != nil {
		t.Fatal(e)
	}
	if e = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); e != nil {
		t.Fatal(e)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
	queryIntConsumeRetained(t, old, check)
	segMetadata(t, false, cols, check.Columns, []bool{false, true})
	segCatalog(t, p, b, c)
	var again bytes.Buffer
	if e = stuntgres.Dump(p.Ctx, p.Native, &again); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("non-deterministic redump")
	}
	target := fixture.NewFixture(t, false, true)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE seg_spacer(v integer)", 0))
	if e = stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); e != nil {
		t.Fatal(e)
	}
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, check)
	segCatalog(t, p, fixture.Backend{Recorder: target.MemoryRecord}, target.Memory)
	for _, bad := range []string{dump.String() + "INSERT INTO seg_controls VALUES (98,'ABC');", strings.Replace(dump.String(), "stuntgres-domain-null-v1", "stuntgres-domain-null-v999", 1)} {
		if bad == dump.String() {
			t.Fatal("missing exact seg replay payload")
		}
		if e = stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)); e == nil {
			t.Fatal("bad restore accepted")
		}
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, check)
	}
	if p.Postgres != nil {
		if _, e = p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); e != nil {
			t.Fatal(e)
		}
		if _, e = p.Postgres.ExecContext(p.Ctx, dump.String()); e != nil {
			t.Fatal("actual dump SQL replay", e)
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, segSelect(pg, check.Query, check.Columns, check.Rows))
		segCatalog(t, p, pg, pg.DB)
		if _, e = p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); e != nil {
			t.Fatal(e)
		}
	}
}

func TestReferenceSegControls(t *testing.T) {
	segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE seg_controls(id integer NOT NULL,v seg)", 0))
		run(fixture.Exec("CREATE TABLE seg_control_vacant(v seg NOT NULL)", 0))
		run(fixture.Exec(segControlInsert, 7))
		check := segControlCheck(b)
		run(check)
		run(fixture.Prepared(check))
		segCatalog(t, p, b, c)
		if !b.PG {
			segNativeCycle(t, p, b, c, check)
		}
		for _, q := range []string{"DROP TABLE seg_controls", "DROP TABLE seg_control_vacant", "DROP EXTENSION seg", "CREATE EXTENSION seg", "CREATE TABLE seg_controls(id integer NOT NULL,v seg)", "CREATE TABLE seg_control_vacant(v seg NOT NULL)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec(segControlInsert, 7))
		run(segControlCheck(b))
		run(fixture.Prepared(segControlCheck(b)))
		segCatalog(t, p, b, c)
	})
}

func TestReferenceSegCancellation(t *testing.T) {
	segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE seg_cancel(v seg)", 0))
		// Pre-canceled ordinary context errors are a native adapter contract, not
		// a pq transport contract; do not interrupt and then reuse a PG borrower.
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, e := c.ExecContext(ctx, "INSERT INTO seg_cancel VALUES ($1)", "1.00")
			if !errors.Is(e, context.Canceled) {
				t.Fatalf("canceled Exec %v", e)
			}
			var state interface{ SQLState() string }
			if errors.As(e, &state) {
				t.Fatalf("ordinary context error acquired SQLSTATE %s", state.SQLState())
			}
			if _, e = c.PrepareContext(ctx, "SELECT $1::seg"); !errors.Is(e, context.Canceled) {
				t.Fatalf("prepare %v", e)
			}
			if _, e = c.QueryContext(ctx, "SELECT $1::seg", "1"); !errors.Is(e, context.Canceled) {
				t.Fatalf("query %v", e)
			}
			var buf bytes.Buffer
			for _, e := range []error{stuntgres.Dump(ctx, p.Native, &buf), stuntgres.Reset(ctx, p.Native), stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE EXTENSION seg;"))} {
				if !errors.Is(e, context.Canceled) {
					t.Fatalf("control %v", e)
				}
			}
			expired, stop := context.WithDeadline(p.Ctx, time.Unix(0, 0))
			defer stop()
			if _, e = c.ExecContext(expired, "INSERT INTO seg_cancel VALUES ($1)", "1"); !errors.Is(e, context.DeadlineExceeded) {
				t.Fatalf("expired input %v", e)
			}
			<-expired.Done()
			if !errors.Is(expired.Err(), context.DeadlineExceeded) {
				t.Fatal("deadline source", expired.Err())
			}
			cursorCtx, stopCursor := context.WithCancel(p.Ctx)
			cursor, err := c.QueryContext(cursorCtx, "SELECT '1.00'::seg AS v")
			if err != nil {
				stopCursor()
				t.Fatal(err)
			}
			cols, err := cursor.ColumnTypes()
			if err != nil {
				stopCursor()
				cursor.Close()
				t.Fatal(err)
			}
			segMetadata(t, false, cols, []string{"v"}, nil)
			stopCursor()
			if cursor.Next() || !errors.Is(cursor.Err(), context.Canceled) {
				cursor.Close()
				t.Fatal("canceled iteration", cursor.Err())
			}
			if err = cursor.Close(); err != nil {
				t.Fatal(err)
			}
			segMetadata(t, false, cols, []string{"v"}, nil)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, segSelect(b, "SELECT v FROM seg_cancel", []string{"v"}, [][]any{}))
	})
}

func TestReferenceSegFeatureSmoke(t *testing.T) {
	segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE seg_controls(id integer NOT NULL,v seg)", 0))
		run(fixture.Exec("INSERT INTO seg_controls VALUES (1,'1.0(+-)0.005'),(2,'1.5 .. ~2.5')", 2))
		run(fixture.Prepared(fixture.Exec("INSERT INTO seg_controls VALUES (3,$1),(4,$2)", 2, "0...", nil)))
		check := segSelect(b, "SELECT id,v FROM seg_controls ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "0.995 .. 1.005"}, {int64(2), "1.5 .. ~2.5"}, {int64(3), "0 .."}, {int64(4), nil}})
		check.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			cols, e := rows.ColumnTypes()
			if e != nil {
				t.Fatal(e)
			}
			segMetadata(t, b.PG, cols, check.Columns, []bool{false, true})
		}
		run(check)
		q := "SELECT v FROM seg_controls WHERE id=$1"
		stmt, e := c.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			id   int
			rows [][]any
		}{{1, [][]any{{"0.995 .. 1.005"}}}, {4, [][]any{{nil}}}, {99, [][]any{}}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, segSelect(b, q, []string{"v"}, tc.rows, tc.id))
		}
		want := fixture.ControlsReferenceDiagnostic("42601", "bad seg representation")
		want.Detail = `syntax error at or near "A"`
		want.Position = "44"
		// The quote offset is counted from this exact statement, not from the codec token.
		queryIntError(t, p, b, c, "INSERT INTO seg_controls VALUES (5,'5'),(6,'ABC')", false, false, true, want)
		run(check)
		if !b.PG {
			segNativeCycle(t, p, b, c, check)
		}
	})
}

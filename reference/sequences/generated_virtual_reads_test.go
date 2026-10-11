package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedVirtualDemand(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(1/a) VIRTUAL)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(0),(1),(2)`, 3))
		run(fixture.Select(`SELECT a FROM g ORDER BY a`, []string{"a"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(2)}}))
		run(fixture.Select(`SELECT CASE WHEN a=0 THEN 9 ELSE b END AS safe FROM g ORDER BY a`, []string{"safe"}, []string{"INT4"}, [][]any{{int64(9)}, {int64(1)}, {int64(0)}}))
		run(fixture.Select(`SELECT b FROM g WHERE a<>0 ORDER BY a`, []string{"b"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(0)}}))
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(`SELECT b FROM g WHERE a=0`, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
		}
		run(fixture.Exec(`CREATE VIEW v AS SELECT a,b FROM g WHERE a<>0`, 0))
		run(fixture.Select(`SELECT sum(b) FROM v`, []string{"sum"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.Select(`SELECT b FROM (SELECT a,b FROM g WHERE a<>0) s ORDER BY b`, []string{"b"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}}))
		// Absent NEW must not demand even an invalid old virtual value.
		run(fixture.Select(`DELETE FROM g WHERE a=0 RETURNING NEW.b`, []string{"b"}, []string{"INT4"}, [][]any{{nil}}))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(0)`, 1))
		s := fixture.ImplicitError(`DELETE FROM g WHERE a=0 RETURNING b`, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		s.SelectRows = true
		run(s)
		run(fixture.Select(`SELECT a FROM g WHERE a=0`, []string{"a"}, []string{"INT4"}, [][]any{{int64(0)}}))
		run(fixture.Exec(`UPDATE g SET a=4 WHERE a=0`, 1))
		run(fixture.Exec(`CREATE TABLE overflow(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0))
		run(fixture.Exec(`INSERT INTO overflow(a) VALUES(2147483647)`, 1))
		s = fixture.ImplicitError(`SELECT b FROM overflow`, fixture.Diagnostic{Code: "22003", Message: "integer out of range"})
		s.SelectRows = true
		run(s)
	})
}

func TestReferenceGeneratedVirtualFrames(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(1),(2)`, 2))
		run(fixture.Exec(`CREATE TABLE leftside(x INT,y INT)`, 0))
		run(fixture.Exec(`INSERT INTO leftside VALUES(10,20)`, 1))
		run(fixture.Select(`SELECT x,g.b FROM leftside CROSS JOIN g ORDER BY g.a`, []string{"x", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(2)}, {int64(10), int64(3)}}))
		run(fixture.Select(`SELECT l.b,r.b FROM g l JOIN g r ON l.b=r.a`, []string{"b", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
		run(fixture.Select(`SELECT g.b,(SELECT g.b+leftside.x FROM leftside) AS correlated FROM g ORDER BY g.a`, []string{"b", "correlated"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(12)}, {int64(3), int64(13)}}))
		q := `SELECT b FROM g WHERE a=$1`
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		check := fixture.Select(q, []string{"b"}, []string{"INT4"}, [][]any{{int64(2)}}, 1)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, check)
		held, err := b.DB.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), `SELECT a,b FROM g WHERE a=1`)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		update := fixture.Select(`UPDATE g SET a=a+5 RETURNING a,b,OLD.b,NEW.b`, []string{"a", "b", "b", "b"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(6), int64(7), int64(2), int64(7)}, {int64(7), int64(8), int64(3), int64(8)}})
		update.Bag = true
		run(update)
		fixture.RunStep(t, p.Ctx, fixture.SequenceFrozenRows{Executor: b.DB, Rows: held, Query: `SELECT a,b FROM g WHERE a=1`}, b.Recorder, fixture.Select(`SELECT a,b FROM g WHERE a=1`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
		check = fixture.Select(q, []string{"b"}, []string{"INT4"}, [][]any{{int64(7)}}, 6)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, check)
		run(fixture.Exec(`UPDATE g SET a=b WHERE b=7`, 1))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(4)`, 1))
		run(fixture.Select(`SELECT b FROM g WHERE a=4`, []string{"b"}, []string{"INT4"}, [][]any{{int64(5)}}))
		preparedUpdate := fixture.Select(`UPDATE g SET a=4 WHERE a=4 RETURNING b,OLD.b,NEW.b`, []string{"b", "b", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(5), int64(5), int64(5)}})
		preparedUpdate.Prepared = true
		run(preparedUpdate)
		preparedDelete := fixture.Select(`DELETE FROM g WHERE b=5 RETURNING OLD.b,NEW.b`, []string{"b", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(5), nil}})
		preparedDelete.Prepared = true
		run(preparedDelete)
		run(fixture.Exec(`CREATE TABLE source(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0))
		run(fixture.Exec(`INSERT INTO source(a) VALUES(7),(9)`, 2))
		merge := fixture.Exec(`MERGE INTO g USING source s ON g.b=s.b WHEN MATCHED THEN UPDATE SET a=s.b WHEN NOT MATCHED THEN INSERT(a) VALUES(s.b)`, 3)
		// lib/pq does not expose the MERGE command count; native must report it.
		// Exact rows below independently prove all three effects in either mode.
		if b.PG {
			merge.Affected = 0
		}
		run(merge)
		run(fixture.Select(`SELECT a,b FROM g ORDER BY a`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(8), int64(9)}, {int64(8), int64(9)}, {int64(10), int64(11)}}))
	})
}

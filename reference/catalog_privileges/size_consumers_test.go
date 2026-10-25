package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSizeConsumers(t *testing.T) {
	sizeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE size_input(id integer,s text,i bigint,n numeric)", 0))
		run(fixture.Exec("INSERT INTO size_input VALUES(1,'1 MB',1024,10.5000),(2,'2 kB',10752,10240),(3,'1 MB',1024,10.5000),(4,NULL,NULL,NULL)", 4))
		for _, s := range []fixture.Step{
			fixture.Select("SELECT id,pg_size_bytes(s),pg_size_pretty(i),pg_size_pretty(n) FROM size_input ORDER BY id", []string{"id", "pg_size_bytes", "pg_size_pretty", "pg_size_pretty"}, []string{"INT4", "INT8", "TEXT", "TEXT"}, [][]any{{int64(1), int64(1048576), "1024 bytes", "10.5000 bytes"}, {int64(2), int64(2048), "11 kB", "10 kB"}, {int64(3), int64(1048576), "1024 bytes", "10.5000 bytes"}, {int64(4), nil, nil, nil}}),
			fixture.Select("SELECT id FROM size_input WHERE pg_size_bytes(s)=1048576 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}),
			fixture.Select("SELECT id FROM size_input ORDER BY pg_size_bytes(s),id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}, {int64(3)}, {int64(4)}}),
			fixture.Select("SELECT pg_size_pretty(i),count(*) FROM size_input GROUP BY pg_size_pretty(i) ORDER BY pg_size_pretty(i)", []string{"pg_size_pretty", "count"}, []string{"TEXT", "INT8"}, [][]any{{"1024 bytes", int64(2)}, {"11 kB", int64(1)}, {nil, int64(1)}}),
			fixture.Select("SELECT count(pg_size_bytes(s)) FROM size_input", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}),
			fixture.Select("SELECT CASE WHEN id=2 THEN pg_size_pretty(i) ELSE pg_size_pretty(n) END AS v,COALESCE(pg_size_bytes(s),0::bigint) AS n,NULLIF(pg_size_pretty(i),'1024 bytes') AS z FROM size_input ORDER BY id", []string{"v", "n", "z"}, []string{"TEXT", "INT8", "TEXT"}, [][]any{{"10.5000 bytes", int64(1048576), nil}, {"11 kB", int64(2048), "11 kB"}, {"10.5000 bytes", int64(1048576), nil}, {nil, int64(0), nil}}),
			fixture.Select("SELECT v FROM (SELECT pg_size_pretty(i) AS v,id FROM size_input) AS d WHERE id=1", []string{"v"}, []string{"TEXT"}, [][]any{{"1024 bytes"}}),
			fixture.Select("SELECT (SELECT pg_size_bytes(s) FROM size_input WHERE id=2) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(2048)}}),
			fixture.Select("SELECT a.id,b.id FROM size_input a JOIN size_input b ON pg_size_bytes(a.s)=pg_size_bytes(b.s) WHERE a.id=1 ORDER BY b.id", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(3)}}),
			fixture.Select("SELECT CASE WHEN true THEN pg_size_bytes('1 MB') ELSE pg_size_bytes($1) END AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1048576)}}, "bad"),
		} {
			sizeBoth(t, p, b, c, s, false)
		}
		sizeBoth(t, p, b, c, fixture.Select("SELECT sum(pg_size_bytes(s)) FROM size_input", []string{"sum"}, []string{"NUMERIC"}, [][]any{{"2099200"}}), false)
		run(fixture.Exec("CREATE TABLE size_dst(id integer,v bigint,p text)", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO size_dst VALUES($1,pg_size_bytes($2),pg_size_pretty($3::numeric))", 1, int64(1), "1 MB", "10.5000")
			s.Prepared = prepared
			run(s)
		}
		sizeBoth(t, p, b, c, fixture.Select("SELECT v,p FROM size_dst ORDER BY id", []string{"v", "p"}, []string{"INT8", "TEXT"}, [][]any{{int64(1048576), "10.5000 bytes"}, {int64(1048576), "10.5000 bytes"}}), false)
		sizeBoth(t, p, b, c, fixture.Select("UPDATE size_dst SET p=pg_size_pretty(v) WHERE id=1 RETURNING v,p", []string{"v", "p"}, []string{"INT8", "TEXT"}, [][]any{{int64(1048576), "1024 kB"}, {int64(1048576), "1024 kB"}}), false)
	})
}

func sizeAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE size_atomic(id integer,v bigint,CONSTRAINT size_allowed CHECK(pg_size_pretty(v)<>'11 kB'))", 0))
	run(fixture.Exec("INSERT INTO size_atomic VALUES(1,1024)", 1))
	unchanged := fixture.Select("SELECT id,v FROM size_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1024)}})
	for _, prepared := range []bool{false, true} {
		for _, input := range []string{"1 KiB", "8192PB"} {
			d := sizeInvalid(input, "KiB")
			if input == "8192PB" {
				d = sizeDiagnostic("22003", "bigint out of range", "", "")
			}
			s := fixture.ErrorDiagnostic("INSERT INTO size_atomic VALUES(2,pg_size_bytes('1 MB')),(3,pg_size_bytes($1))", d, input)
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			run(unchanged)
		}
		d := sizeDiagnostic("23514", `new row for relation "size_atomic" violates check constraint "size_allowed"`, "", "")
		d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (3, 10752).", "public", "size_atomic", "size_allowed"
		s := fixture.ErrorDiagnostic("INSERT INTO size_atomic VALUES(2,1024),(3,pg_size_bytes($1))", d, "10752")
		s.Prepared, s.ErrorStage = prepared, "execute"
		run(s)
		run(unchanged)
		s = fixture.ErrorDiagnostic("UPDATE size_atomic SET v=pg_size_bytes($1) WHERE id=1", sizeInvalid("1 KiB", "KiB"), "1 KiB")
		s.Prepared, s.ErrorStage = prepared, "execute"
		run(s)
		run(unchanged)
	}
	tx, err := c.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO size_atomic VALUES(2,1024)", 1))
	s := fixture.ErrorDiagnostic("INSERT INTO size_atomic VALUES(3,pg_size_bytes($1))", sizeInvalid("1 KiB", "KiB"), "1 KiB")
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("SELECT v FROM size_atomic", fixture.TransactionAborted()))
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	run(unchanged)
	run(fixture.Exec("INSERT INTO size_atomic VALUES(4,pg_size_bytes('1 MB'))", 1))
	run(fixture.Select("SELECT id,v FROM size_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1024)}, {int64(4), int64(1048576)}}))
}

func TestReferenceSizeAtomicity(t *testing.T) { sizeScenario(t, sizeAtomicity) }

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceSizeConsumers(t *testing.T) {
	sizeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE size_input(id integer,s text,i bigint,n numeric)", 0))
		run(referenceExec("INSERT INTO size_input VALUES(1,'1 MB',1024,10.5000),(2,'2 kB',10752,10240),(3,'1 MB',1024,10.5000),(4,NULL,NULL,NULL)", 4))
		for _, s := range []referenceStep{
			referenceSelect("SELECT id,pg_size_bytes(s),pg_size_pretty(i),pg_size_pretty(n) FROM size_input ORDER BY id", []string{"id", "pg_size_bytes", "pg_size_pretty", "pg_size_pretty"}, []string{"INT4", "INT8", "TEXT", "TEXT"}, [][]any{{int64(1), int64(1048576), "1024 bytes", "10.5000 bytes"}, {int64(2), int64(2048), "11 kB", "10 kB"}, {int64(3), int64(1048576), "1024 bytes", "10.5000 bytes"}, {int64(4), nil, nil, nil}}),
			referenceSelect("SELECT id FROM size_input WHERE pg_size_bytes(s)=1048576 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}),
			referenceSelect("SELECT id FROM size_input ORDER BY pg_size_bytes(s),id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}, {int64(3)}, {int64(4)}}),
			referenceSelect("SELECT pg_size_pretty(i),count(*) FROM size_input GROUP BY pg_size_pretty(i) ORDER BY pg_size_pretty(i)", []string{"pg_size_pretty", "count"}, []string{"TEXT", "INT8"}, [][]any{{"1024 bytes", int64(2)}, {"11 kB", int64(1)}, {nil, int64(1)}}),
			referenceSelect("SELECT count(pg_size_bytes(s)) FROM size_input", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}),
			referenceSelect("SELECT CASE WHEN id=2 THEN pg_size_pretty(i) ELSE pg_size_pretty(n) END AS v,COALESCE(pg_size_bytes(s),0::bigint) AS n,NULLIF(pg_size_pretty(i),'1024 bytes') AS z FROM size_input ORDER BY id", []string{"v", "n", "z"}, []string{"TEXT", "INT8", "TEXT"}, [][]any{{"10.5000 bytes", int64(1048576), nil}, {"11 kB", int64(2048), "11 kB"}, {"10.5000 bytes", int64(1048576), nil}, {nil, int64(0), nil}}),
			referenceSelect("SELECT v FROM (SELECT pg_size_pretty(i) AS v,id FROM size_input) AS d WHERE id=1", []string{"v"}, []string{"TEXT"}, [][]any{{"1024 bytes"}}),
			referenceSelect("SELECT (SELECT pg_size_bytes(s) FROM size_input WHERE id=2) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(2048)}}),
			referenceSelect("SELECT a.id,b.id FROM size_input a JOIN size_input b ON pg_size_bytes(a.s)=pg_size_bytes(b.s) WHERE a.id=1 ORDER BY b.id", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(3)}}),
			referenceSelect("SELECT CASE WHEN true THEN pg_size_bytes('1 MB') ELSE pg_size_bytes($1) END AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1048576)}}, "bad"),
		} {
			sizeBoth(t, p, b, c, s, false)
		}
		sizeBoth(t, p, b, c, referenceSelect("SELECT sum(pg_size_bytes(s)) FROM size_input", []string{"sum"}, []string{"NUMERIC"}, [][]any{{"2099200"}}), false)
		run(referenceExec("CREATE TABLE size_dst(id integer,v bigint,p text)", 0))
		for _, prepared := range []bool{false, true} {
			s := referenceExec("INSERT INTO size_dst VALUES($1,pg_size_bytes($2),pg_size_pretty($3::numeric))", 1, int64(1), "1 MB", "10.5000")
			s.prepared = prepared
			run(s)
		}
		sizeBoth(t, p, b, c, referenceSelect("SELECT v,p FROM size_dst ORDER BY id", []string{"v", "p"}, []string{"INT8", "TEXT"}, [][]any{{int64(1048576), "10.5000 bytes"}, {int64(1048576), "10.5000 bytes"}}), false)
		sizeBoth(t, p, b, c, referenceSelect("UPDATE size_dst SET p=pg_size_pretty(v) WHERE id=1 RETURNING v,p", []string{"v", "p"}, []string{"INT8", "TEXT"}, [][]any{{int64(1048576), "1024 kB"}, {int64(1048576), "1024 kB"}}), false)
	})
}

func sizeAtomicity(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
	run(referenceExec("CREATE TABLE size_atomic(id integer,v bigint,CONSTRAINT size_allowed CHECK(pg_size_pretty(v)<>'11 kB'))", 0))
	run(referenceExec("INSERT INTO size_atomic VALUES(1,1024)", 1))
	unchanged := referenceSelect("SELECT id,v FROM size_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1024)}})
	for _, prepared := range []bool{false, true} {
		for _, input := range []string{"1 KiB", "8192PB"} {
			d := sizeInvalid(input, "KiB")
			if input == "8192PB" {
				d = sizeDiagnostic("22003", "bigint out of range", "", "")
			}
			s := referenceErrorDiagnostic("INSERT INTO size_atomic VALUES(2,pg_size_bytes('1 MB')),(3,pg_size_bytes($1))", d, input)
			s.prepared, s.errorStage = prepared, "execute"
			run(s)
			run(unchanged)
		}
		d := sizeDiagnostic("23514", `new row for relation "size_atomic" violates check constraint "size_allowed"`, "", "")
		d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (3, 10752).", "public", "size_atomic", "size_allowed"
		s := referenceErrorDiagnostic("INSERT INTO size_atomic VALUES(2,1024),(3,pg_size_bytes($1))", d, "10752")
		s.prepared, s.errorStage = prepared, "execute"
		run(s)
		run(unchanged)
		s = referenceErrorDiagnostic("UPDATE size_atomic SET v=pg_size_bytes($1) WHERE id=1", sizeInvalid("1 KiB", "KiB"), "1 KiB")
		s.prepared, s.errorStage = prepared, "execute"
		run(s)
		run(unchanged)
	}
	tx, err := c.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("INSERT INTO size_atomic VALUES(2,1024)", 1))
	s := referenceErrorDiagnostic("INSERT INTO size_atomic VALUES(3,pg_size_bytes($1))", sizeInvalid("1 KiB", "KiB"), "1 KiB")
	s.errorStage = "execute"
	referenceRunStep(t, p.ctx, tx, b.recorder, s)
	referenceRunStep(t, p.ctx, tx, b.recorder, referenceErrorDiagnostic("SELECT v FROM size_atomic", referenceTransactionAborted()))
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	run(unchanged)
	run(referenceExec("INSERT INTO size_atomic VALUES(4,pg_size_bytes('1 MB'))", 1))
	run(referenceSelect("SELECT id,v FROM size_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1024)}, {int64(4), int64(1048576)}}))
}

func TestReferenceSizeAtomicity(t *testing.T) { sizeScenario(t, sizeAtomicity) }

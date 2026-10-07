//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
	"time"
)

func TestReferenceViewCanonicalLabels(t *testing.T) {
	p := newReferencePair(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE t(id INT,txt TEXT)", 0), referenceExec("INSERT INTO t VALUES(7,'seven')", 1),
		referenceSelect("SELECT id AS k,txt AS label FROM t", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "seven"}}),
		referencePrepared(referenceSelect("SELECT id AS k FROM t WHERE id=$1", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}, 7)),
		referenceExec("CREATE VIEW v(k,label) AS SELECT id AS discarded,COALESCE(txt,'x') AS ignored FROM t", 0),
		referenceExec("ALTER TABLE t RENAME COLUMN id TO renamed", 0),
	} {
		p.step(t, step)
	}
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		var definition string
		if err := db.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='v'").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(p.ctx, "CREATE VIEW reconstructed AS "+definition); err != nil {
			t.Fatal(err, definition)
		}
	}
	p.step(t, referencePrepared(referenceSelect("SELECT k,label FROM reconstructed", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "seven"}})))
}

func TestReferenceViewIntegratedBetweenDefinition(t *testing.T) {
	p := newReferencePair(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE t(id INT)", 0),
		referenceExec("INSERT INTO t VALUES(2),(4)", 2),
		referenceExec("CREATE VIEW v(id,inclusive,nested,unknown_value) AS SELECT id,id BETWEEN SYMMETRIC 3 AND 1,(id BETWEEN 1 AND 3) BETWEEN TRUE AND TRUE,NULL BETWEEN (TRUE AND TRUE) AND TRUE FROM t", 0),
	} {
		p.step(t, step)
	}
	check := func(name string) {
		p.step(t, referenceSelect("SELECT id,inclusive,nested,unknown_value FROM "+name+" ORDER BY id",
			[]string{"id", "inclusive", "nested", "unknown_value"}, []string{"INT4", "BOOL", "BOOL", "BOOL"},
			[][]any{{int64(2), true, true, nil}, {int64(4), false, false, nil}}))
	}
	check("v")
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		var definition string
		if err := db.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='v'").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(p.ctx, "CREATE VIEW reconstructed AS "+definition); err != nil {
			t.Fatal(err, definition)
		}
	}
	check("reconstructed")
}

func TestReferenceViewAnalyzedLiteralPersistence(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		names, types []string
		want         [][]any
	}{
		{"date", "SELECT DATE '2000-01-02' FROM t WHERE id=1", []string{"value"}, []string{"DATE"}, [][]any{{"2000-01-02"}}},
		{"time_precision", "SELECT TIME(3) '04:05:06.1235' FROM t WHERE id=1", []string{"value"}, []string{"TIME"}, [][]any{{"04:05:06.124"}}},
		{"timestamp_precision", "SELECT TIMESTAMP(3) '2000-01-02 04:05:06.1235' FROM t WHERE id=1", []string{"value"}, []string{"TIMESTAMP"}, [][]any{{time.Date(2000, 1, 2, 4, 5, 6, 124000000, time.UTC)}}},
		{"date_infinity", "SELECT DATE 'infinity' FROM t WHERE id=1", []string{"value"}, []string{"DATE"}, [][]any{{"infinity"}}},
		{"timestamp_infinity", "SELECT TIMESTAMP 'infinity' FROM t WHERE id=1", []string{"value"}, []string{"TIMESTAMP"}, [][]any{{"infinity"}}},
		{"typed_null", "SELECT CASE WHEN id=0 THEN DATE '2000-01-01' ELSE NULL END FROM t WHERE id=1", []string{"value"}, []string{"DATE"}, [][]any{{nil}}},
		{"numeric_scale", "SELECT 1e3 FROM t WHERE id=1", []string{"value"}, []string{"NUMERIC"}, [][]any{{"1000"}}},
		{"contextual_date", "SELECT id FROM t WHERE d='2000-02-01'", []string{"value"}, []string{"INT4"}, [][]any{{int64(1)}}},
		{"repeated_context", "SELECT '2000-02-01' BETWEEN d AND ts FROM t WHERE id=1", []string{"value"}, []string{"BOOL"}, [][]any{{true}}},
		{"other_contexts", `SELECT COALESCE(n,'MiXeD'),COALESCE(u,'550e8400-e29b-41d4-a716-446655440000'),COALESCE(b,'\x61'),COALESCE(num,'1.24') FROM t WHERE id=1`, []string{"n", "u", "b", "num"}, []string{"NAME", "UUID", "BYTEA", "NUMERIC"}, [][]any{{"MiXeD", "550e8400-e29b-41d4-a716-446655440000", "a", "1.24"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferencePair(t)
			for _, backend := range []struct {
				name   string
				pg     bool
				db     *sql.DB
				record *referenceRecorder
			}{{"native", false, p.memory, p.memoryRecord}, {"postgres", true, p.postgres, p.postgresRecord}} {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
					run(referenceExec("CREATE TABLE t(id INT,d DATE,ts TIMESTAMP,n NAME,u UUID,b BYTEA,num NUMERIC(5,2))", 0))
					run(referenceExec("INSERT INTO t(id,d,ts) VALUES(1,'2000-02-01','2000-02-02'),(2,'2000-01-02','2000-01-03')", 2))
					columns := "value"
					projection := "value"
					if tc.name == "other_contexts" {
						columns, projection = "n,u,b,num", "n,u,b,num"
					}
					run(referenceExec("CREATE VIEW v("+columns+") AS "+tc.body, 0))
					want := referenceTimeRows(t, backend.pg, tc.types, tc.want)
					if backend.pg && tc.name == "date" {
						want = [][]any{{time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)}}
					}
					check := func(name string) {
						s := referenceSelect("SELECT "+projection+" FROM "+name, tc.names, tc.types, want)
						run(s)
						run(referencePrepared(s))
					}
					check("v")
					var definition string
					if err := backend.db.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='v'").Scan(&definition); err != nil {
						t.Fatal(err)
					}
					run(referenceExec("CREATE VIEW reconstructed AS "+definition, 0))
					check("reconstructed")
				})
			}
		})
	}
}

// Native relative-time replay uses an injected TransactionStart in
// TestViewFrozenRelativeLiteralNativeReplay; PostgreSQL's transaction clock is
// the independent public oracle, without sleeps or a midnight boundary gamble.
func TestReferenceViewFrozenRelativeLiteralOracle(t *testing.T) {
	p := newReferencePair(t)
	conn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(p.ctx, "BEGIN"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(p.ctx, "ROLLBACK")
	for _, query := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(1)", "CREATE VIEW v(d) AS SELECT DATE 'today' FROM t"} {
		if _, err := conn.ExecContext(p.ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	var captured time.Time
	if err := conn.QueryRowContext(p.ctx, "SELECT DATE 'today' FROM t").Scan(&captured); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(p.ctx, `SET "DateStyle"='ISO, DMY'`); err != nil {
		t.Fatal(err)
	}
	var definition string
	if err := conn.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='v'").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(p.ctx, `SET "DateStyle"='ISO, MDY'`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(p.ctx, "CREATE VIEW reconstructed AS "+definition); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"v", "reconstructed"} {
		referenceRunStep(t, p.ctx, conn, p.postgresRecord, referenceSelect("SELECT d FROM "+name, []string{"d"}, []string{"DATE"}, [][]any{{captured.Round(0).UTC()}}))
	}
}

// DateStyle is a PostgreSQL-only input environment control. Native admitted ISO
// contexts and a deterministic relative transaction clock are covered separately.
func TestReferenceViewContextualDateStyleOracle(t *testing.T) {
	p := newReferencePair(t)
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s) }
	for _, step := range []referenceStep{
		referenceExec(`SET "DateStyle"='ISO, DMY'`, 0),
		referenceExec("CREATE TABLE t(id INT,d DATE,ts TIMESTAMP)", 0),
		referenceExec("INSERT INTO t VALUES(1,'2000-02-01','2000-02-02'),(2,'2000-01-02','2000-01-03')", 2),
		referenceExec("CREATE VIEW v(id) AS SELECT id FROM t WHERE d='01/02/2000'", 0),
		referenceExec("CREATE VIEW w(matches) AS SELECT '01/02/2000' BETWEEN d AND ts FROM t WHERE id=1", 0),
		referenceExec(`SET "DateStyle"='ISO, MDY'`, 0),
		referenceSelect("SELECT id FROM v", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
		referenceSelect("SELECT matches FROM w", []string{"matches"}, []string{"BOOL"}, [][]any{{true}}),
	} {
		run(step)
	}
	for _, name := range []string{"v", "w"} {
		var definition string
		if err := p.postgres.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname=$1", name).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		run(referenceExec("CREATE VIEW "+name+"_copy AS "+definition, 0))
	}
	run(referenceSelect("SELECT id FROM v_copy", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	run(referenceSelect("SELECT matches FROM w_copy", []string{"matches"}, []string{"BOOL"}, [][]any{{true}}))
}

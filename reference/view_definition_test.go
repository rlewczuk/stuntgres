//go:build reference

package reference_test

import (
	"testing"
	"time"
)

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

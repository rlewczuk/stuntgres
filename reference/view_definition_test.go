//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
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

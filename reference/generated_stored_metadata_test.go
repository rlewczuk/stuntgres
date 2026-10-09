package reference_test

import "testing"

func TestReferenceGeneratedStoredMetadata(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
		run(referenceExec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED,n INT GENERATED ALWAYS AS(NULL) STORED)`, 0))
		referenceTableDDLMetadata(t, p.ctx, backend.db, backend.pg, false, `SELECT b,n FROM g`, []string{"b", "n"}, []string{"INT4", "INT4"}, []bool{true, true}, 0)
		var relation, definition int64
		if err := backend.db.QueryRowContext(p.ctx, `SELECT oid FROM pg_class WHERE relname='g'`).Scan(&relation); err != nil || relation <= 0 {
			t.Fatal(relation, err)
		}
		run(referenceSelect(`SELECT adnum,pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE adrelid=$1 ORDER BY adnum`, []string{"adnum", "pg_get_expr"}, []string{"INT2", "TEXT"}, [][]any{{int64(2), "(a + 1)"}, {int64(3), "NULL::integer"}}, relation))
		if err := backend.db.QueryRowContext(p.ctx, `SELECT oid FROM pg_attrdef WHERE adrelid=$1 AND adnum=2`, relation).Scan(&definition); err != nil || definition <= 0 {
			t.Fatal(definition, err)
		}
		run(referenceExec(`ALTER TABLE g RENAME COLUMN a TO "a.dot"`, 0))
		s := referenceSelect(`SELECT pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE oid=$1`, []string{"pg_get_expr"}, []string{"TEXT"}, [][]any{{`("a.dot" + 1)`}})
		s.args = []any{definition}
		run(s)
	})
}

func TestReferenceGeneratedStoredTableOID(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
		run(referenceExec(`CREATE TABLE oidg(a INT,b oid GENERATED ALWAYS AS(tableoid) STORED)`, 0))
		run(referenceExec(`INSERT INTO oidg(a) VALUES(2)`, 1))
		var owner, stored int64
		if err := backend.db.QueryRowContext(p.ctx, `SELECT oid FROM pg_class WHERE relname='oidg'`).Scan(&owner); err != nil || owner <= 0 {
			t.Fatal(owner, err)
		}
		if err := backend.db.QueryRowContext(p.ctx, `SELECT b FROM oidg`).Scan(&stored); err != nil || stored != owner {
			t.Fatal(stored, owner, err)
		}
		run(referenceExec(`UPDATE oidg SET a=8`, 1))
		if err := backend.db.QueryRowContext(p.ctx, `SELECT b FROM oidg`).Scan(&stored); err != nil || stored != owner {
			t.Fatal(stored, owner, err)
		}
	})
}

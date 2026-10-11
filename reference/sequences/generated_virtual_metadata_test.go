package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedVirtualMetadata(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL,n INT GENERATED ALWAYS AS(NULL) VIRTUAL,unused INT)`, 0))
		fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, false, `SELECT b,n FROM g`, []string{"b", "n"}, []string{"INT4", "INT4"}, []bool{true, true}, 0)
		var relation, definition int64
		if err := backend.DB.QueryRowContext(p.Ctx, `SELECT oid FROM pg_class WHERE relname='g'`).Scan(&relation); err != nil || relation <= 0 {
			t.Fatal(relation, err)
		}
		run(fixture.Select(`SELECT adnum,pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE adrelid=$1 ORDER BY adnum`, []string{"adnum", "pg_get_expr"}, []string{"INT2", "TEXT"}, [][]any{{int64(2), "(a + 1)"}, {int64(3), "NULL::integer"}}, relation))
		if err := backend.DB.QueryRowContext(p.Ctx, `SELECT oid FROM pg_attrdef WHERE adrelid=$1 AND adnum=2`, relation).Scan(&definition); err != nil || definition <= 0 {
			t.Fatal(definition, err)
		}
		run(fixture.Exec(`INSERT INTO g(a) VALUES(2),(NULL)`, 2))
		fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, true, `SELECT b,n FROM g`, []string{"b", "n"}, []string{"INT4", "INT4"}, []bool{true, true}, 2)
		run(fixture.Exec(`ALTER TABLE g DROP COLUMN unused`, 0))
		run(fixture.Exec(`ALTER TABLE g RENAME COLUMN a TO "a.dot"`, 0))
		s := fixture.Select(`SELECT pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE oid=$1`, []string{"pg_get_expr"}, []string{"TEXT"}, [][]any{{`("a.dot" + 1)`}})
		s.Args = []any{definition}
		run(s)
		if backend.PG {
			run(fixture.Select(`SELECT attgenerated::TEXT FROM pg_attribute WHERE attrelid=$1 AND attname IN ('b','n') ORDER BY attname`, []string{"attgenerated"}, []string{"TEXT"}, [][]any{{"v"}, {"v"}}, relation))
		}
	})
}

func TestReferenceGeneratedVirtualTableOID(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE oidg(a INT,b oid GENERATED ALWAYS AS(tableoid) VIRTUAL)`, 0))
		run(fixture.Exec(`INSERT INTO oidg(a) VALUES(2)`, 1))
		var owner, stored int64
		if err := backend.DB.QueryRowContext(p.Ctx, `SELECT oid FROM pg_class WHERE relname='oidg'`).Scan(&owner); err != nil || owner <= 0 {
			t.Fatal(owner, err)
		}
		if err := backend.DB.QueryRowContext(p.Ctx, `SELECT b FROM oidg`).Scan(&stored); err != nil || stored != owner {
			t.Fatal(stored, owner, err)
		}
		run(fixture.Exec(`UPDATE oidg SET a=8`, 1))
		if err := backend.DB.QueryRowContext(p.Ctx, `SELECT b FROM oidg`).Scan(&stored); err != nil || stored != owner {
			t.Fatal(stored, owner, err)
		}
	})
}

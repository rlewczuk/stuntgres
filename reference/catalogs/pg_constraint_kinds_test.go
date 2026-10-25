package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGConstraintKinds(t *testing.T) {
	pgConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.PGClassExec(t, p, b, b.DB,
			"CREATE TABLE pc_kinds(a INT CONSTRAINT pc_nn NOT NULL,b INT,dead TEXT,d INT,CONSTRAINT pc_ck CHECK(b>a AND b<>0),CONSTRAINT pc_const CHECK(TRUE),CONSTRAINT pc_private CHECK(a>0) NO INHERIT,CONSTRAINT pc_x EXCLUDE USING btree ((a+1) WITH =))",
			"ALTER TABLE pc_kinds DROP COLUMN dead",
			"CREATE UNIQUE INDEX pc_u ON pc_kinds(d,a) INCLUDE(b)",
			"ALTER TABLE pc_kinds ADD CONSTRAINT pc_u UNIQUE USING INDEX pc_u",
			"CREATE UNIQUE INDEX pc_standalone ON pc_kinds(b)",
			"CREATE TABLE pc_kinds_other(a INT,CONSTRAINT pc_ck CHECK(a>0))",
			"CREATE TABLE pc_pk(a INT,b INT,payload TEXT)",
			"CREATE UNIQUE INDEX pc_pk_key ON pc_pk(b,a) INCLUDE(payload)",
			"ALTER TABLE pc_pk ADD CONSTRAINT pc_pk_key PRIMARY KEY USING INDEX pc_pk_key")
		q := "SELECT conname,contype,condeferrable,condeferred,conenforced,convalidated,contypid=0::oid,conparentid=0::oid,confrelid=0::oid,confupdtype,confdeltype,confmatchtype,conislocal,coninhcount,connoinherit,conperiod,conkey::text,confkey::text,conpfeqop::text,conppeqop::text,conffeqop::text,confdelsetcols::text,conexclop::text,conbin IS NOT NULL AS tree FROM pg_constraint WHERE conrelid='pc_kinds'::regclass::oid ORDER BY conname"
		cols := []string{"conname", "contype", "condeferrable", "condeferred", "conenforced", "convalidated", "?column?", "?column?", "?column?", "confupdtype", "confdeltype", "confmatchtype", "conislocal", "coninhcount", "connoinherit", "conperiod", "conkey", "confkey", "conpfeqop", "conppeqop", "conffeqop", "confdelsetcols", "conexclop", "tree"}
		types := []string{"NAME", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "CHAR", "CHAR", "CHAR", "BOOL", "INT2", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL"}
		row := func(name, kind string, key any, noinherit, tree bool, op any) []any {
			return []any{name, kind, false, false, true, true, true, true, true, " ", " ", " ", true, int64(0), noinherit, false, key, nil, nil, nil, nil, nil, op, tree}
		}
		want := [][]any{row("pc_ck", "c", "{2,1}", false, true, nil), row("pc_const", "c", nil, false, true, nil), row("pc_nn", "n", "{1}", false, false, nil), row("pc_private", "c", "{1}", true, true, nil), row("pc_u", "u", "{4,1}", true, false, nil), row("pc_x", "x", "{0}", true, false, "{96}")}
		for _, s := range []fixture.Step{fixture.Select(q, cols, types, want), fixture.Prepared(fixture.Select(q, cols, types, want))} {
			pgConstraintCheck(t, p, b, b.DB, s)
		}
		pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT c.connamespace=r.relnamespace AS namespace,c.conrelid=r.oid AS owner,c.oid<>r.oid AS distinct_owner,c.conindid<>c.oid AS distinct_index,c.conindid<>0::oid AS indexed FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid WHERE c.conrelid='pc_kinds'::regclass::oid AND c.contype IN ('u','x') ORDER BY c.conname", []string{"namespace", "owner", "distinct_owner", "distinct_index", "indexed"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true}, {true, true, true, true, true}}))
		pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT conname FROM pg_constraint WHERE conname='pc_standalone'", []string{"conname"}, []string{"NAME"}, nil))
		pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT conkey::text FROM pg_constraint WHERE conrelid='pc_pk'::regclass::oid AND contype='p'", []string{"conkey"}, []string{"TEXT"}, [][]any{{"{2,1}"}}))
		pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT relchecks FROM pg_class WHERE oid='pc_kinds'::regclass::oid", []string{"relchecks"}, []string{"INT2"}, [][]any{{int64(3)}}))
		pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT contype,conkey::text FROM pg_constraint WHERE conrelid='pc_pk'::regclass::oid ORDER BY contype,conkey::text", []string{"contype", "conkey"}, []string{"CHAR", "TEXT"}, [][]any{{"n", "{1}"}, {"n", "{2}"}, {"p", "{2,1}"}}))
		pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT conname FROM pg_constraint WHERE conname='pc_ck' AND conrelid IN ('pc_kinds'::regclass::oid,'pc_kinds_other'::regclass::oid) ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{{"pc_ck"}, {"pc_ck"}}))
		fixture.PGClassExec(t, p, b, b.DB, "CREATE TABLE pc_invalid_nn(a INT)", "ALTER TABLE pc_invalid_nn ADD CONSTRAINT pc_invalid_nn_key NOT NULL a NOT VALID")
		pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT contype,convalidated,conenforced,conkey::text FROM pg_constraint WHERE conrelid='pc_invalid_nn'::regclass::oid", []string{"contype", "convalidated", "conenforced", "conkey"}, []string{"CHAR", "BOOL", "BOOL", "TEXT"}, [][]any{{"n", false, true, "{1}"}}))
	})
}

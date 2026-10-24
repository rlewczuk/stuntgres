package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

type transactionalSchemaClosure struct{ relation, rowtype, array, defaultID int64 }

func transactionalSchemaFlywayClosure(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) transactionalSchemaClosure {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, `SELECT c.oid,c.reltype,t.typarray,d.oid FROM pg_class c JOIN pg_type t ON t.oid=c.reltype AND t.typrelid=c.oid JOIN pg_attribute a ON a.attrelid=c.oid AND a.attname='installed_on' JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum WHERE c.oid='public.flyway_schema_history'::regclass`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids transactionalSchemaClosure
	if !rows.Next() {
		t.Fatal("missing private companion closure", rows.Err())
	}
	if err := rows.Scan(&ids.relation, &ids.rowtype, &ids.array, &ids.defaultID); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("duplicate private closure", rows.Err())
	}
	if ids.relation <= 0 || ids.rowtype <= 0 || ids.array <= 0 || ids.defaultID <= 0 || ids.rowtype == ids.array {
		t.Fatal(ids)
	}
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT pg_get_expr(adbin,adrelid) AS expression FROM pg_attrdef WHERE oid=$1", []string{"expression"}, []string{"TEXT"}, [][]any{{"now()"}}, ids.defaultID))
	return ids
}

func transactionalSchemaPeerClosure(t *testing.T, p *fixture.Pair, b fixture.Backend, peer *sql.Conn, ids transactionalSchemaClosure, published bool) {
	t.Helper()
	n := int64(0)
	if published {
		n = 1
	}
	run := func(q string, want int64, args ...any) {
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{want}}, args...))
	}
	run("SELECT count(*) FROM pg_class WHERE oid=$1", n, ids.relation)
	run("SELECT count(*) FROM pg_type WHERE oid IN ($1,$2)", 2*n, ids.rowtype, ids.array)
	run("SELECT count(*) FROM pg_attribute WHERE attrelid=$1 AND attnum>0 AND NOT attisdropped", 10*n, ids.relation)
	run("SELECT count(*) FROM pg_attrdef WHERE oid=$1", n, ids.defaultID)
	run(`SELECT count(*) FROM pg_depend WHERE (classid='pg_class'::regclass AND objid=$1) OR (classid='pg_type'::regclass AND objid IN ($2,$3)) OR (classid='pg_attrdef'::regclass AND objid=$4)`, 4*n, ids.relation, ids.rowtype, ids.array, ids.defaultID)
}

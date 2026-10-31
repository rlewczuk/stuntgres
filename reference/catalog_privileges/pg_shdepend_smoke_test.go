package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGShdependFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sh_group", "sh_member", "sh_grantor", "sh_reader", "sh_owner"})
	names := []string{"sh_smoke_db"}
	fixture.DatabaseReferenceCleanup(t, &names)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		db, table, owner, reader, grantor := shSetup(t, p, b)
		q := "SELECT * FROM pg_catalog.pg_shdepend WHERE dbid=$1 AND classid=1259 AND objid=$2"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		o := shEdge(db, 1259, table, 0, owner, "o")
		fixture.PgDescriptionExecutorRows(t, p, b, held, shSelect(q, [][]any{o}, db, table))
		old, err := stmt.QueryContext(p.Ctx, db, table)
		if err != nil {
			t.Fatal(err)
		}
		shExec(t, p, b, b.DB, "GRANT SELECT ON sh_table TO sh_reader", "GRANT UPDATE(b) ON sh_table TO sh_reader")
		fixture.PgDescriptionExecutorRows(t, p, b, held, shSelect(q, [][]any{o, shEdge(db, 1259, table, 0, reader, "a"), shEdge(db, 1259, table, 2, reader, "a")}, db, table))
		shExec(t, p, b, b.DB, "REVOKE SELECT ON sh_table FROM sh_reader")
		fixture.PgDescriptionExecutorRows(t, p, b, held, shSelect(q, [][]any{o, shEdge(db, 1259, table, 2, reader, "a")}, db, table))
		shExec(t, p, b, b.DB, "ALTER TABLE sh_table OWNER TO sh_reader")
		fixture.PgDescriptionExecutorRows(t, p, b, held, shSelect(q, [][]any{shEdge(db, 1259, table, 0, reader, "o")}, db, table))
		shRetained(t, old, [][]any{o})
		stmt.Close()
		s := fixture.ImplicitError("SELECT oid FROM pg_catalog.pg_shdepend", fixture.Diagnostic{Code: "42703", Message: "column \"oid\" does not exist", Position: "8"})
		s.SelectRows = true
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		shExec(t, p, b, b.DB, "CREATE DATABASE sh_smoke_db OWNER sh_owner", "CREATE ROLE sh_group", "CREATE ROLE sh_member", "GRANT sh_group TO sh_grantor WITH ADMIN TRUE", "GRANT sh_group TO sh_member GRANTED BY sh_grantor")
		peer, _, rec := fixture.DatabaseScenarioPool(t, p, b.PG, "sh_smoke_db", "")
		bb := b
		bb.DB = peer
		bb.Recorder = rec
		shExec(t, p, bb, peer, "CREATE TABLE sh_peer(a INT)", "ALTER TABLE sh_peer OWNER TO sh_owner")
		d := shOID(t, p, b.DB, "SELECT oid FROM pg_database WHERE datname='sh_smoke_db'")
		obj := shOID(t, p, peer, "SELECT oid FROM pg_class WHERE relname='sh_peer'")
		fixture.PgDescriptionExecutorRows(t, p, b, b.DB, shSelect("SELECT * FROM pg_catalog.pg_shdepend WHERE dbid=$1 AND classid=1259 AND objid=$2", [][]any{shEdge(d, 1259, obj, 0, owner, "o")}, d, obj))
		shBag(t, p, bb, peer, 1262, d, shEdge(0, 1262, d, 0, owner, "o"))
		m := shOID(t, p, b.DB, "SELECT oid FROM pg_auth_members WHERE grantor=$1", grantor)
		shBag(t, p, bb, peer, 1261, m, shEdge(0, 1261, m, 0, grantor, "a"))
		peer.Close()
		if b.PG {
			shExec(t, p, b, b.DB, "DROP DATABASE sh_smoke_db")
		}
	}
}

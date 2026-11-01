package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoElementTypesTemporary(t *testing.T) {
	etScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer func() {
			for _, conn := range []*sql.Conn{c, peer} {
				for _, q := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP VIEW IF EXISTS pg_temp.et_temp_view", "DROP TABLE IF EXISTS pg_temp.et_temp_own", "DROP TABLE IF EXISTS pg_temp.et_temp_peer"} {
					if _, err := conn.ExecContext(p.Ctx, q); err != nil {
						t.Errorf("temporary cleanup: %v", err)
					}
				}
			}
		}()
		etExec(t, p, b, c, "CREATE ROLE et_reader", "CREATE TEMP TABLE et_temp_own(a integer[])", "CREATE TEMP VIEW et_temp_view AS SELECT a FROM et_temp_own")
		etExec(t, p, b, peer, "CREATE TEMP TABLE et_temp_peer(a integer[])")
		namespace := func(conn *sql.Conn, name string) string {
			t.Helper()
			var schema string
			if err := conn.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=$1::regclass", name).Scan(&schema); err != nil {
				t.Fatal(err)
			}
			return schema
		}
		ownSchema, peerSchema := namespace(c, "pg_temp.et_temp_own"), namespace(peer, "pg_temp.et_temp_peer")
		if ownSchema == peerSchema {
			t.Fatal("independent sessions share a temporary namespace")
		}
		where := "object_name IN ('et_temp_own','et_temp_view','et_temp_peer')"
		own := [][]any{
			etRow(database, ownSchema, "et_temp_own", "TABLE", 1, "integer", "pg_catalog", "int4"),
			etRow(database, ownSchema, "et_temp_view", "TABLE", 1, "integer", "pg_catalog", "int4"),
		}
		other := [][]any{etRow(database, peerSchema, "et_temp_peer", "TABLE", 1, "integer", "pg_catalog", "int4")}
		etCheck(t, p, b, c, etProjection(b.PG, where, own))
		etCheck(t, p, b, peer, etProjection(b.PG, where, other))
		etExec(t, p, b, c, "GRANT SELECT ON pg_temp.et_temp_own,pg_temp.et_temp_view TO et_reader")
		etExec(t, p, b, peer, "GRANT SELECT ON pg_temp.et_temp_peer TO et_reader")
		for _, mode := range []string{"ROLE", "SESSION AUTHORIZATION"} {
			etExec(t, p, b, c, "SET "+mode+" et_reader")
			etExec(t, p, b, peer, "SET "+mode+" et_reader")
			etCheck(t, p, b, c, etProjection(b.PG, where, own))
			etCheck(t, p, b, peer, etProjection(b.PG, where, other))
			etExec(t, p, b, c, "RESET "+mode)
			etExec(t, p, b, peer, "RESET "+mode)
		}
	})
}

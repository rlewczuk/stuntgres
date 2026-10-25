package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoTablesTemporary(t *testing.T) {
	itScenario(t, func(c *itCase) {
		peer, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			c.t.Fatal(err)
		}
		defer peer.Close()
		defer func() {
			for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP TABLE IF EXISTS pg_temp.it_peer CASCADE"} {
				if _, err := peer.ExecContext(c.p.Ctx, q); err != nil {
					c.t.Error(err)
				}
			}
		}()
		defer func() {
			for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP VIEW IF EXISTS pg_temp.it_temp_view", "DROP TABLE IF EXISTS pg_temp.it_same CASCADE", "DROP TABLE IF EXISTS pg_temp.it_delete CASCADE", "DROP TABLE IF EXISTS pg_temp.it_drop CASCADE"} {
				if _, err := c.conn.ExecContext(c.p.Ctx, q); err != nil {
					c.t.Error(err)
				}
			}
		}()
		c.exec("CREATE TABLE it_scope.it_same(a integer)", "CREATE TEMP TABLE it_same(a integer)", "CREATE TEMP VIEW it_temp_view AS SELECT a FROM it_same", "CREATE ROLE it_reader")
		c.run(peer, fixture.Exec("CREATE TEMP TABLE it_peer(a integer)", 0))
		namespace := func(conn *sql.Conn, name string) string {
			var schema string
			if err := conn.QueryRowContext(c.p.Ctx, "SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=$1::regclass", name).Scan(&schema); err != nil {
				c.t.Fatal(err)
			}
			return schema
		}
		ownSchema, peerSchema := namespace(c.conn, "pg_temp.it_same"), namespace(peer, "pg_temp.it_peer")
		if ownSchema == peerSchema {
			c.t.Fatal("distinct sessions share TEMP namespace")
		}
		c.exec("SET search_path=it_scope,public")
		c.run(peer, fixture.Exec("SET search_path=it_scope,public", 0))
		c.run(c.conn, fixture.Select("SELECT 'it_same'::regclass='pg_temp.it_same'::regclass AS temp_wins", []string{"temp_wins"}, []string{"BOOL"}, [][]any{{true}}))
		q := "SELECT * FROM information_schema.tables WHERE table_name IN ('it_same','it_temp_view','it_peer')"
		ownWant := [][]any{c.row("it_same", "BASE TABLE", "YES"), itRow(c.database, ownSchema, "it_same", "LOCAL TEMPORARY", "YES"), itRow(c.database, ownSchema, "it_temp_view", "LOCAL TEMPORARY", "YES")}
		peerWant := [][]any{c.row("it_same", "BASE TABLE", "YES"), itRow(c.database, peerSchema, "it_peer", "LOCAL TEMPORARY", "YES")}
		held := c.hold(q)
		peerStmt, err := peer.PrepareContext(c.p.Ctx, q)
		if err != nil {
			c.t.Fatal(err)
		}
		defer peerStmt.Close()
		peerHeld := fixture.HeldStatement{Executor: peer, Statement: peerStmt, SQL: q}
		for _, x := range []struct {
			conn *sql.Conn
			held fixture.HeldStatement
			want [][]any
		}{{c.conn, held, ownWant}, {peer, peerHeld, peerWant}} {
			s := itStep(c.b.PG, q, x.want)
			c.run(x.conn, s)
			c.run(x.conn, fixture.Prepared(s))
			c.run(x.held, s)
		}
		c.exec("GRANT SELECT ON it_scope.it_same,pg_temp.it_same,pg_temp.it_temp_view TO it_reader")
		c.run(peer, fixture.Exec("GRANT SELECT ON pg_temp.it_peer TO it_reader", 0))
		for _, mode := range []string{"ROLE", "SESSION AUTHORIZATION"} {
			c.exec("SET " + mode + " it_reader")
			c.run(held, itStep(c.b.PG, q, ownWant))
			c.exec("RESET " + mode)
			c.run(peer, fixture.Exec("SET "+mode+" it_reader", 0))
			c.run(peerHeld, itStep(c.b.PG, q, peerWant))
			c.run(peer, fixture.Exec("RESET "+mode, 0))
		}
		tq := "SELECT * FROM information_schema.tables WHERE table_name IN ('it_drop','it_delete')"
		th := c.hold(tq)
		c.exec("BEGIN", "CREATE TEMP TABLE it_drop(a integer) ON COMMIT DROP", "CREATE TEMP TABLE it_delete(a integer) ON COMMIT DELETE ROWS")
		tempWant := [][]any{itRow(c.database, ownSchema, "it_drop", "LOCAL TEMPORARY", "YES"), itRow(c.database, ownSchema, "it_delete", "LOCAL TEMPORARY", "YES")}
		c.run(th, itStep(c.b.PG, tq, tempWant))
		c.run(c.conn, fixture.Exec("INSERT INTO it_delete VALUES(1)", 1))
		c.exec("COMMIT")
		s := itStep(c.b.PG, tq, tempWant[1:])
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		c.run(th, s)
		c.run(c.conn, fixture.Select("SELECT a FROM it_delete", []string{"a"}, []string{"INT4"}, nil))
	})
}

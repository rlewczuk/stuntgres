package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoSequencesDependencies(t *testing.T) {
	isqScenario(t, func(c *isqCase) {
		c.exec("CREATE ROLE isq_actor", "CREATE TABLE isq_dep_identity(a bigint GENERATED ALWAYS AS IDENTITY)", "CREATE TABLE isq_dep_serial(a serial)", "CREATE TABLE isq_dep_table(a integer)", "CREATE VIEW isq_dep_view AS SELECT a FROM isq_dep_table", "CREATE SEQUENCE isq_dep_auto OWNED BY isq_dep_table.a", "GRANT ALL ON SEQUENCE isq_dep_identity_a_seq TO isq_actor")
		held := c.hold(c.conn, isqQuery)
		c.all(held, "isq_dep_%", c.row("public", "isq_dep_serial_a_seq", "integer", 32, "1", "1", "2147483647", "1", "NO"), c.def("public", "isq_dep_auto"))
		q := `SELECT s.relname,d.deptype FROM pg_depend d JOIN pg_class s ON s.oid=d.objid JOIN pg_namespace n ON n.oid=s.relnamespace WHERE d.classid='pg_class'::regclass AND d.refclassid='pg_class'::regclass AND s.relkind='S' AND n.nspname='public' AND s.relname LIKE 'isq_dep_%' AND d.deptype IN ('a','i') ORDER BY s.relname`
		s := fixture.Select(q, []string{"relname", "deptype"}, []string{"NAME", "CHAR"}, [][]any{{"isq_dep_auto", "a"}, {"isq_dep_identity_a_seq", "i"}, {"isq_dep_serial_a_seq", "a"}})
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		c.exec("SET ROLE isq_actor")
		c.all(held, "isq_dep_%")
	})
}

func TestReferenceInfoSequencesTemporary(t *testing.T) {
	isqScenario(t, func(c *isqCase) {
		peer := c.peer()
		c.exec("CREATE ROLE isq_actor", "CREATE SEQUENCE isq_temp_same", "CREATE TEMP SEQUENCE isq_temp_same", "CREATE TEMP SEQUENCE isq_temp_own")
		c.run(peer, fixture.Exec("CREATE TEMP SEQUENCE isq_temp_peer", 0))
		resolve := func(e fixture.Executor, name string) string {
			rows, err := e.QueryContext(c.p.Ctx, "SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relname=$1 AND n.oid=pg_my_temp_schema()", name)
			if err != nil {
				c.t.Fatal(err)
			}
			defer rows.Close()
			var namespace string
			if !rows.Next() {
				c.t.Fatal("missing own temp namespace")
			}
			if err := rows.Scan(&namespace); err != nil {
				c.t.Fatal(err)
			}
			if rows.Next() {
				c.t.Fatal("duplicate own temp namespace")
			}
			if err := rows.Err(); err != nil {
				c.t.Fatal(err)
			}
			return namespace
		}
		ownNS, peerNS := resolve(c.conn, "isq_temp_own"), resolve(peer, "isq_temp_peer")
		if ownNS == peerNS {
			c.t.Fatal("borrowed connections share physical TEMP owner")
		}
		held, peerHeld := c.hold(c.conn, isqQuery), c.hold(peer, isqQuery)
		own := [][]any{c.def("public", "isq_temp_same"), c.def(ownNS, "isq_temp_same"), c.def(ownNS, "isq_temp_own")}
		other := [][]any{c.def("public", "isq_temp_same"), c.def(peerNS, "isq_temp_peer")}
		c.all(held, "isq_temp_%", own...)
		c.check(peer, "isq_temp_%", other...)
		c.check(peerHeld, "isq_temp_%", other...)
		c.exec("SET search_path TO pg_catalog")
		c.all(held, "isq_temp_%", own...)
		c.exec("RESET search_path", "SET ROLE isq_actor")
		c.all(held, "isq_temp_%")
		c.exec("RESET ROLE", "GRANT USAGE ON SEQUENCE pg_temp.isq_temp_own TO isq_actor", "SET ROLE isq_actor")
		c.all(held, "isq_temp_%", c.def(ownNS, "isq_temp_own"))
		// Even a direct grant to the same observer cannot expose a foreign TEMP.
		c.run(peer, fixture.Exec("GRANT USAGE ON SEQUENCE pg_temp.isq_temp_peer TO isq_actor", 0))
		c.all(held, "isq_temp_%", c.def(ownNS, "isq_temp_own"))
	})
}

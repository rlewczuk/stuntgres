package reference_test

import "testing"

func TestReferencePGShdependEdges(t *testing.T) {
	p := shScenario(t)
	for _, b := range p.Backends() {
		db, table, owner, reader, grantor := shSetup(t, p, b)
		o := shEdge(db, 1259, table, 0, owner, "o")
		shBag(t, p, b, b.DB, 1259, table, o)
		shExec(t, p, b, b.DB, "GRANT SELECT,UPDATE ON sh_table TO sh_reader", "GRANT SELECT ON sh_table TO sh_reader", "GRANT UPDATE(b) ON sh_table TO sh_reader", "GRANT SELECT ON sh_table TO PUBLIC,sh_owner")
		shBag(t, p, b, b.DB, 1259, table, o, shEdge(db, 1259, table, 0, reader, "a"), shEdge(db, 1259, table, 2, reader, "a"))
		shExec(t, p, b, b.DB, "GRANT SELECT ON sh_table TO sh_grantor WITH GRANT OPTION", "GRANT USAGE ON SCHEMA public TO sh_grantor")
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		shExec(t, p, b, conn, "SET ROLE sh_grantor", "GRANT SELECT ON sh_table TO sh_reader", "RESET ROLE")
		conn.Close()
		shBag(t, p, b, b.DB, 1259, table, o, shEdge(db, 1259, table, 0, reader, "a"), shEdge(db, 1259, table, 2, reader, "a"), shEdge(db, 1259, table, 0, grantor, "a"))
		shExec(t, p, b, b.DB, "CREATE UNIQUE INDEX sh_idx ON sh_table(a)", "CREATE TYPE sh_type AS ENUM ('a')", "ALTER TYPE sh_type OWNER TO sh_owner", "CREATE FUNCTION sh_function() RETURNS integer LANGUAGE SQL AS 'SELECT 1'", "ALTER FUNCTION sh_function() OWNER TO sh_owner", "CREATE SCHEMA sh_schema AUTHORIZATION sh_owner")
		idx := shOID(t, p, b.DB, "SELECT oid FROM pg_class WHERE relname='sh_idx'")
		shBag(t, p, b, b.DB, 1259, idx)
		typ := shOID(t, p, b.DB, "SELECT oid FROM pg_type WHERE typname='sh_table' AND typnamespace='public'::regnamespace")
		shBag(t, p, b, b.DB, 1247, typ)
		array := shOID(t, p, b.DB, "SELECT typarray FROM pg_type WHERE oid=$1", typ)
		shBag(t, p, b, b.DB, 1247, array)
		typ = shOID(t, p, b.DB, "SELECT oid FROM pg_type WHERE typname='sh_type' AND typnamespace='public'::regnamespace")
		shBag(t, p, b, b.DB, 1247, typ, shEdge(db, 1247, typ, 0, owner, "o"))
		fun := shOID(t, p, b.DB, "SELECT oid FROM pg_proc WHERE proname='sh_function'")
		shBag(t, p, b, b.DB, 1255, fun, shEdge(db, 1255, fun, 0, owner, "o"))
		ns := shOID(t, p, b.DB, "SELECT oid FROM pg_namespace WHERE nspname='sh_schema'")
		shBag(t, p, b, b.DB, 2615, ns, shEdge(db, 2615, ns, 0, owner, "o"))
		shExec(t, p, b, b.DB, "DROP SCHEMA sh_schema")
	}
}

func TestReferencePGShdependInitialDefaultPolicy(t *testing.T) {
	p := shScenario(t)
	for _, b := range p.Backends() {
		db, table, owner, reader, grantor := shSetup(t, p, b)
		shExec(t, p, b, b.DB, "ALTER DEFAULT PRIVILEGES FOR ROLE sh_owner GRANT SELECT ON TABLES TO sh_reader")
		def := shOID(t, p, b.DB, "SELECT oid FROM pg_default_acl WHERE defaclrole=$1 AND defaclnamespace=0 AND defaclobjtype='r'", owner)
		shBag(t, p, b, b.DB, 826, def, shEdge(db, 826, def, 0, owner, "o"), shEdge(db, 826, def, 0, reader, "a"))
		shExec(t, p, b, b.DB, "ALTER DEFAULT PRIVILEGES FOR ROLE sh_owner REVOKE SELECT ON TABLES FROM sh_reader")
		shBag(t, p, b, b.DB, 826, def)
		shExec(t, p, b, b.DB, "CREATE POLICY sh_policy ON sh_table TO sh_reader,sh_reader USING (true)")
		policy := shOID(t, p, b.DB, "SELECT oid FROM pg_policy WHERE polname='sh_policy'")
		shBag(t, p, b, b.DB, 3256, policy, shEdge(db, 3256, policy, 0, reader, "r"), shEdge(db, 3256, policy, 0, reader, "r"))
		shExec(t, p, b, b.DB, "ALTER POLICY sh_policy ON sh_table TO sh_reader,sh_reader,PUBLIC")
		shBag(t, p, b, b.DB, 3256, policy)
		shExec(t, p, b, b.DB, "ALTER POLICY sh_policy ON sh_table TO sh_grantor")
		shBag(t, p, b, b.DB, 3256, policy, shEdge(db, 3256, policy, 0, grantor, "r"))
		shExec(t, p, b, b.DB, "DROP POLICY sh_policy ON sh_table")
		shBag(t, p, b, b.DB, 3256, policy)
		shExec(t, p, b, b.DB, "CREATE EXTENSION pageinspect", "GRANT SELECT ON sh_table TO sh_reader WITH GRANT OPTION", "GRANT UPDATE(b) ON sh_table TO sh_grantor", "ALTER EXTENSION pageinspect ADD TABLE sh_table")
		expected := [][]any{shEdge(db, 1259, table, 0, owner, "o"), shEdge(db, 1259, table, 0, reader, "a"), shEdge(db, 1259, table, 2, grantor, "a"), shEdge(db, 1259, table, 0, owner, "i"), shEdge(db, 1259, table, 0, reader, "i"), shEdge(db, 1259, table, 2, owner, "i"), shEdge(db, 1259, table, 2, grantor, "i")}
		shBag(t, p, b, b.DB, 1259, table, expected...)
		shExec(t, p, b, b.DB, "REVOKE SELECT ON sh_table FROM sh_reader", "REVOKE UPDATE(b) ON sh_table FROM sh_grantor", "ALTER TABLE sh_table OWNER TO sh_grantor")
		shBag(t, p, b, b.DB, 1259, table, shEdge(db, 1259, table, 0, grantor, "o"), expected[3], expected[4], expected[5], expected[6])
		shExec(t, p, b, b.DB, "ALTER EXTENSION pageinspect DROP TABLE sh_table")
		shBag(t, p, b, b.DB, 1259, table, shEdge(db, 1259, table, 0, grantor, "o"))
		shExec(t, p, b, b.DB, "DROP EXTENSION pageinspect")
	}
}

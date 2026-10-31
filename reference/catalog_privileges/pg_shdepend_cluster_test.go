package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGShdependCluster(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sh_owner", "sh_reader", "sh_grantor"})
	names := []string{"sh_db_one", "sh_db_two"}
	fixture.DatabaseReferenceCleanup(t, &names)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		_, _, owner, reader, _ := shSetup(t, p, b)
		shExec(t, p, b, b.DB, "CREATE DATABASE sh_db_one OWNER sh_owner", "CREATE DATABASE sh_db_two OWNER sh_owner", "GRANT CONNECT ON DATABASE sh_db_one,sh_db_two TO sh_reader")
		dUnconnected := shOID(t, p, b.DB, "SELECT oid FROM pg_database WHERE datname='sh_db_one'")
		shBag(t, p, b, b.DB, 1262, dUnconnected, shEdge(0, 1262, dUnconnected, 0, owner, "o"), shEdge(0, 1262, dUnconnected, 0, reader, "a"))
		one, _, r1 := fixture.DatabaseScenarioPool(t, p, b.PG, "sh_db_one", "")
		two, _, r2 := fixture.DatabaseScenarioPool(t, p, b.PG, "sh_db_two", "")
		b1, b2 := b, b
		b1.DB = one
		b1.Recorder = r1
		b2.DB = two
		b2.Recorder = r2
		shExec(t, p, b1, one, "CREATE TABLE sh_one(a INT)", "ALTER TABLE sh_one OWNER TO sh_owner")
		shExec(t, p, b2, two, "CREATE TABLE sh_two(a INT)", "ALTER TABLE sh_two OWNER TO sh_owner")
		d1 := shOID(t, p, b.DB, "SELECT oid FROM pg_database WHERE datname='sh_db_one'")
		d2 := shOID(t, p, b.DB, "SELECT oid FROM pg_database WHERE datname='sh_db_two'")
		t1 := shOID(t, p, one, "SELECT oid FROM pg_class WHERE relname='sh_one'")
		t2 := shOID(t, p, two, "SELECT oid FROM pg_class WHERE relname='sh_two'")
		check := func(bb fixture.Backend) {
			t.Helper()
			q := "SELECT * FROM pg_catalog.pg_shdepend WHERE (classid=1262 AND (objid=$1 OR objid=$2)) OR (classid=1259 AND ((dbid=$1 AND objid=$3) OR (dbid=$2 AND objid=$4)))"
			fixture.PgDescriptionExecutorRows(t, p, bb, bb.DB, shSelect(q, [][]any{shEdge(0, 1262, d1, 0, owner, "o"), shEdge(0, 1262, d2, 0, owner, "o"), shEdge(0, 1262, d1, 0, reader, "a"), shEdge(0, 1262, d2, 0, reader, "a"), shEdge(d1, 1259, t1, 0, owner, "o"), shEdge(d2, 1259, t2, 0, owner, "o")}, d1, d2, t1, t2))
		}
		check(b)
		check(b1)
		check(b2)
		// Database commands replace the one shared authority copy, visible from
		// both registered targets without changing their local table ownership.
		checkDatabases := func(first, second int64, acl bool) {
			t.Helper()
			expected := [][]any{shEdge(0, 1262, d1, 0, first, "o"), shEdge(0, 1262, d2, 0, second, "o")}
			if acl {
				expected = append(expected, shEdge(0, 1262, d1, 0, reader, "a"), shEdge(0, 1262, d2, 0, reader, "a"))
			}
			for _, target := range []fixture.Backend{b1, b2} {
				fixture.PgDescriptionExecutorRows(t, p, target, target.DB, shSelect("SELECT * FROM pg_catalog.pg_shdepend WHERE classid=1262 AND objid IN ($1,$2)", expected, d1, d2))
			}
		}
		newOwner := shOID(t, p, b.DB, "SELECT oid FROM pg_roles WHERE rolname='sh_grantor'")
		shExec(t, p, b, b.DB, "ALTER DATABASE sh_db_one OWNER TO sh_grantor")
		checkDatabases(newOwner, owner, true)
		shExec(t, p, b2, two, "ALTER DATABASE sh_db_two OWNER TO sh_grantor")
		checkDatabases(newOwner, newOwner, true)
		shExec(t, p, b1, one, "REVOKE CONNECT ON DATABASE sh_db_one,sh_db_two FROM sh_reader")
		checkDatabases(newOwner, newOwner, false)
		shExec(t, p, b2, two, "ALTER DATABASE sh_db_one OWNER TO sh_owner", "ALTER DATABASE sh_db_two OWNER TO sh_owner", "GRANT CONNECT ON DATABASE sh_db_one,sh_db_two TO sh_reader")
		checkDatabases(owner, owner, true)
		check(b1)
		check(b2)
		tx, err := two.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		shExec(t, p, b2, tx, "GRANT SELECT ON sh_two TO sh_reader")
		shDatabaseBag(t, p, b2, tx, d2, 1259, t2, shEdge(d2, 1259, t2, 0, owner, "o"), shEdge(d2, 1259, t2, 0, reader, "a"))
		shExec(t, p, b2, tx, "CREATE TABLE sh_private(a INT)", "ALTER TABLE sh_private OWNER TO sh_owner")
		privateTable := shOID(t, p, tx, "SELECT oid FROM pg_class WHERE relname='sh_private'")
		shDatabaseBag(t, p, b2, tx, d2, 1259, privateTable, shEdge(d2, 1259, privateTable, 0, owner, "o"))
		shDatabaseBag(t, p, b1, one, d2, 1259, privateTable)
		check(b1)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		check(b1)
		shDatabaseBag(t, p, b1, one, d2, 1259, privateTable)
		shExec(t, p, b2, two, "CREATE TABLE sh_published(a INT)", "ALTER TABLE sh_published OWNER TO sh_owner")
		published := shOID(t, p, two, "SELECT oid FROM pg_class WHERE relname='sh_published'")
		shDatabaseBag(t, p, b1, one, d2, 1259, published, shEdge(d2, 1259, published, 0, owner, "o"))
		shExec(t, p, b2, two, "GRANT SELECT ON sh_two TO sh_reader")
		shDatabaseBag(t, p, b1, one, d2, 1259, t2, shEdge(d2, 1259, t2, 0, owner, "o"), shEdge(d2, 1259, t2, 0, reader, "a"))
		conn, err := one.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		shExec(t, p, b1, conn, "SET ROLE sh_reader")
		shDatabaseBag(t, p, b1, conn, d2, 1259, t2, shEdge(d2, 1259, t2, 0, owner, "o"), shEdge(d2, 1259, t2, 0, reader, "a"))
		shExec(t, p, b1, conn, "RESET ROLE")
		conn.Close()
		one.Close()
		two.Close()
		reopened, _, reopenedRecorder := fixture.DatabaseScenarioPool(t, p, b.PG, "sh_db_two", "")
		reopenedBackend := b
		reopenedBackend.DB = reopened
		reopenedBackend.Recorder = reopenedRecorder
		shDatabaseBag(t, p, reopenedBackend, reopened, d2, 1259, t2, shEdge(d2, 1259, t2, 0, owner, "o"), shEdge(d2, 1259, t2, 0, reader, "a"))
		reopened.Close()
		// Native has no DROP DATABASE command; registered state survives pool close.
		// The real-backend phase additionally witnesses removal and releases its DBs.
		if b.PG {
			shExec(t, p, b, b.DB, "DROP DATABASE sh_db_one", "DROP DATABASE sh_db_two")
			shBag(t, p, b, b.DB, 1262, d1)
			shBag(t, p, b, b.DB, 1262, d2)
		}
	}
}

func TestReferencePGShdependMembershipPinsTablespace(t *testing.T) {
	p := shScenario(t)
	for _, b := range p.Backends() {
		db, table, _, reader, grantor := shSetup(t, p, b)
		shExec(t, p, b, b.DB, "CREATE ROLE sh_group", "CREATE ROLE sh_member", "GRANT sh_group TO sh_grantor WITH ADMIN TRUE", "GRANT sh_group TO sh_member GRANTED BY sh_grantor")
		membership := shOID(t, p, b.DB, "SELECT oid FROM pg_auth_members WHERE roleid=(SELECT oid FROM pg_roles WHERE rolname='sh_group') AND member=(SELECT oid FROM pg_roles WHERE rolname='sh_member')")
		bootstrapMembership := shOID(t, p, b.DB, "SELECT oid FROM pg_auth_members WHERE roleid=(SELECT oid FROM pg_roles WHERE rolname='sh_group') AND member=$1", grantor)
		shBag(t, p, b, b.DB, 1261, membership, shEdge(0, 1261, membership, 0, grantor, "a"))
		shBag(t, p, b, b.DB, 1261, bootstrapMembership)
		shExec(t, p, b, b.DB, "ALTER ROLE sh_grantor RENAME TO sh_renamed")
		shBag(t, p, b, b.DB, 1261, membership, shEdge(0, 1261, membership, 0, grantor, "a"))
		shExec(t, p, b, b.DB, "REVOKE sh_group FROM sh_member GRANTED BY sh_renamed", "ALTER ROLE sh_renamed RENAME TO sh_grantor")
		shBag(t, p, b, b.DB, 1261, membership)
		shExec(t, p, b, b.DB, "GRANT SELECT ON pg_catalog.pg_class TO sh_reader", "GRANT SELECT(relname) ON pg_catalog.pg_class TO sh_reader")
		sub := shOID(t, p, b.DB, "SELECT attnum FROM pg_attribute WHERE attrelid=1259 AND attname='relname'")
		shBag(t, p, b, b.DB, 1259, 1259, shEdge(db, 1259, 1259, 0, reader, "a"), shEdge(db, 1259, 1259, sub, reader, "a"))
		shExec(t, p, b, b.DB, "REVOKE SELECT ON pg_catalog.pg_class FROM sh_reader", "REVOKE SELECT(relname) ON pg_catalog.pg_class FROM sh_reader")
		shBag(t, p, b, b.DB, 1259, 1259)
		shExec(t, p, b, b.DB, "CREATE INDEX sh_pin_idx ON sh_table(a)", "CREATE TABLE sh_partition(a INT) PARTITION BY RANGE(a)")
		idx := shOID(t, p, b.DB, "SELECT oid FROM pg_class WHERE relname='sh_pin_idx'")
		partition := shOID(t, p, b.DB, "SELECT oid FROM pg_class WHERE relname='sh_partition'")
		shBag(t, p, b, b.DB, 1259, partition) // Authentic bootstrap owner is pinned.
		fixture.PgDescriptionExecutorRows(t, p, b, b.DB, shSelect("SELECT * FROM pg_catalog.pg_shdepend WHERE refclassid=1213 AND classid=1259 AND objid IN ($1,$2,$3)", [][]any{}, table, idx, partition))
	}
}

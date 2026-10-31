package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferencePGShdependControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sh_group", "sh_member", "sh_grantor", "sh_reader", "sh_owner"})
	names := []string{"sh_control_peer"}
	fixture.DatabaseReferenceCleanup(t, &names)
	p := fixture.NewScenario(t)
	var nativeDump string
	for _, b := range p.Backends() {
		db, table, owner, reader, grantor := shSetup(t, p, b)
		shExec(t, p, b, b.DB, "GRANT SELECT ON sh_table TO sh_reader", "GRANT UPDATE(b) ON sh_table TO sh_reader", "CREATE DATABASE sh_control_peer OWNER sh_owner", "CREATE ROLE sh_group", "CREATE ROLE sh_member", "GRANT sh_group TO sh_grantor WITH ADMIN TRUE", "GRANT sh_group TO sh_member GRANTED BY sh_grantor")
		peer, _, rec := fixture.DatabaseScenarioPool(t, p, b.PG, "sh_control_peer", "")
		bb := b
		bb.DB = peer
		bb.Recorder = rec
		shExec(t, p, bb, peer, "CREATE TABLE sh_peer(a INT)", "ALTER TABLE sh_peer OWNER TO sh_owner")
		shExec(t, p, bb, peer, "ALTER DEFAULT PRIVILEGES FOR ROLE sh_owner GRANT SELECT ON TABLES TO sh_reader")
		peerDefault := shOID(t, p, peer, "SELECT oid FROM pg_default_acl WHERE defaclrole=$1 AND defaclnamespace=0 AND defaclobjtype='r'", owner)
		peerDB := shOID(t, p, b.DB, "SELECT oid FROM pg_database WHERE datname='sh_control_peer'")
		peerTable := shOID(t, p, peer, "SELECT oid FROM pg_class WHERE relname='sh_peer'")
		member := shOID(t, p, b.DB, "SELECT oid FROM pg_auth_members WHERE grantor=$1", grantor)
		checkShared := func() {
			t.Helper()
			fixture.PgDescriptionExecutorRows(t, p, b, b.DB, shSelect("SELECT * FROM pg_catalog.pg_shdepend WHERE dbid=$1 AND classid=1259 AND objid=$2", [][]any{shEdge(peerDB, 1259, peerTable, 0, owner, "o")}, peerDB, peerTable))
			shBag(t, p, b, b.DB, 1261, member, shEdge(0, 1261, member, 0, grantor, "a"))
			shBag(t, p, b, b.DB, 1262, peerDB, shEdge(0, 1262, peerDB, 0, owner, "o"))
			shDatabaseBag(t, p, b, b.DB, peerDB, 826, peerDefault, shEdge(peerDB, 826, peerDefault, 0, owner, "o"), shEdge(peerDB, 826, peerDefault, 0, reader, "a"))
		}
		local := func() {
			t.Helper()
			table = shOID(t, p, b.DB, "SELECT oid FROM pg_class WHERE relname='sh_table' AND relnamespace='public'::regnamespace")
			o := shOID(t, p, b.DB, "SELECT oid FROM pg_roles WHERE rolname='sh_owner'")
			r := shOID(t, p, b.DB, "SELECT oid FROM pg_roles WHERE rolname='sh_reader'")
			fixture.PgDescriptionExecutorRows(t, p, b, b.DB, shSelect("SELECT * FROM pg_catalog.pg_shdepend WHERE dbid=$1 AND classid=1259 AND objid=$2", [][]any{shEdge(db, 1259, table, 0, o, "o"), shEdge(db, 1259, table, 0, r, "a"), shEdge(db, 1259, table, 2, r, "a")}, db, table))
		}
		local()
		checkShared()
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			nativeDump = dump.String()
			if strings.Contains(dump.String(), "INSERT INTO \"pg_catalog\".\"pg_shdepend\"") {
				t.Fatal("derived inventory serialized")
			}
			old, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_shdepend WHERE dbid=$1 AND classid=1259 AND objid=$2", db, table)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			invalid := dump.String() + "INSERT INTO public.sh_table(missing) VALUES(1);"
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(invalid))
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"missing\" of relation \"sh_table\" does not exist", Position: fixture.Position(utf8.RuneCountInString(dump.String()+"INSERT INTO public.sh_table(") + 1)})
			local()
			checkShared()
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.Native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			local()
			checkShared()
			failure := errors.New("shared dependency control I/O")
			if err := stuntgres.Dump(p.Ctx, p.Native, ipFailWriter{failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, ipFailReader{failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			local()
			checkShared()
			originalTable := table
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.PgDescriptionExecutorRows(t, p, b, b.DB, shSelect("SELECT * FROM pg_catalog.pg_shdepend WHERE dbid=$1 AND classid=1259 AND objid=$2", [][]any{}, db, originalTable))
			checkShared()
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			local()
			checkShared()
			shRetained(t, old, [][]any{shEdge(db, 1259, originalTable, 0, owner, "o"), shEdge(db, 1259, originalTable, 0, reader, "a"), shEdge(db, 1259, originalTable, 2, reader, "a")})
			driver := stuntgres.NewDriver()
			connector, err := driver.OpenConnector(p.NativeDSN)
			if err != nil {
				t.Fatal(err)
			}
			foreign := sql.OpenDB(connector)
			defer foreign.Close()
			// Another Driver with the exact opaque key has no source authority.
			foreignRows, err := foreign.QueryContext(p.Ctx, "SELECT d.* FROM pg_catalog.pg_shdepend d JOIN pg_roles r ON r.oid=d.refobjid WHERE r.rolname='sh_owner'")
			if err != nil {
				t.Fatal(err)
			}
			shRetained(t, foreignRows, [][]any{})
			// Restore remaps against destination roles; it does not create cluster roles.
			if _, err := foreign.ExecContext(p.Ctx, "CREATE ROLE sh_collision; CREATE ROLE sh_collision_two; CREATE ROLE sh_reader; CREATE ROLE sh_owner"); err != nil {
				t.Fatal(err)
			}
			foreignReader := shOID(t, p, foreign, "SELECT oid FROM pg_roles WHERE rolname='sh_reader'")
			if foreignReader == reader {
				t.Fatal("principal remapping witness did not distinguish identities")
			}
			if err := stuntgres.Restore(p.Ctx, foreign, strings.NewReader(nativeDump)); err != nil {
				t.Fatal(err)
			}
			fd := shOID(t, p, foreign, "SELECT oid FROM pg_database WHERE datname=current_database()")
			ft := shOID(t, p, foreign, "SELECT oid FROM pg_class WHERE relname='sh_table' AND relnamespace='public'::regnamespace")
			fo := shOID(t, p, foreign, "SELECT oid FROM pg_roles WHERE rolname='sh_owner'")
			fr := shOID(t, p, foreign, "SELECT oid FROM pg_roles WHERE rolname='sh_reader'")
			foreignRows, err = foreign.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_shdepend WHERE dbid=$1 AND classid=1259 AND objid=$2", fd, ft)
			if err != nil {
				t.Fatal(err)
			}
			shRetained(t, foreignRows, [][]any{shEdge(fd, 1259, ft, 0, fo, "o"), shEdge(fd, 1259, ft, 0, fr, "a"), shEdge(fd, 1259, ft, 2, fr, "a")})
			local()
			checkShared()
		}
		if b.PG && nativeDump != "" {
			shExec(t, p, b, b.DB, "DROP TABLE sh_table")
			if _, err := b.DB.ExecContext(p.Ctx, nativeDump); err != nil {
				t.Fatal("pinned replay of actual native dump", err)
			}
			table = shOID(t, p, b.DB, "SELECT oid FROM pg_class WHERE relname='sh_table' AND relnamespace='public'::regnamespace")
			// The emitted stream repairs raw ACLs with catalog UPDATE, which on PG
			// does not invoke GRANT's dependency bookkeeping. Verify every ACL item
			// before exercising command-owned dependency rebuilding.
			aclColumns := []string{"grantor", "grantee", "privilege_type", "is_grantable"}
			aclTypes := []string{"OID", "OID", "TEXT", "BOOL"}
			tableACL := [][]any{}
			for _, right := range []string{"INSERT", "SELECT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "MAINTAIN"} {
				tableACL = append(tableACL, []any{owner, owner, right, false})
			}
			tableACL = append(tableACL, []any{owner, reader, "SELECT", false})
			rawTable := fixture.Select("SELECT x.grantor,x.grantee,x.privilege_type,x.is_grantable FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) x WHERE c.oid=$1", aclColumns, aclTypes, tableACL, table)
			rawTable.Bag = true
			fixture.PgDescriptionExecutorRows(t, p, b, b.DB, rawTable)
			rawColumn := fixture.Select("SELECT x.grantor,x.grantee,x.privilege_type,x.is_grantable FROM pg_attribute a CROSS JOIN LATERAL aclexplode(a.attacl) x WHERE a.attrelid=$1 AND a.attnum=2", aclColumns, aclTypes, [][]any{{owner, reader, "UPDATE", false}}, table)
			rawColumn.Bag = true
			fixture.PgDescriptionExecutorRows(t, p, b, b.DB, rawColumn)
			shDatabaseBag(t, p, b, b.DB, db, 1259, table, shEdge(db, 1259, table, 0, owner, "o"))
			checkShared()
			shExec(t, p, b, b.DB, "REVOKE SELECT ON sh_table FROM sh_reader", "REVOKE UPDATE(b) ON sh_table FROM sh_reader", "GRANT SELECT ON sh_table TO sh_reader", "GRANT UPDATE(b) ON sh_table TO sh_reader")
			local()
			checkShared()
		}
		peer.Close()
		if b.PG {
			shExec(t, p, b, b.DB, "DROP DATABASE sh_control_peer")
		}
	}
}

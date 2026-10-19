package reference_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

type pgDefaultACLEntry struct {
	oid          int64
	name, rights string
}

func pgDefaultACLText(t *testing.T, creator string, entries ...pgDefaultACLEntry) string {
	t.Helper()
	sort.Slice(entries, func(i, j int) bool { return entries[i].oid < entries[j].oid })
	items := make([]string, len(entries))
	aclName := func(name string) string {
		if name == "" {
			return ""
		}
		for _, c := range name {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
				return pq.QuoteIdentifier(name)
			}
		}
		return name
	}
	for i, e := range entries {
		item := aclName(e.name) + "=" + e.rights + "/" + aclName(creator)
		if strings.ContainsAny(item, " ,{}\"\\\t\n\r") {
			item = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(item) + `"`
		}
		items[i] = item
	}
	return "{" + strings.Join(items, ",") + "}"
}

func TestReferencePGDefaultACLEntries(t *testing.T) {
	pgDefaultACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		const creator, reader, second, quoted = "dacl18_entries_creator", "dacl18_z_reader", "dacl18_a_reader", "dacl18 quoted"
		pgDefaultACLRoles(t, p, b, creator, reader, second, quoted)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		creatorID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_roles WHERE rolname=$1", creator)
		readerID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_roles WHERE rolname=$1", reader)
		secondID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_roles WHERE rolname=$1", second)
		quotedID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_roles WHERE rolname=$1", quoted)
		const schema = "dacl18_entries_schema"
		run(fixture.Exec("CREATE SCHEMA "+schema, 0))
		defer func() {
			if _, err := b.DB.ExecContext(p.Ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		schemaID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_namespace WHERE nspname=$1", schema)
		prefix := "ALTER DEFAULT PRIVILEGES FOR ROLE " + creator + " "
		query := "SELECT * FROM pg_default_acl WHERE defaclrole=$1 AND defaclnamespace=$2 AND defaclobjtype=$3"
		ids := map[int64]bool{}
		for _, tc := range []struct{ class, code, owner, public, privilege, reader string }{
			{"TABLES", "r", "arwdDxtm", "", "SELECT", "r"},
			{"SEQUENCES", "S", "rwU", "", "USAGE", "U"},
			{"FUNCTIONS", "f", "X", "X", "EXECUTE", "X"},
			{"TYPES", "T", "U", "U", "USAGE", "U"},
			{"SCHEMAS", "n", "UC", "", "USAGE", "U"},
			{"LARGE OBJECTS", "L", "rw", "", "SELECT", "r"},
		} {
			grant := prefix + "GRANT " + tc.privilege + " ON " + tc.class + " TO " + reader
			run(fixture.Exec(grant, 0))
			id := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_default_acl WHERE defaclrole=$1 AND defaclnamespace=0 AND defaclobjtype=$2", creatorID, tc.code)
			if ids[id] {
				t.Fatal("distinct default keys reused identity", id)
			}
			ids[id] = true
			entries := []pgDefaultACLEntry{{creatorID, creator, tc.owner}, {readerID, reader, tc.reader}}
			if tc.public != "" {
				entries = append(entries, pgDefaultACLEntry{0, "", tc.public})
			}
			expected := [][]any{{id, creatorID, int64(0), tc.code, pgDefaultACLText(t, creator, entries...)}}
			pgDefaultACLRead(t, p, b, query, expected, creatorID, int64(0), tc.code)
			run(fixture.Exec(grant, 0))
			pgDefaultACLRead(t, p, b, query, expected, creatorID, int64(0), tc.code)
		}
		// Grant in reverse numeric order. Role names deliberately sort differently.
		for _, name := range []string{quoted, second, reader} {
			run(fixture.Exec(prefix+"GRANT SELECT ON TABLES TO "+pq.QuoteIdentifier(name)+" WITH GRANT OPTION", 0))
		}
		run(fixture.Exec(prefix+"GRANT SELECT ON TABLES TO PUBLIC", 0))
		tableID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_default_acl WHERE defaclrole=$1 AND defaclnamespace=0 AND defaclobjtype='r'", creatorID)
		tableACL := pgDefaultACLText(t, creator, pgDefaultACLEntry{0, "", "r"}, pgDefaultACLEntry{creatorID, creator, "arwdDxtm"}, pgDefaultACLEntry{readerID, reader, "r*"}, pgDefaultACLEntry{secondID, second, "r*"}, pgDefaultACLEntry{quotedID, quoted, "r*"})
		pgDefaultACLRead(t, p, b, query, [][]any{{tableID, creatorID, int64(0), "r", tableACL}}, creatorID, int64(0), "r")
		run(fixture.Exec(prefix+"IN SCHEMA "+schema+","+schema+" GRANT SELECT ON TABLES TO "+reader+" WITH GRANT OPTION", 0))
		scopedID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_default_acl WHERE defaclrole=$1 AND defaclnamespace=$2 AND defaclobjtype='r'", creatorID, schemaID)
		if ids[scopedID] {
			t.Fatal("scoped/global identity collision")
		}
		pgDefaultACLRead(t, p, b, query, [][]any{{scopedID, creatorID, schemaID, "r", pgDefaultACLText(t, creator, pgDefaultACLEntry{readerID, reader, "r*"})}}, creatorID, schemaID, "r")
		explode := fixture.Select("SELECT x.grantee,x.grantor,x.privilege_type,x.is_grantable FROM pg_default_acl d CROSS JOIN LATERAL aclexplode(d.defaclacl) x WHERE d.oid=$1", []string{"grantee", "grantor", "privilege_type", "is_grantable"}, []string{"OID", "OID", "TEXT", "BOOL"}, [][]any{{readerID, creatorID, "SELECT", true}}, scopedID)
		pgDatabaseCheck(t, p, b, b.DB, explode)
		var ownerPrivileges [][]any
		for _, privilege := range []string{"DELETE", "INSERT", "MAINTAIN", "REFERENCES", "SELECT", "TRIGGER", "TRUNCATE", "UPDATE"} {
			ownerPrivileges = append(ownerPrivileges, []any{creatorID, creatorID, privilege, false})
		}
		ownerExplode := fixture.Select("SELECT x.grantee,x.grantor,x.privilege_type,x.is_grantable FROM pg_default_acl d CROSS JOIN LATERAL aclexplode(d.defaclacl) x WHERE d.oid=$1 AND x.grantee=$2 ORDER BY x.privilege_type", []string{"grantee", "grantor", "privilege_type", "is_grantable"}, []string{"OID", "OID", "TEXT", "BOOL"}, ownerPrivileges, tableID, creatorID)
		pgDatabaseCheck(t, p, b, b.DB, ownerExplode)
		pgDatabaseCheck(t, p, b, b.DB, fixture.Prepared(ownerExplode))
		run(fixture.Exec(prefix+"REVOKE ALL ON ROUTINES FROM "+creator+",PUBLIC,"+reader, 0))
		routineID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_default_acl WHERE defaclrole=$1 AND defaclobjtype='f'", creatorID)
		pgDefaultACLRead(t, p, b, query, [][]any{{routineID, creatorID, int64(0), "f", "{}"}}, creatorID, int64(0), "f")
		pgDatabaseCheck(t, p, b, b.DB, fixture.Select("SELECT defaclacl IS NOT NULL AS nonnull FROM pg_default_acl WHERE oid=$1", []string{"nonnull"}, []string{"BOOL"}, [][]any{{true}}, routineID))
		run(fixture.Exec(prefix+"GRANT EXECUTE ON FUNCTIONS TO "+creator+",PUBLIC", 0))
		pgDefaultACLRead(t, p, b, query, nil, creatorID, int64(0), "f")
		run(fixture.Exec(prefix+"IN SCHEMA "+schema+" REVOKE ALL ON TABLES FROM "+reader, 0))
		pgDefaultACLRead(t, p, b, query, nil, creatorID, schemaID, "r")
		// Two creators and two schemas are a four-key product, even with repeated
		// operands. No additional utility behavior is inferred from catalog reads.
		const otherSchema = "dacl18_entries_other"
		run(fixture.Exec("CREATE SCHEMA "+otherSchema, 0))
		defer func() {
			if _, err := b.DB.ExecContext(p.Ctx, "DROP SCHEMA "+otherSchema+" CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		otherSchemaID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_namespace WHERE nspname=$1", otherSchema)
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE "+creator+","+second+","+creator+" IN SCHEMA "+schema+","+otherSchema+","+schema+" GRANT USAGE ON SEQUENCES TO "+reader, 0))
		productIDs := map[int64]bool{}
		for _, owner := range []struct {
			id   int64
			name string
		}{{creatorID, creator}, {secondID, second}} {
			for _, namespace := range []int64{schemaID, otherSchemaID} {
				id := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_default_acl WHERE defaclrole=$1 AND defaclnamespace=$2 AND defaclobjtype='S'", owner.id, namespace)
				if ids[id] || productIDs[id] {
					t.Fatal("duplicate policy identity", id)
				}
				productIDs[id] = true
				pgDefaultACLRead(t, p, b, query, [][]any{{id, owner.id, namespace, "S", pgDefaultACLText(t, owner.name, pgDefaultACLEntry{readerID, reader, "U"})}}, owner.id, namespace, "S")
			}
		}
	})
}

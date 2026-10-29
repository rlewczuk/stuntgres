package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var ipColumns = []string{"objoid", "classoid", "objsubid", "privtype", "initprivs"}
var ipTypes = []string{"OID", "OID", "INT4", "CHAR", "_ACLITEM"}
var ipTableRights = []string{"INSERT", "SELECT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "MAINTAIN"}

func ipScenario(t *testing.T, f func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"ip_reader", "ip_other", "ip_owner", "ip_renamed", "ip_target"})
	fixture.SchemaReferenceCleanup(t, "ip_hidden", "ip_moved")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { f(t, p, b) })
	}
}

func ipExec(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, commands ...string) {
	t.Helper()
	for _, q := range commands {
		fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.Exec(q, 0))
	}
}

func ipOID(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, args ...any) int64 {
	t.Helper()
	return pgDefaultACLOID(t, p, b, q, args...)
}

func ipOwner(t *testing.T, p *fixture.Pair, b fixture.Backend) int64 {
	t.Helper()
	return ipOID(t, p, b, "SELECT relowner FROM pg_class WHERE oid=1259")
}

func ipRole(t *testing.T, p *fixture.Pair, b fixture.Backend, name string) int64 {
	t.Helper()
	return ipOID(t, p, b, "SELECT oid FROM pg_roles WHERE rolname=$1", name)
}

func ipCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, q string, columns, types []string, want [][]any, args ...any) {
	t.Helper()
	s := fixture.Select(q, columns, types, want, args...)
	s.Bag = true
	fixture.PGDatabaseCheck(t, p, b, db, s)
	fixture.PGDatabaseCheck(t, p, b, db, fixture.Prepared(s))
}

// Expected grants are prescribed independently, never harvested from the current
// ACL or the catalog under test. Bags deliberately impose no array entry order.
type ipGrant struct {
	grantee, grantor int64
	rights           []string
	option           bool
}

func ipACL(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, class, oid, sub int64, kind string, grants ...ipGrant) {
	t.Helper()
	ipCheck(t, p, b, db, "SELECT objoid,classoid,objsubid,privtype FROM pg_init_privs WHERE classoid=$1 AND objoid=$2 AND objsubid=$3", ipColumns[:4], ipTypes[:4], [][]any{{oid, class, sub, kind}}, class, oid, sub)
	var want [][]any
	for _, g := range grants {
		for _, right := range g.rights {
			want = append(want, []any{g.grantee, g.grantor, right, g.option})
		}
	}
	ipCheck(t, p, b, db, "SELECT x.grantee,x.grantor,x.privilege_type,x.is_grantable FROM pg_init_privs i CROSS JOIN LATERAL aclexplode(i.initprivs) x WHERE i.classoid=$1 AND i.objoid=$2 AND i.objsubid=$3", []string{"grantee", "grantor", "privilege_type", "is_grantable"}, []string{"OID", "OID", "TEXT", "BOOL"}, want, class, oid, sub)
}

func ipAbsent(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, class, oid int64) {
	t.Helper()
	ipCheck(t, p, b, db, "SELECT objoid,classoid,objsubid,privtype FROM pg_init_privs WHERE classoid=$1 AND objoid=$2", ipColumns[:4], ipTypes[:4], nil, class, oid)
}

func ipMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 5 {
		t.Fatal("five columns", types)
	}
	for i, typ := range types {
		if typ.Name() != ipColumns[i] || typ.DatabaseTypeName() != ipTypes[i] {
			t.Fatal("column", i, typ.Name(), typ.DatabaseTypeName())
		}
		scan := reflect.TypeFor[string]()
		if i < 3 {
			scan = reflect.TypeFor[int64]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
			if i == 2 {
				scan = reflect.TypeFor[int32]()
			}
		}
		if typ.ScanType() != scan {
			t.Fatal("scan type", i, typ.ScanType(), scan)
		}
		if nullable, known := typ.Nullable(); nullable || known == pg {
			t.Fatal("nullable", i, nullable, known)
		}
		if n, known := typ.Length(); n != 0 || known {
			t.Fatal("length", i, n, known)
		}
		if n, scale, known := typ.DecimalSize(); n != 0 || scale != 0 || known {
			t.Fatal("decimal", i, n, scale, known)
		}
	}
}

func ipShapeStep(q string, pg bool, args ...any) fixture.Step {
	s := fixture.Select(q, ipColumns, ipTypes, nil, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		ipMetadata(t, types, pg)
	}
	return s
}

func ipMemberSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) (int64, int64, int64) {
	t.Helper()
	ipExec(t, p, b, b.DB, "DROP SCHEMA public CASCADE", "CREATE SCHEMA public")
	ipExec(t, p, b, b.DB, "CREATE ROLE ip_reader", "CREATE ROLE ip_other", "CREATE EXTENSION pageinspect", "CREATE TABLE ip_member(a INT,b INT)", "GRANT SELECT ON ip_member TO ip_reader", "GRANT UPDATE(b) ON ip_member TO ip_reader")
	return ipOID(t, p, b, "SELECT oid FROM pg_class WHERE relname='ip_member' AND relnamespace='public'::regnamespace"), ipOwner(t, p, b), ipRole(t, p, b, "ip_reader")
}

func ipTableACL(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, oid, owner, reader int64, right string) {
	t.Helper()
	ipACL(t, p, b, db, 1259, oid, 0, "e", ipGrant{owner, owner, ipTableRights, false}, ipGrant{reader, owner, []string{right}, false})
}

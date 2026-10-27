package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

var subscriptionNames = []string{"oid", "subdbid", "subskiplsn", "subname", "subowner", "subenabled", "subbinary", "substream", "subtwophasestate", "subdisableonerr", "subpasswordrequired", "subrunasowner", "subfailover", "subconninfo", "subslotname", "subsynccommit", "subpublications", "suborigin"}
var subscriptionTypes = []string{"OID", "OID", "PG_LSN", "NAME", "OID", "BOOL", "BOOL", "CHAR", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "NAME", "TEXT", "_TEXT", "TEXT"}
var subscriptionTypeIDs = []int64{26, 26, 3220, 19, 26, 16, 16, 18, 18, 16, 16, 16, 16, 25, 19, 25, 1009, 25}

func subscriptionProjection() string {
	names := append([]string(nil), subscriptionNames[:13]...)
	names = append(names, subscriptionNames[14:]...)
	return strings.Join(names, ",")
}

func subscriptionEmpty(pg bool, query string, public bool, args ...any) fixture.Step {
	var names, types []string
	var indices []int
	for i := range subscriptionNames {
		if public && i == 13 { continue }
		names, types, indices = append(names, subscriptionNames[i]), append(types, subscriptionTypes[i]), append(indices, i)
	}
	s := fixture.Select(query, names, types, nil, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != len(indices) { t.Fatal("subscription metadata", columns, err) }
		for j, c := range columns {
			i := indices[j]
			nullable, known := c.Nullable()
			if c.Name() != names[j] || c.DatabaseTypeName() != types[j] || known == pg || !pg && nullable != (i == 14 || i == 17) { t.Fatal("subscription attribute", i+1, c, nullable, known) }
			scan := reflect.TypeFor[string]()
			switch types[j] { case "OID": scan = reflect.TypeFor[int64](); case "BOOL": scan = reflect.TypeFor[bool]() }
			if pg && types[j] != "BOOL" && types[j] != "TEXT" { scan = reflect.TypeFor[any]() }
			if c.ScanType() != scan { t.Fatal("subscription scan type", i+1, c.ScanType(), scan) }
		}
	}
	return s
}

func TestReferencePGSubscriptionReads(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, source := range []string{"pg_catalog.pg_subscription", "pg_subscription"} {
				for _, suffix := range []string{"", " WHERE FALSE"} {
					s := subscriptionEmpty(b.PG, "SELECT * FROM "+source+suffix, false)
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
				}
			}
		})
	}
}

func subscriptionShape(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	run := func(s fixture.Step) { fixture.OidReferenceRows(t, p.Ctx, b, e, s) }
	run(fixture.Select("SELECT oid,relname,relnamespace,relowner,reltype,relnatts,relkind,relpersistence,relisshared,relhasindex,reltablespace FROM pg_catalog.pg_class WHERE oid=6100", []string{"oid","relname","relnamespace","relowner","reltype","relnatts","relkind","relpersistence","relisshared","relhasindex","reltablespace"}, []string{"OID","NAME","OID","OID","OID","INT2","CHAR","CHAR","BOOL","BOOL","OID"}, [][]any{{int64(6100),"pg_subscription",int64(11),int64(10),int64(6101),int64(18),"r","p",true,true,int64(1664)}}))
	// REVOKE ALL FROM PUBLIC stores the owner-only ACL, not an absent default.
	// Build its exact singleton from numeric owner identity, never role spelling.
	acl := fixture.Select("SELECT relacl IS NOT NULL AS explicit_acl,relacl=ARRAY[pg_catalog.makeaclitem(relowner,relowner,'INSERT,SELECT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER,MAINTAIN',false)] AS owner_only_acl FROM pg_catalog.pg_class WHERE oid=6100", []string{"explicit_acl","owner_only_acl"}, []string{"BOOL","BOOL"}, [][]any{{true,true}})
	run(acl)
	run(fixture.Prepared(acl))
	var ownerPrivileges [][]any
	for _, privilege := range []string{"DELETE","INSERT","MAINTAIN","REFERENCES","SELECT","TRIGGER","TRUNCATE","UPDATE"} {
		ownerPrivileges = append(ownerPrivileges, []any{true,true,privilege,false})
	}
	expandedACL := fixture.Select("SELECT a.grantor=c.relowner AS owner_grantor,a.grantee=c.relowner AS owner_grantee,a.privilege_type,a.is_grantable FROM pg_catalog.pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a WHERE c.oid=6100 ORDER BY a.privilege_type", []string{"owner_grantor","owner_grantee","privilege_type","is_grantable"}, []string{"BOOL","BOOL","TEXT","BOOL"}, ownerPrivileges)
	run(expandedACL)
	run(fixture.Prepared(expandedACL))
	var attrs [][]any
	for i, name := range subscriptionNames {
		collation, dims := int64(0), int64(0)
		if i == 3 || i >= 13 { collation = 950 }
		if i == 16 { dims = 1 }
		attrs = append(attrs, []any{int64(i+1),name,subscriptionTypeIDs[i],i != 14 && i != 17,int64(-1),dims,collation})
	}
	run(fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attndims,attcollation FROM pg_catalog.pg_attribute WHERE attrelid=6100 AND attnum>0 ORDER BY attnum", []string{"attnum","attname","atttypid","attnotnull","atttypmod","attndims","attcollation"}, []string{"INT2","NAME","OID","BOOL","INT4","INT2","OID"}, attrs))
	// Resolve the generated companion rather than pinning the oracle allocator.
	rows, err := e.QueryContext(p.Ctx, "SELECT typarray::bigint FROM pg_catalog.pg_type WHERE oid=6101")
	if err != nil { t.Fatal(err) }
	var array int64
	if !rows.Next() { rows.Close(); t.Fatal("missing subscription row type") }
	if err := rows.Scan(&array); err != nil { rows.Close(); t.Fatal(err) }
	if rows.Next() || rows.Err() != nil { rows.Close(); t.Fatal("duplicate row type") }
	rows.Close()
	if array <= 0 || array == 6101 || !b.PG && array != 10112 { t.Fatal("subscription array", array) }
	run(fixture.Select("SELECT typname,typtype,typrelid,typnamespace,typowner,typarray FROM pg_catalog.pg_type WHERE oid=6101", []string{"typname","typtype","typrelid","typnamespace","typowner","typarray"}, []string{"NAME","CHAR","OID","OID","OID","OID"}, [][]any{{"pg_subscription","c",int64(6100),int64(11),int64(10),array}}))
	run(fixture.Select("SELECT typname,typelem,typnamespace,typowner FROM pg_catalog.pg_type WHERE oid=$1", []string{"typname","typelem","typnamespace","typowner"}, []string{"NAME","OID","OID","OID"}, [][]any{{"_pg_subscription",int64(6101),int64(11),int64(10)}}, array))
}

func TestReferencePGSubscriptionShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name, func(t *testing.T) { subscriptionShape(t,p,b,b.DB) }) }
}

func TestReferencePGSubscriptionDiagnostics(t *testing.T) {
	withSubscriptionReader(t,func(p *fixture.Pair,b fixture.Backend,admin,actor *sql.Conn) {
		for _, e := range []fixture.Executor{admin,actor} {
			for _, prepared := range []bool{false,true} {
				want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
				want.Position = "8"
				s := fixture.ErrorDiagnostic("SELECT missing FROM pg_catalog.pg_subscription", want)
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared { s.ErrorStage = "prepare" }
				fixture.RunStep(t,p.Ctx,e,b.Recorder,s)
				fixture.RunStep(t,p.Ctx,e,b.Recorder,subscriptionEmpty(b.PG,"SELECT "+subscriptionProjection()+" FROM pg_subscription",true))
			}
		}
	})
}

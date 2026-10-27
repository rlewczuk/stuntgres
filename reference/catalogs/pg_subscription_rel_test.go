package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var subscriptionRelNames = []string{"srsubid", "srrelid", "srsubstate", "srsublsn"}
var subscriptionRelTypes = []string{"OID", "OID", "CHAR", "PG_LSN"}

func subscriptionRelMetadata(t *testing.T, pg bool, columns []*sql.ColumnType) {
	t.Helper()
	if len(columns) != 4 { t.Fatal("subscription relation metadata width", columns) }
	for i, c := range columns {
		nullable, known := c.Nullable()
		if c.Name() != subscriptionRelNames[i] || c.DatabaseTypeName() != subscriptionRelTypes[i] || known == pg || !pg && nullable != (i == 3) { t.Fatal("subscription relation metadata", i, c, nullable, known) }
		scan := reflect.TypeFor[string]()
		if i < 2 { scan = reflect.TypeFor[int64]() }
		if pg { scan = reflect.TypeFor[any]() }
		if c.ScanType() != scan { t.Fatal("subscription relation ScanType", i, c.ScanType(), scan) }
	}
}

func subscriptionRelEmpty(pg bool, query string, args ...any) fixture.Step {
	s := fixture.Select(query, subscriptionRelNames, subscriptionRelTypes, nil, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil { t.Fatal(err) }
		subscriptionRelMetadata(t, pg, columns)
	}
	return s
}

func subscriptionRelCount() fixture.Step {
	return fixture.Select("SELECT count(*) FROM pg_catalog.pg_subscription_rel", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
}

func TestReferencePGSubscriptionRelReads(t *testing.T) {
	withSubscriptionRelReader(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin, actor *sql.Conn) {
		for _, e := range []fixture.Executor{admin, actor} {
			for _, source := range []string{"pg_catalog.pg_subscription_rel", "pg_subscription_rel"} {
				for _, suffix := range []string{"", " WHERE FALSE", " WHERE srsubid=$1"} {
					q := "SELECT * FROM " + source + suffix
					var args []any
					if suffix == " WHERE srsubid=$1" { args = []any{int64(0)} }
					s := subscriptionRelEmpty(b.PG, q, args...)
					fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Prepared(s))
					stmt, err := e.PrepareContext(p.Ctx, q)
					if err != nil { t.Fatal(err) }
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}, b.Recorder, s)
					if err := stmt.Close(); err != nil { t.Fatal(err) }
				}
			}
			for _, s := range []fixture.Step{subscriptionRelCount(), fixture.Select("SELECT 1 FROM pg_catalog.pg_subscription_rel", []string{"?column?"}, []string{"INT4"}, nil)} {
				fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Prepared(s))
				stmt, err := e.PrepareContext(p.Ctx, s.Query)
				if err != nil { t.Fatal(err) }
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: e, Statement: stmt, SQL: s.Query}, b.Recorder, s)
				if err := stmt.Close(); err != nil { t.Fatal(err) }
			}
			rows, err := e.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_subscription_rel")
			if err != nil { t.Fatal(err) }
			columns, err := rows.ColumnTypes()
			if err != nil { rows.Close(); t.Fatal(err) }
			subscriptionRelMetadata(t, b.PG, columns)
			if rows.Next() || rows.Err() != nil { rows.Close(); t.Fatal("expected actual empty source", rows.Err()) }
			if err := rows.Close(); err != nil { t.Fatal(err) }
			// database/sql closes at EOF: inspect the descriptors captured while open.
			subscriptionRelMetadata(t, b.PG, columns)
		}
	})
}

func subscriptionRelStoredACL(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, public, reader bool) {
	t.Helper()
	var want [][]any
	for _, privilege := range []string{"DELETE", "INSERT", "MAINTAIN", "REFERENCES", "SELECT", "TRIGGER", "TRUNCATE", "UPDATE"} {
		want = append(want, []any{true, true, false, privilege, false})
	}
	if public { want = append(want, []any{true, false, true, "SELECT", false}) }
	if reader { want = append(want, []any{true, false, false, "SELECT", false}) }
	s := fixture.Select("SELECT a.grantor=c.relowner AS owner_grantor,a.grantee=c.relowner AS owner_grantee,a.grantee=0 AS public_grantee,a.privilege_type,a.is_grantable FROM pg_catalog.pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a WHERE c.oid=6102", []string{"owner_grantor", "owner_grantee", "public_grantee", "privilege_type", "is_grantable"}, []string{"BOOL", "BOOL", "BOOL", "TEXT", "BOOL"}, want)
	s.Bag = true
	for _, prepared := range []bool{false, true} {
		explicit := fixture.Select("SELECT relacl IS NOT NULL AS explicit_acl FROM pg_catalog.pg_class WHERE oid=6102", []string{"explicit_acl"}, []string{"BOOL"}, [][]any{{true}})
		explicit.Prepared = prepared
		fixture.RunStep(t, p.Ctx, e, b.Recorder, explicit)
		next := s; next.Prepared = prepared
		fixture.RunStep(t, p.Ctx, e, b.Recorder, next)
	}
}

func subscriptionRelShape(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	var row, array int64
	rows, err := e.QueryContext(p.Ctx, "SELECT reltype::bigint FROM pg_catalog.pg_class WHERE oid=6102")
	if err != nil { t.Fatal(err) }
	if !rows.Next() { rows.Close(); t.Fatal("missing subscription relation") }
	if err := rows.Scan(&row); err != nil { rows.Close(); t.Fatal(err) }
	if rows.Next() || rows.Err() != nil { rows.Close(); t.Fatal("duplicate subscription relation", rows.Err()) }
	rows.Close()
	rows, err = e.QueryContext(p.Ctx, "SELECT typarray::bigint FROM pg_catalog.pg_type WHERE oid=$1", row)
	if err != nil { t.Fatal(err) }
	if !rows.Next() { rows.Close(); t.Fatal("missing subscription relation row type") }
	if err := rows.Scan(&array); err != nil { rows.Close(); t.Fatal(err) }
	if rows.Next() || rows.Err() != nil { rows.Close(); t.Fatal("duplicate row type", rows.Err()) }
	rows.Close()
	if row <= 0 || array <= 0 || row == array || row == 6102 || array == 6102 { t.Fatal("invalid reciprocal type identities", row, array) }
	run := func(s fixture.Step) {
		fixture.OidReferenceRows(t, p.Ctx, b, e, s)
		fixture.OidReferenceRows(t, p.Ctx, b, e, fixture.Prepared(s))
	}
	run(fixture.Select("SELECT oid,relname,relnamespace,relowner,reltype,relnatts,relkind,relpersistence,relisshared,relhasindex,reltablespace FROM pg_catalog.pg_class WHERE oid=6102", []string{"oid", "relname", "relnamespace", "relowner", "reltype", "relnatts", "relkind", "relpersistence", "relisshared", "relhasindex", "reltablespace"}, []string{"OID", "NAME", "OID", "OID", "OID", "INT2", "CHAR", "CHAR", "BOOL", "BOOL", "OID"}, [][]any{{int64(6102), "pg_subscription_rel", int64(11), int64(10), row, int64(4), "r", "p", false, true, int64(0)}}))
	run(fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attndims,attcollation,attacl IS NULL AS absent_acl FROM pg_catalog.pg_attribute WHERE attrelid=6102 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attndims", "attcollation", "absent_acl"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "INT2", "OID", "BOOL"}, [][]any{
		{int64(1), "srsubid", int64(26), true, int64(-1), int64(0), int64(0), true},
		{int64(2), "srrelid", int64(26), true, int64(-1), int64(0), int64(0), true},
		{int64(3), "srsubstate", int64(18), true, int64(-1), int64(0), int64(0), true},
		{int64(4), "srsublsn", int64(3220), false, int64(-1), int64(0), int64(0), true},
	}))
	run(fixture.Select("SELECT typname,typtype,typrelid,typnamespace,typowner,typarray,typelem FROM pg_catalog.pg_type WHERE oid=$1", []string{"typname", "typtype", "typrelid", "typnamespace", "typowner", "typarray", "typelem"}, []string{"NAME", "CHAR", "OID", "OID", "OID", "OID", "OID"}, [][]any{{"pg_subscription_rel", "c", int64(6102), int64(11), int64(10), array, int64(0)}}, row))
	run(fixture.Select("SELECT typname,typtype,typelem,typrelid,typnamespace,typowner FROM pg_catalog.pg_type WHERE oid=$1", []string{"typname", "typtype", "typelem", "typrelid", "typnamespace", "typowner"}, []string{"NAME", "CHAR", "OID", "OID", "OID", "OID"}, [][]any{{"_pg_subscription_rel", "b", row, int64(0), int64(11), int64(10)}}, array))
	subscriptionRelStoredACL(t, p, b, e, true, false)
}

func TestReferencePGSubscriptionRelShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { subscriptionRelShape(t, p, b, b.DB) })
	}
}

func TestReferencePGSubscriptionRelDiagnostics(t *testing.T) {
	withSubscriptionRelReader(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin, actor *sql.Conn) {
		for _, e := range []fixture.Executor{admin, actor} {
			for _, tc := range []struct { query, state, message string }{
				{"SELECT missing FROM pg_catalog.pg_subscription_rel", "42703", `column "missing" does not exist`},
				{"SELECT oid FROM pg_catalog.pg_subscription_rel", "42703", `column "oid" does not exist`},
				{"SELECT 'bad'::pg_lsn FROM pg_catalog.pg_subscription_rel WHERE FALSE", "22P02", `invalid input syntax for type pg_lsn: "bad"`},
			} {
				for _, prepared := range []bool{false, true} {
					want := fixture.ControlsReferenceDiagnostic(tc.state, tc.message); want.Position = "8"
					s := fixture.ErrorDiagnostic(tc.query, want)
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared { s.ErrorStage = "prepare" }
					fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, e, b.Recorder, subscriptionRelEmpty(b.PG, "SELECT * FROM pg_catalog.pg_subscription_rel"))
				}
			}
		}
	})
}

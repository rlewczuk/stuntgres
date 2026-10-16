package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"github.com/lib/pq"
	"github.com/ngrok/sqlmw"
	"net/url"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCollationFunctionsPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"cf_ordinary"})
	fixture.SchemaReferenceCleanup(t, "cf_auth_inventory")
	dsn := "reference/" + t.Name()
	collationFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.CollationExec(t, p, b, `CREATE USER cf_ordinary`, `GRANT postgres TO cf_ordinary`, `CREATE SCHEMA cf_auth_inventory`)
		if b.PG {
			fixture.CollationExec(t, p, b, `ALTER ROLE cf_ordinary PASSWORD 'role-core-fixture'`)
		}
		var base driver.Connector
		if b.PG {
			u, err := url.Parse(fixture.DSN)
			if err != nil {
				t.Fatal(err)
			}
			u.User = url.UserPassword("cf_ordinary", "role-core-fixture")
			base, err = pq.NewConnector(u.String())
			if err != nil {
				t.Fatal(err)
			}
		} else {
			var err error
			base, err = p.NativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(dsn, "cf_ordinary")
			if err != nil {
				t.Fatal(err)
			}
		}
		wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{Connector: base}, fixture.Interceptor{})
		connector, err := wrapped.(driver.DriverContext).OpenConnector("")
		if err != nil {
			t.Fatal(err)
		}
		ordinary := sql.OpenDB(connector)
		t.Cleanup(func() { ordinary.Close() })
		ob := b
		ob.DB = ordinary
		fixture.CollationRoutes(t, p, ob, fixture.Select(`SELECT pg_collation_actual_version(950::oid) AS c,pg_database_collation_actual_version(oid) AS d FROM pg_database WHERE datname=current_database()`, []string{"c", "d"}, []string{"TEXT", "TEXT"}, [][]any{{nil, nil}}))
		fixture.CollationRoutes(t, p, ob, fixture.Select(`SELECT pg_import_system_collations(NULL::regnamespace) AS n`, []string{"n"}, []string{"INT4"}, [][]any{{nil}}))
		for _, arg := range []any{int64(0), int64(4294967295), "cf_auth_inventory"} {
			collationFunctionsError(t, p, ob, `SELECT pg_import_system_collations($1::regnamespace) AS n`, "42501", "must be superuser to import system collations", arg)
		}
		symbolic := fixture.ImplicitError(`SELECT 'é',pg_import_system_collations('cf_missing_namespace')`, fixture.Diagnostic{Code: "3F000", Message: `schema "cf_missing_namespace" does not exist`, Position: "40"})
		symbolic.SelectRows = true
		fixture.CollationRoutes(t, p, ob, symbolic)
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		q := `SELECT pg_import_system_collations($1) AS n`
		h := collationFunctionsHeld(t, p, b, c, q)
		if _, err = c.ExecContext(p.Ctx, `SET ROLE cf_ordinary`); err != nil {
			t.Fatal(err)
		}
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "must be superuser to import system collations"}, int64(0))
		s.SelectRows = true
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"n"}, []string{"INT4"}, [][]any{{nil}}, nil))
		if _, err = c.ExecContext(p.Ctx, `RESET ROLE`); err != nil {
			t.Fatal(err)
		}
		if b.PG {
			var n int64
			if err = h.Statement.QueryRowContext(p.Ctx, "cf_auth_inventory").Scan(&n); err != nil || n <= 0 {
				t.Fatalf("fresh admin import count=%d err=%v", n, err)
			}
		} else {
			s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "system collation import is unsupported in embedded mode"}, "cf_auth_inventory")
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
		}
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT pg_import_system_collations('cf_auth_inventory'::regnamespace) AS n WHERE false`, []string{"n"}, []string{"INT4"}, nil))
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT CASE WHEN false THEN pg_import_system_collations('cf_auth_inventory'::regnamespace) ELSE 7 END AS n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
		fixture.CollationExec(t, p, b, `CREATE VIEW cf_undemanded_import AS SELECT pg_import_system_collations('cf_auth_inventory'::regnamespace) AS n WHERE false`)
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT n FROM cf_undemanded_import`, []string{"n"}, []string{"INT4"}, nil))
	})
}

const collationFunctionsInventory = `SELECT oid,collname,collnamespace,collowner,collprovider,collisdeterministic,collencoding,collcollate,collctype,colllocale,collicurules,collversion FROM pg_collation WHERE collnamespace=$1::regnamespace::oid ORDER BY collname,collencoding`

func collationFunctionsBag(t *testing.T, p *fixture.Pair, b fixture.Backend, schema string) [][]any {
	t.Helper()
	r, err := b.DB.QueryContext(p.Ctx, collationFunctionsInventory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var out [][]any
	for r.Next() {
		v := make([]any, 12)
		ptr := make([]any, 12)
		for i := range v {
			ptr[i] = &v[i]
		}
		if err = r.Scan(ptr...); err != nil {
			t.Fatal(err)
		}
		for i, x := range v {
			if bytes, ok := x.([]byte); ok {
				v[i] = string(bytes)
			}
		}
		out = append(out, v)
	}
	if err = r.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReferenceCollationFunctionsImport(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "cf_import_inventory")
	collationFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.CollationExec(t, p, b, `CREATE SCHEMA cf_import_inventory`, `CREATE COLLATION cf_import_inventory.cf_sentinel FROM pg_catalog."C"`)
		before := collationFunctionsBag(t, p, b, "cf_import_inventory")
		if len(before) != 1 {
			t.Fatal("missing sentinel", before)
		}
		q := `SELECT pg_import_system_collations($1::regnamespace) AS n`
		h := collationFunctionsHeld(t, p, b, b.DB, q)
		if !b.PG {
			collationFunctionsError(t, p, b, q, "0A000", "system collation import is unsupported in embedded mode", "cf_import_inventory")
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "system collation import is unsupported in embedded mode"}, "cf_import_inventory")
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"n"}, []string{"INT4"}, [][]any{{nil}}, nil))
			fixture.CollationExec(t, p, b, `CREATE TABLE cf_import_atomic(id int,n int)`)
			fixture.CollationStep(t, p, b, fixture.Exec(`INSERT INTO cf_import_atomic VALUES(0,7)`, 1))
			insert := `INSERT INTO cf_import_atomic SELECT id,CASE WHEN id=2 THEN pg_import_system_collations('cf_import_inventory'::regnamespace) ELSE 7 END FROM cf_import_source ORDER BY id`
			fixture.CollationExec(t, p, b, `CREATE TABLE cf_import_source(id int)`)
			fixture.CollationStep(t, p, b, fixture.Exec(`INSERT INTO cf_import_source VALUES(1),(2)`, 2))
			failure := fixture.ImplicitError(insert, fixture.Diagnostic{Code: "0A000", Message: "system collation import is unsupported in embedded mode"})
			failure.ErrorStage = "execute"
			fixture.CollationRoutes(t, p, b, failure)
			fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT id,n FROM cf_import_atomic`, []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(7)}}))
			if after := collationFunctionsBag(t, p, b, "cf_import_inventory"); !reflect.DeepEqual(before, after) {
				t.Fatal("unsupported import mutated catalog", before, after)
			}
			return
		}
		var n int64
		if err := h.Statement.QueryRowContext(p.Ctx, "cf_import_inventory").Scan(&n); err != nil {
			t.Fatal(err)
		}
		after := collationFunctionsBag(t, p, b, "cf_import_inventory")
		if n <= 0 || int64(len(after)-len(before)) != n {
			t.Fatalf("actual imports=%d catalog growth=%d", n, len(after)-len(before))
		}
		var chosen string
		var oldOID any
		providers := map[any]int{}
		for _, row := range after {
			if row[1] == "cf_sentinel" {
				if !reflect.DeepEqual(row, before[0]) {
					t.Fatal("sentinel changed", row)
				}
				continue
			}
			providers[row[4]]++
			if row[4] != "c" && row[4] != "i" {
				t.Fatal("unexpected imported provider", row)
			}
			if row[5] != true || row[2] != before[0][2] || row[3] != before[0][3] {
				t.Fatal("wrong import authority/namespace/determinism", row)
			}
			if row[4] == "i" {
				if row[6] != int64(-1) || row[9] == nil || row[11] == nil {
					t.Fatal("invalid ICU definition", row)
				}
				if chosen == "" {
					chosen = fmt.Sprint(row[1])
					oldOID = row[0]
				}
			}
		}
		if chosen == "" {
			t.Fatal("pinned ICU inventory absent")
		}
		if n != 810 || providers["c"] != 4 || providers["i"] != 806 {
			t.Fatalf("pinned import inventory: count=%d providers=%v", n, providers)
		}
		for _, tc := range []struct {
			name   string
			fields []any
		}{
			{"C", []any{"c", true, int64(6), "C.utf8", "C.utf8", nil, nil, nil}},
			{"C.utf8", []any{"c", true, int64(6), "C.utf8", "C.utf8", nil, nil, nil}},
			{"af-x-icu", []any{"i", true, int64(-1), nil, nil, "af", nil, "153.120.42"}},
			{"agq-CM-x-icu", []any{"i", true, int64(-1), nil, nil, "agq-CM", nil, "153.120"}},
		} {
			found := false
			for _, row := range after {
				if row[1] == tc.name {
					found = true
					if !reflect.DeepEqual(row[4:], tc.fields) {
						t.Fatalf("provider %s: %#v want %#v", tc.name, row[4:], tc.fields)
					}
				}
			}
			if !found {
				t.Fatal("missing pinned provider representative", tc.name)
			}
		}
		fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}}, "cf_import_inventory"))
		if repeat := collationFunctionsBag(t, p, b, "cf_import_inventory"); !reflect.DeepEqual(after, repeat) {
			t.Fatal("repeat changed definitions or identities")
		}
		fixture.CollationExec(t, p, b, `DROP COLLATION cf_import_inventory."`+chosen+`"`)
		fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}, "cf_import_inventory"))
		replaced := collationFunctionsBag(t, p, b, "cf_import_inventory")
		if len(replaced) != len(after) {
			t.Fatalf("replacement inventory length=%d want %d", len(replaced), len(after))
		}
		for i, row := range replaced {
			if row[1] == chosen {
				if row[0] == oldOID {
					t.Fatal("replacement reused dropped identity")
				}
				row[0] = oldOID
			}
			if !reflect.DeepEqual(row, after[i]) {
				t.Fatal("single replacement changed other definitions", row, after[i])
			}
		}
		fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}}, "cf_import_inventory"))
	})
}

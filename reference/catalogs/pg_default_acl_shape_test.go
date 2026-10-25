package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

var pgDefaultACLTypes = []string{"OID", "OID", "OID", "CHAR", "_ACLITEM"}

func pgDefaultACLScenario(t *testing.T, f func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { f(t, p, b) })
	}
}

func pgDefaultACLMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 5 {
		t.Fatal("five columns", types)
	}
	for i, typ := range types {
		if typ.Name() != fixture.PGDefaultACLColumns[i] || typ.DatabaseTypeName() != pgDefaultACLTypes[i] {
			t.Fatal("column", i, typ.Name(), typ.DatabaseTypeName())
		}
		scan := reflect.TypeFor[string]()
		if i < 3 {
			scan = reflect.TypeFor[int64]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
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

func pgDefaultACLSelect(q string, pg bool, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, fixture.PGDefaultACLColumns, pgDefaultACLTypes, rows, args...)
	s.Bag = true
	if pg {
		s.Rows = make([][]any, len(rows))
		for i, row := range rows {
			s.Rows[i] = append([]any(nil), row...)
			for j := range 3 {
				s.Rows[i][j] = strconv.FormatInt(row[j].(int64), 10)
			}
		}
	}
	s.CheckRaw = func(t *testing.T, col, row int, value any) bool {
		if pg && col != 3 {
			if _, ok := value.([]byte); !ok {
				t.Fatalf("pq %s carrier: %T", pgDefaultACLTypes[col], value)
			}
		} else if !pg && col < 3 {
			if _, ok := value.(int64); !ok {
				t.Fatalf("native OID carrier: %T", value)
			}
		} else if _, ok := value.(string); !ok {
			t.Fatalf("%s carrier: %T", pgDefaultACLTypes[col], value)
		}
		return false
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		names, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(names, fixture.PGDefaultACLColumns) {
			t.Fatal("labels", names, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		pgDefaultACLMetadata(t, types, pg)
		if !pg {
			names[0] = "corrupted"
			again, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(again, fixture.PGDefaultACLColumns) {
				t.Fatal("owned labels", again, err)
			}
		}
	}
	return s
}

func pgDefaultACLRoles(t *testing.T, p *fixture.Pair, b fixture.Backend, names ...string) {
	t.Helper()
	for _, name := range names {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE ROLE "+pq.QuoteIdentifier(name), 0))
		t.Cleanup(func() {
			for _, q := range []string{"DROP OWNED BY " + pq.QuoteIdentifier(name) + " CASCADE", "DROP ROLE " + pq.QuoteIdentifier(name)} {
				if _, err := b.DB.ExecContext(p.Ctx, q); err != nil {
					t.Error("default ACL cleanup", q, err)
				}
			}
		})
	}
}

func pgDefaultACLOID(t *testing.T, p *fixture.Pair, b fixture.Backend, query string, args ...any) int64 {
	t.Helper()
	var oid int64
	if err := b.DB.QueryRowContext(p.Ctx, query, args...).Scan(&oid); err != nil || oid <= 0 || oid > 4294967295 {
		t.Fatal("actual identity", oid, err)
	}
	return oid
}

func pgDefaultACLRead(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, rows [][]any, args ...any) {
	t.Helper()
	s := pgDefaultACLSelect(q, b.PG, rows, args...)
	for _, plain := range []bool{false, true} {
		s.NonContext = plain
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
	}
	s.NonContext = false
	fixture.RunStep(t, p.Ctx, fixture.ViewHold(t, p, b, q), b.Recorder, s)
}

func TestReferencePGDefaultACLShape(t *testing.T) {
	pgDefaultACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgDefaultACLRoles(t, p, b, "dacl18_shape_creator")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dacl18_shape_creator GRANT ALL ON TABLES TO dacl18_shape_creator", 0))
		for _, q := range []string{"SELECT * FROM pg_catalog.pg_default_acl WHERE defaclrole=(SELECT oid FROM pg_roles WHERE rolname='dacl18_shape_creator')", "SELECT * FROM pg_default_acl WHERE FALSE", "SELECT oid,defaclrole,defaclnamespace,defaclobjtype,defaclacl FROM pg_default_acl WHERE oid=$1"} {
			var args []any
			if q == "SELECT oid,defaclrole,defaclnamespace,defaclobjtype,defaclacl FROM pg_default_acl WHERE oid=$1" {
				args = []any{int64(0)}
			}
			pgDefaultACLRead(t, p, b, q, nil, args...)
		}
		rows, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_default_acl WHERE FALSE")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		saved, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("empty EOF", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		pgDefaultACLMetadata(t, saved, b.PG)
	})
}

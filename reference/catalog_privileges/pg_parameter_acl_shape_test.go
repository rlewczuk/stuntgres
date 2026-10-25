package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

var pgParameterACLTypes = []string{"OID", "TEXT", "_ACLITEM"}

func pgParameterACLScenario(t *testing.T, f func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { f(t, p, b) })
	}
}

func pgParameterACLMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 3 {
		t.Fatalf("three columns: %v", types)
	}
	for i, typ := range types {
		if typ.Name() != fixture.PGParameterACLColumns[i] || typ.DatabaseTypeName() != pgParameterACLTypes[i] {
			t.Fatalf("column %d: %s/%s", i+1, typ.Name(), typ.DatabaseTypeName())
		}
		scan := reflect.TypeFor[string]()
		if i == 0 {
			scan = reflect.TypeFor[int64]()
		}
		if pg && i != 1 {
			scan = reflect.TypeFor[any]()
		}
		if typ.ScanType() != scan {
			t.Fatal("scan type", i, typ.ScanType(), scan)
		}
		if nullable, known := typ.Nullable(); (pg && (nullable || known)) || (!pg && (!known || nullable != (i == 2))) {
			t.Fatal("nullable", i, nullable, known)
		}
		length, lengthKnown := int64(0), false
		if i == 1 {
			length, lengthKnown = math.MaxInt64, true
		}
		if n, known := typ.Length(); n != length || known != lengthKnown {
			t.Fatal("length", i, n, known)
		}
		if n, scale, known := typ.DecimalSize(); n != 0 || scale != 0 || known {
			t.Fatal("decimal", i, n, scale, known)
		}
	}
}

func pgParameterACLSelect(q string, pg bool, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, fixture.PGParameterACLColumns, pgParameterACLTypes, rows, args...)
	// Match fixture.PGDatabaseCheck's backend transport contract without changing the
	// independently checked uint32 identity or the public OID database type.
	if pg {
		s.Rows = make([][]any, len(rows))
		for i, row := range rows {
			s.Rows[i] = append([]any(nil), row...)
			s.Rows[i][0] = strconv.FormatInt(row[0].(int64), 10)
		}
	}
	s.CheckRaw = func(t *testing.T, col, row int, value any) bool {
		if pg && col != 1 {
			if _, ok := value.([]byte); !ok {
				t.Fatalf("pq %s raw carrier: %T", pgParameterACLTypes[col], value)
			}
		} else if col == 0 {
			if _, ok := value.(int64); !ok {
				t.Fatalf("native OID raw carrier: %T", value)
			}
		} else if _, ok := value.(string); !ok {
			t.Fatalf("%s raw carrier: %T", pgParameterACLTypes[col], value)
		}
		return false
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		names, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(names, fixture.PGParameterACLColumns) {
			t.Fatal("labels", names, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		pgParameterACLMetadata(t, types, pg)
		if !pg {
			names[0] = "corrupted"
			again, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(again, fixture.PGParameterACLColumns) {
				t.Fatal("owned labels", again, err)
			}
		}
	}
	return s
}

func pgParameterACLOwner(b fixture.Backend) string {
	if b.PG {
		return "stuntgres"
	}
	return "postgres"
}

func pgParameterACLRoles(t *testing.T, p *fixture.Pair, b fixture.Backend, names ...string) {
	t.Helper()
	for _, name := range names {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE ROLE "+name, 0))
		t.Cleanup(func() {
			for _, q := range []string{"DROP OWNED BY " + name + " CASCADE", "DROP ROLE " + name} {
				if _, err := b.DB.ExecContext(p.Ctx, q); err != nil {
					t.Error("parameter ACL cleanup", q, err)
				}
			}
		})
	}
}

func pgParameterACLOID(t *testing.T, p *fixture.Pair, b fixture.Backend, name string) int64 {
	t.Helper()
	var oid int64
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_parameter_acl WHERE parname=$1", name).Scan(&oid); err != nil || oid <= 0 || oid > 4294967295 {
		t.Fatal("actual parameter identity", oid, err)
	}
	return oid
}

func pgParameterACLRead(t *testing.T, p *fixture.Pair, b fixture.Backend, name, acl string, oid int64) {
	t.Helper()
	q := "SELECT * FROM pg_catalog.pg_parameter_acl WHERE parname=$1"
	s := pgParameterACLSelect(q, b.PG, [][]any{{oid, name, acl}}, name)
	for _, plain := range []bool{false, true} {
		s.NonContext = plain
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
	}
	s.NonContext = false
	held := fixture.ViewHold(t, p, b, q)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
}

func TestReferencePGParameterACLShape(t *testing.T) {
	pgParameterACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{
			"SELECT * FROM pg_catalog.pg_parameter_acl WHERE parname='pacl18_shape.absent'",
			"SELECT * FROM pg_parameter_acl WHERE FALSE",
			"SELECT oid,parname,paracl FROM pg_parameter_acl WHERE parname=$1",
		} {
			var args []any
			if q == "SELECT oid,parname,paracl FROM pg_parameter_acl WHERE parname=$1" {
				args = []any{"pacl18_shape.absent"}
			}
			s := pgParameterACLSelect(q, b.PG, nil, args...)
			for _, plain := range []bool{false, true} {
				s.NonContext = plain
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
			}
			s.NonContext = false
			held := fixture.ViewHold(t, p, b, q)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		rows, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_parameter_acl WHERE FALSE")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		saved, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		pgParameterACLMetadata(t, saved, b.PG)
		if rows.Next() || rows.Err() != nil {
			t.Fatal("empty EOF", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		pgParameterACLMetadata(t, saved, b.PG)
	})
}

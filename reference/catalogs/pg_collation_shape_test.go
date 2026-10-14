package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var pgCollationColumns = []string{"oid", "collname", "collnamespace", "collowner", "collprovider", "collisdeterministic", "collencoding", "collcollate", "collctype", "colllocale", "collicurules", "collversion"}
var pgCollationTypes = []string{"OID", "NAME", "OID", "OID", "CHAR", "BOOL", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}

func pgCollationScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	fixture.CollationScenario(t, body)
}

func pgCollationMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, pgCollationColumns) {
		t.Fatal("twelve labels", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 12 {
		t.Fatal("twelve types", types, err)
	}
	for i, typ := range types {
		if typ.Name() != pgCollationColumns[i] || typ.DatabaseTypeName() != pgCollationTypes[i] {
			t.Fatal("column identity", i, typ.Name(), typ.DatabaseTypeName())
		}
		if nullable, known := typ.Nullable(); (pg && known) || (!pg && (!known || nullable != (i >= 7))) {
			t.Fatal("nullable", i, nullable, known)
		}
		scan := reflect.TypeFor[string]()
		switch pgCollationTypes[i] {
		case "OID", "INT4":
			scan = reflect.TypeFor[int64]()
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		}
		if pg {
			switch pgCollationTypes[i] {
			case "INT4":
				scan = reflect.TypeFor[int32]()
			case "BOOL", "TEXT":
			default:
				scan = reflect.TypeFor[any]()
			}
		}
		if typ.ScanType() != scan {
			t.Fatal("ScanType", i, typ.ScanType(), scan)
		}
		length, lengthKnown := int64(0), false
		if pgCollationTypes[i] == "TEXT" {
			length, lengthKnown = math.MaxInt64, true
		}
		if n, known := typ.Length(); n != length || known != lengthKnown {
			t.Fatal("length", i, n, known)
		}
		if n, scale, known := typ.DecimalSize(); n != 0 || scale != 0 || known {
			t.Fatal("decimal size", i, n, scale, known)
		}
	}
	// Obtained descriptors remain independently usable after the cursor closes.
	t.Cleanup(func() {
		for i, typ := range types {
			if typ.Name() != pgCollationColumns[i] || typ.DatabaseTypeName() != pgCollationTypes[i] {
				t.Error("retained metadata changed", i, typ)
			}
		}
	})
}

func pgCollationStep(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, s fixture.Step) {
	t.Helper()
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgCollationMetadata(t, rows, b.PG) }
	// The shared database-catalog runner preserves pq's raw OID string contract
	// while asserting both the scanned and recorded native unsigned carriers.
	pgDatabaseCheck(t, p, b, x, s)
}

func pgCollationRow(oid int64, name string, namespace, owner int64, provider string, encoding int64, collate, ctype, locale, version any) []any {
	return []any{oid, name, namespace, owner, provider, true, encoding, collate, ctype, locale, nil, version}
}

func TestReferencePGCollationShape(t *testing.T) {
	pgCollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			query string
			args  []any
			rows  [][]any
		}{
			{"SELECT * FROM pg_catalog.pg_collation WHERE oid=811", nil, [][]any{pgCollationRow(811, "pg_c_utf8", 11, 10, "b", 6, nil, nil, "C.UTF-8", "1")}},
			{"SELECT * FROM pg_catalog.pg_collation WHERE FALSE", nil, nil},
			{"SELECT * FROM pg_catalog.pg_collation WHERE oid=$1", []any{int64(4294967295)}, nil},
			{"SELECT * FROM pg_catalog.pg_collation WHERE oid=$1", []any{nil}, nil},
		} {
			s := fixture.Select(tc.query, pgCollationColumns, pgCollationTypes, tc.rows, tc.args...)
			for _, plain := range []bool{false, true} {
				s.NonContext = plain
				pgCollationStep(t, p, b, b.DB, s)
				pgCollationStep(t, p, b, b.DB, fixture.Prepared(s))
			}
			s.NonContext = false
			held := fixture.ViewHold(t, p, b, tc.query)
			pgCollationStep(t, p, b, held, s)
			pgCollationStep(t, p, b, held, s)
		}
		q := "SELECT * FROM pg_catalog.pg_collation WHERE oid=$1"
		held := fixture.ViewHold(t, p, b, q)
		pgCollationStep(t, p, b, held, fixture.Select(q, pgCollationColumns, pgCollationTypes, [][]any{pgCollationRow(811, "pg_c_utf8", 11, 10, "b", 6, nil, nil, "C.UTF-8", "1")}, int64(811)))
		if b.PG {
			// REL_18_0 pg_collation.h and genbki.pl prescribe this vector,
			// independently of the implementation descriptor under test.
			ids := []int64{26, 19, 26, 26, 18, 16, 23, 25, 25, 25, 25, 25}
			collations := []int64{0, 950, 0, 0, 0, 0, 0, 950, 950, 950, 950, 950}
			want := make([][]any, 12)
			for i := range want {
				want[i] = []any{int64(i + 1), pgCollationColumns[i], ids[i], int64(-1), i < 7, collations[i]}
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT attnum::bigint AS n,attname::text AS name,atttypid::bigint AS type,atttypmod::bigint AS mod,attnotnull,attcollation::bigint AS collation FROM pg_catalog.pg_attribute WHERE attrelid=3456 AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"n", "name", "type", "mod", "attnotnull", "collation"}, []string{"INT8", "TEXT", "INT8", "INT8", "BOOL", "INT8"}, want))
		}
	})
}

func pgCollationAttributeProbe(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	fixture.CollationExec(t, p, b, `CREATE TABLE pgc_attr_probe(v TEXT COLLATE "POSIX")`)
	fixture.CollationStep(t, p, b, fixture.Exec("INSERT INTO pgc_attr_probe VALUES ('1')", 1))
	for _, tc := range []struct {
		field, value string
		oid          int64
	}{
		{"collversion", "1", 811}, {"collcollate", "C", 950}, {"collctype", "C", 950}, {"colllocale", "C.UTF-8", 811},
	} {
		fixture.CollationStep(t, p, b, fixture.Exec("UPDATE pgc_attr_probe SET v=$1", 1, tc.value))
		q := "SELECT c." + tc.field + " = p.v AS same FROM pg_catalog.pg_collation c CROSS JOIN pgc_attr_probe p WHERE c.oid=" + fmtCollationOID(tc.oid)
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly."})
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.CollationRoutes(t, p, b, s)
		held := fixture.ViewHold(t, p, b, q)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		control := "SELECT c." + tc.field + ` COLLATE "POSIX" = p.v AS same FROM pg_catalog.pg_collation c CROSS JOIN pgc_attr_probe p WHERE c.oid=` + fmtCollationOID(tc.oid)
		fixture.CollationRoutes(t, p, b, fixture.Select(control, []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
		fixture.CollationRoutes(t, p, b, fixture.Select("SELECT c."+tc.field+" = p.v AS same FROM pg_catalog.pg_collation c CROSS JOIN pgc_attr_probe p WHERE FALSE", []string{"same"}, []string{"BOOL"}, nil))
	}
}

func TestReferencePGCollationAttributeCollation(t *testing.T) {
	pgCollationScenario(t, pgCollationAttributeProbe)
}

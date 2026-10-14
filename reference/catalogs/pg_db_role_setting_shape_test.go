package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var pgDBRoleSettingColumns = []string{"setdatabase", "setrole", "setconfig"}
var pgDBRoleSettingTypes = []string{"OID", "OID", "_TEXT"}

func pgDBRoleSettingScenario(t *testing.T, f func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { f(t, p, b) })
	}
}

func pgDBRoleSettingMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 3 {
		t.Fatalf("three columns: %v", types)
	}
	for i, typ := range types {
		if typ.Name() != pgDBRoleSettingColumns[i] || typ.DatabaseTypeName() != pgDBRoleSettingTypes[i] {
			t.Fatalf("column %d: %s/%s", i+1, typ.Name(), typ.DatabaseTypeName())
		}
		scan := reflect.TypeFor[int64]()
		if i == 2 {
			scan = reflect.TypeFor[string]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if typ.ScanType() != scan {
			t.Fatal("scan type", i, typ.ScanType(), scan)
		}
		if nullable, known := typ.Nullable(); (pg && (nullable || known)) || (!pg && (!known || nullable != (i == 2))) {
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

func pgDBRoleSettingEmpty(q string, pg bool, args ...any) fixture.Step {
	s := fixture.Select(q, pgDBRoleSettingColumns, pgDBRoleSettingTypes, nil, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		names, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(names, pgDBRoleSettingColumns) {
			t.Fatal("labels", names, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		pgDBRoleSettingMetadata(t, types, pg)
		if !pg {
			names[0] = "corrupted"
			again, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(again, pgDBRoleSettingColumns) {
				t.Fatal("owned labels", again, err)
			}
		}
	}
	return s
}

func pgDBRoleSettingShape(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	q := "SELECT * FROM pg_db_role_setting WHERE setdatabase=$1 AND setrole=$2"
	for _, tc := range []struct {
		q    string
		args []any
	}{
		{"SELECT * FROM pg_catalog.pg_db_role_setting", nil},
		{"SELECT * FROM pg_db_role_setting", nil},
		{"SELECT * FROM pg_db_role_setting WHERE FALSE", nil},
		{q, []any{int64(0), int64(0)}},
		{q, []any{int64(4294967295), int64(4294967295)}},
		{q, []any{nil, nil}},
	} {
		for _, plain := range []bool{false, true} {
			s := pgDBRoleSettingEmpty(tc.q, b.PG, tc.args...)
			s.NonContext = plain
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		}
	}
	held := fixture.ViewHold(t, p, b, q)
	for _, args := range [][]any{{int64(0), int64(0)}, {int64(4294967295), int64(4294967295)}, {nil, nil}, {int64(0), int64(0)}} {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgDBRoleSettingEmpty(q, b.PG, args...))
	}
	count := fixture.Select("SELECT count(*) AS n FROM pg_db_role_setting", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, count)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(count))
	rows, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_db_role_setting")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	saved, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	pgDBRoleSettingMetadata(t, saved, b.PG)
	if rows.Next() || rows.Err() != nil {
		t.Fatal("empty EOF", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	pgDBRoleSettingMetadata(t, saved, b.PG)
}

func TestReferencePGDBRoleSettingShape(t *testing.T) {
	pgDBRoleSettingScenario(t, pgDBRoleSettingShape)
}

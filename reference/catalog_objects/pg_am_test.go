package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

var pgAMTypes = []string{"OID", "NAME", "REGPROC", "CHAR"}

const pgAMProbe = "SELECT * FROM pg_catalog.pg_am WHERE oid=$1::oid"

// Released REL_18_0 pg_am.dat and pg_proc.dat, not another backend's output.
func pgAMFacts(pg bool, numeric bool) [][]any {
	rows := [][]any{
		{int64(2), "heap", int64(3), "t"},
		{int64(403), "btree", int64(330), "i"},
		{int64(405), "hash", int64(331), "i"},
		{int64(783), "gist", int64(332), "i"},
	}
	names := []string{"heap_tableam_handler", "bthandler", "hashhandler", "gisthandler"}
	if pg {
		rows = append(rows, []any{int64(2742), "gin", int64(333), "i"})
		names = append(names, "ginhandler")
	}
	rows = append(rows, []any{int64(3580), "brin", int64(335), "i"})
	names = append(names, "brinhandler")
	rows = append(rows, []any{int64(4000), "spgist", int64(334), "i"})
	names = append(names, "spghandler")
	if !numeric {
		for i := range rows {
			rows[i][2] = names[i]
		}
	}
	return rows
}

func pgAMScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func pgAMSavedMetadata(t *testing.T, columns []*sql.ColumnType, pg, nullable bool) {
	t.Helper()
	if len(columns) != 4 {
		t.Fatal("pg_am width", len(columns))
	}
	for i, c := range columns {
		scan := reflect.TypeFor[string]()
		if i == 0 {
			scan = reflect.TypeFor[int64]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if c.Name() != fixture.PGAMColumns[i] || c.DatabaseTypeName() != pgAMTypes[i] || c.ScanType() != scan {
			t.Fatalf("column %d: %q/%q/%v want %q/%q/%v", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), fixture.PGAMColumns[i], pgAMTypes[i], scan)
		}
		if n, known := c.Nullable(); n != (!pg && nullable) || known != !pg {
			t.Fatal("nullability", i, n, known)
		}
		if n, known := c.Length(); n != 0 || known {
			t.Fatal("length", i, n, known)
		}
		if p, s, known := c.DecimalSize(); p != 0 || s != 0 || known {
			t.Fatal("decimal", i, p, s, known)
		}
	}
}

func pgAMMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, fixture.PGAMColumns) {
		t.Fatal("pg_am columns", columns, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	pgAMSavedMetadata(t, meta, pg, false)
}

func pgAMStep(b fixture.Backend, q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, fixture.PGAMColumns, pgAMTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgAMMetadata(t, rows, b.PG) }
	s.CheckRaw = func(t *testing.T, col, row int, value any) bool {
		typ := reflect.TypeFor[string]()
		if col == 0 {
			typ = reflect.TypeFor[int64]()
		}
		if b.PG && col != 3 {
			typ = reflect.TypeFor[[]byte]()
		}
		if reflect.TypeOf(value) != typ {
			t.Errorf("raw column %d row %d: %T/%v want %v", col, row, value, value, typ)
		}
		return true
	}
	return s
}

func pgAMRows(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, want [][]any, prepared bool, args ...any) {
	t.Helper()
	s := pgAMStep(b, q, want, args...)
	s.Prepared = prepared
	fixture.PgDescriptionExecutorRows(t, p, b, e, s)
}

func pgAMLookup(value any, name bool) [][]any {
	column := 0
	if name {
		column = 1
	}
	for _, row := range pgAMFacts(false, false) {
		if row[column] == value {
			return [][]any{row}
		}
	}
	return nil
}

func pgAMReads(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	for _, probe := range []struct {
		q      string
		name   bool
		values []any
	}{
		{pgAMProbe, false, []any{int64(2), int64(403), int64(405), int64(783), int64(3580), int64(0), nil, int64(2)}},
		{"SELECT * FROM pg_catalog.pg_am WHERE amname=$1::name", true, []any{"heap", "hash", "brin", "missing", nil, "gist"}},
	} {
		stmt, err := e.PrepareContext(p.Ctx, probe.q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: e, Statement: stmt, SQL: probe.q}
		for _, value := range probe.values {
			for _, prepared := range []bool{false, true} {
				pgAMRows(t, p, b, e, probe.q, pgAMLookup(value, probe.name), prepared, value)
			}
			pgAMRows(t, p, b, held, probe.q, pgAMLookup(value, probe.name), false, value)
		}
	}
}

func pgAMInventory(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		for _, relation := range []string{"pg_catalog.pg_am", "pg_am"} {
			pgAMRows(t, p, b, b.DB, "SELECT * FROM "+relation+" ORDER BY oid", pgAMFacts(b.PG, false), prepared)
		}
		s := fixture.Select("SELECT oid,amname,amhandler::oid AS amhandler,amtype FROM pg_catalog.pg_am ORDER BY oid", fixture.PGAMColumns, []string{"OID", "NAME", "OID", "CHAR"}, pgAMFacts(b.PG, true))
		s.Prepared = prepared
		fixture.PgDescriptionRows(t, p, b, s)
		pgAMRows(t, p, b, b.DB, "SELECT * FROM pg_am WHERE oid IN (2,403,405,783,3580,4000) ORDER BY oid", pgAMFacts(false, false), prepared)
	}
	s := pgAMStep(b, "SELECT * FROM pg_catalog.pg_am", pgAMFacts(b.PG, false))
	s.Bag = true
	fixture.PgDescriptionRows(t, p, b, s)
}

func TestReferencePGAMInventory(t *testing.T) {
	pgAMScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgAMInventory(t, p, b)
		pgAMReads(t, p, b, b.DB)
	})
}

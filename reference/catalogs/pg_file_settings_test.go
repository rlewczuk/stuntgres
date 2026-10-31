package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const pgFileSettingsSelect = "SELECT sourcefile,sourceline,seqno,name,setting,applied,error FROM pg_catalog.pg_file_settings"

var pgFileSettingsColumns = []string{"sourcefile", "sourceline", "seqno", "name", "setting", "applied", "error"}
var pgFileSettingsTypes = []string{"TEXT", "INT4", "INT4", "TEXT", "TEXT", "BOOL", "TEXT"}

func pgFileSettingsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func pgFileSettingsMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, pgFileSettingsColumns) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 7 {
		t.Fatal(types, err)
	}
	fixture.ScalarAssertMetadata(t, pg, types)
	for i, typ := range types {
		if typ.Name() != pgFileSettingsColumns[i] || typ.DatabaseTypeName() != pgFileSettingsTypes[i] {
			t.Fatal("descriptor", i, typ)
		}
		nullable, known := typ.Nullable()
		if pg && (nullable || known) || !pg && (!nullable || !known) {
			t.Fatal("nullability", i, nullable, known)
		}
		length, lengthKnown := typ.Length()
		precision, scale, decimalKnown := typ.DecimalSize()
		if precision != 0 || scale != 0 || decimalKnown {
			t.Fatal("decimal metadata", i, precision, scale, decimalKnown)
		}
		if pgFileSettingsTypes[i] == "TEXT" {
			if length != math.MaxInt64 || !lengthKnown || typ.ScanType() != reflect.TypeOf("") {
				t.Fatal("text metadata", i, length, lengthKnown, typ.ScanType())
			}
		} else if lengthKnown {
			t.Fatal("fixed type length", i, length)
		}
	}
}

func pgFileSettingsRead(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	s := fixture.Select(pgFileSettingsSelect+" WHERE FALSE", pgFileSettingsColumns, pgFileSettingsTypes, nil)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgFileSettingsMetadata(t, rows, b.PG) }
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func pgFileSettingsSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT n FROM pgfilesettings_sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
}

// PostgreSQL owns real configuration files; native has no parsed GUC source.
// This checks each model independently instead of comparing incompatible counts.
func pgFileSettingsSource(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, prepared bool) {
	t.Helper()
	if !b.PG {
		for _, query := range []string{pgFileSettingsSelect, "SELECT * FROM pg_file_settings ORDER BY seqno"} {
			s := fixture.Select(query, pgFileSettingsColumns, pgFileSettingsTypes, nil)
			s.Prepared = prepared
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgFileSettingsMetadata(t, rows, false) }
			fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT count(*) FROM pg_catalog.pg_file_settings", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		return
	}
	var config, contents string
	if err := b.DB.QueryRowContext(p.Ctx, "SHOW config_file").Scan(&config); err != nil {
		t.Fatal(err)
	}
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT pg_read_file(current_setting('config_file'))").Scan(&contents); err != nil {
		t.Fatal(err)
	}
	query := pgFileSettingsSelect + " ORDER BY seqno"
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, prepErr := b.DB.PrepareContext(p.Ctx, query)
		if prepErr != nil {
			t.Fatal(prepErr)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(p.Ctx)
	} else {
		rows, err = e.QueryContext(p.Ctx, query)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	pgFileSettingsMetadata(t, rows, true)
	count, witnessed := 0, false
	lines := strings.Split(contents, "\n")
	for rows.Next() {
		var file, name, setting string
		var line, seq int
		var applied bool
		var diagnostic sql.NullString
		if err := rows.Scan(&file, &line, &seq, &name, &setting, &applied, &diagnostic); err != nil {
			t.Fatal(err)
		}
		count++
		if seq != count || line <= 0 || file == "" || name == "" || diagnostic.Valid {
			t.Fatal("real file assignment", file, line, seq, name, setting, diagnostic)
		}
		if file == config && line <= len(lines) {
			assignment := strings.TrimSpace(lines[line-1])
			if strings.HasPrefix(assignment, name) && strings.Contains(assignment, "=") && strings.Contains(assignment, setting) {
				witnessed = true
			}
		}
	}
	if err := rows.Err(); err != nil || count == 0 || !witnessed {
		t.Fatal("actual main-file provenance", count, witnessed, err)
	}
}

func TestReferencePGFileSettingsRows(t *testing.T) {
	pgFileSettingsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(pgFileSettingsSelect+" WHERE FALSE", pgFileSettingsColumns, pgFileSettingsTypes, nil)
			s.Prepared = prepared
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgFileSettingsMetadata(t, rows, b.PG) }
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			pgFileSettingsSource(t, p, b, b.DB, prepared)
			s = fixture.Select("SELECT p.name AS parameter FROM pg_catalog.pg_file_settings p WHERE p.name=$1 ORDER BY p.seqno", []string{"parameter"}, []string{"TEXT"}, nil, "stuntgres_missing_file_parameter")
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			if !b.PG {
				s = fixture.Exec(pgFileSettingsSelect, 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
	})
}

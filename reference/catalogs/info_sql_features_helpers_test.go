package reference_test

import (
	"context"
	"database/sql"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

var sfColumns = []string{"feature_id", "feature_name", "sub_feature_id", "sub_feature_name", "is_supported", "is_verified_by", "comments"}
var sfTypes = []string{"VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR"}

const sfTable = "information_schema.sql_features"
const sfQuery = "SELECT feature_id,feature_name,sub_feature_id,sub_feature_name,is_supported,is_verified_by,comments FROM " + sfTable

// The independent six-field fixture is the untouched release body from
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/catalog/sql_features.txt
// initdb COPY omits is_verified_by; its seventh-column value is SQL NULL.
// Never derive this fixture or expectations from the native producer or SQL output.
func sfRelease(t *testing.T) [][]any {
	t.Helper()
	data, err := os.ReadFile("testdata/sql_features_pg18.tsv")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	rows := make([][]any, 0, len(lines))
	for i, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 6 {
			t.Fatalf("release line %d: %d fields", i+1, len(fields))
		}
		for _, field := range fields {
			if field == `\N` {
				t.Fatalf("release line %d: unexpected NULL marker", i+1)
			}
		}
		rows = append(rows, []any{fields[0], fields[1], fields[2], fields[3], fields[4], nil, fields[5]})
	}
	return rows
}

func sfFilter(rows [][]any, keep func([]any) bool) [][]any {
	var result [][]any
	for _, row := range rows {
		if keep(row) {
			result = append(result, row)
		}
	}
	return result
}

func sfFeature(rows [][]any, id any) [][]any {
	return sfFilter(rows, func(row []any) bool { return row[0] == id })
}

type sfCase struct {
	t       *testing.T
	p       *fixture.Pair
	b       fixture.Backend
	conn    *sql.Conn
	release [][]any
}

func sfScenario(t *testing.T, roles bool, body func(*sfCase)) {
	t.Helper()
	if roles {
		fixture.RoleReferenceCleanup(t, []string{"sf_owner", "sf_reader"})
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				defer conn.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET ROLE"} {
					if _, err := conn.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			body(&sfCase{t: t, p: p, b: b, conn: conn, release: sfRelease(t)})
		})
	}
}

func (c *sfCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}

func (c *sfCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}

func (c *sfCase) hold(q string) fixture.HeldStatement {
	c.t.Helper()
	stmt, err := c.conn.PrepareContext(c.p.Ctx, q)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			c.t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: c.conn, Statement: stmt, SQL: q}
}

func sfMetadata(t *testing.T, columns []*sql.ColumnType, names []string, pg bool) {
	t.Helper()
	if len(columns) != len(names) {
		t.Fatalf("columns=%d want %d", len(columns), len(names))
	}
	for i, col := range columns {
		wantLength := int64(9223372036854775807)
		if pg {
			wantLength = -5
		}
		if names[i] == "is_supported" {
			wantLength = 3
		}
		length, lengthOK := col.Length()
		nullable, nullableOK := col.Nullable()
		precision, scale, decimalOK := col.DecimalSize()
		if col.Name() != names[i] || col.DatabaseTypeName() != "VARCHAR" || col.ScanType() != reflect.TypeFor[string]() || length != wantLength || !lengthOK || nullable != !pg || nullableOK != !pg || precision != 0 || scale != 0 || decimalOK {
			t.Fatalf("column %d: name=%q type=%q scan=%v length=%d/%t nullable=%t/%t decimal=%d/%d/%t", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), length, lengthOK, nullable, nullableOK, precision, scale, decimalOK)
		}
	}
}

func (c *sfCase) projection(q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, sfColumns, sfTypes, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		sfMetadata(t, columns, sfColumns, c.b.PG)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if column == 5 {
			if value != nil {
				t.Fatalf("verification raw=%T(%v), want nil", value, value)
			}
		} else if _, ok := value.(string); !ok {
			t.Fatalf("raw column %d=%T, want string", column, value)
		}
		return true
	}
	return s
}

func (c *sfCase) check(e fixture.Executor, q string, want [][]any, args ...any) {
	c.t.Helper()
	s := c.projection(q, want, args...)
	c.run(e, s)
	c.run(c.conn, fixture.Prepared(s))
}

func (c *sfCase) privateDenied() {
	c.t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic("SELECT * FROM public.sf_private", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table sf_private"))
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		c.run(c.conn, s)
	}
}

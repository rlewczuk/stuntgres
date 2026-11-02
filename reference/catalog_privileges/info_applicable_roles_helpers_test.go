package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const applicableRolesQuery = "SELECT grantee,role_name,is_grantable FROM information_schema.applicable_roles"

var applicableRolesColumns = []string{"grantee", "role_name", "is_grantable"}
var applicableRolesTypes = []string{"NAME", "NAME", "VARCHAR"}

type applicableRolesCase struct {
	*enabledRolesCase
}

func applicableRolesScenario(t *testing.T, roles []string, body func(*applicableRolesCase)) {
	t.Helper()
	// Reuse the role family's notice-enabled pinned session and reset/cleanup order.
	enabledRolesScenario(t, roles, func(s *enabledRolesCase) { body(&applicableRolesCase{s}) })
}

func applicableRolesMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 3 {
		t.Fatalf("columns=%d want 3", len(columns))
	}
	for i, c := range columns {
		scan := reflect.TypeFor[string]()
		if pg && i < 2 {
			scan = reflect.TypeFor[any]()
		}
		wantLength, wantLengthOK := int64(0), false
		if i == 2 {
			wantLength, wantLengthOK = 3, true
		}
		length, lengthOK := c.Length()
		precision, scale, decimalOK := c.DecimalSize()
		nullable, nullableOK := c.Nullable()
		if c.Name() != applicableRolesColumns[i] || c.DatabaseTypeName() != applicableRolesTypes[i] || c.ScanType() != scan || length != wantLength || lengthOK != wantLengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
			t.Fatalf("column %d: name=%s type=%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t; want name=%s type=%s scan=%v length=%d/%t nullable=%t/%t", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK, applicableRolesColumns[i], applicableRolesTypes[i], scan, wantLength, wantLengthOK, !pg, !pg)
		}
	}
}

func applicableRolesRaw(t *testing.T, pg bool, column int, value any) string {
	t.Helper()
	if pg && column < 2 {
		text, ok := value.([]byte)
		if !ok {
			t.Fatalf("raw NAME=%T(%v), want []byte", value, value)
		}
		return string(text)
	}
	text, ok := value.(string)
	if !ok {
		t.Fatalf("raw column %d=%T(%v), want nonNULL string", column, value, value)
	}
	return text
}

func (s *applicableRolesCase) projection(query string, triples ...[3]string) fixture.Step {
	s.t.Helper()
	var want [][]any
	for _, triple := range triples {
		want = append(want, []any{triple[0], triple[1], triple[2]})
	}
	step := fixture.Select(query, applicableRolesColumns, applicableRolesTypes, want)
	step.Bag = true
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		applicableRolesMetadata(t, columns, s.b.PG)
	}
	step.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		text := applicableRolesRaw(t, s.b.PG, column, value)
		for _, triple := range triples {
			if triple[column] == text {
				return true
			}
		}
		t.Fatalf("unexpected raw column %d=%q", column, text)
		return true
	}
	return step
}

func (s *applicableRolesCase) read(triples ...[3]string) {
	s.t.Helper()
	s.readQuery(applicableRolesQuery, triples...)
}

func (s *applicableRolesCase) readQuery(query string, triples ...[3]string) {
	s.t.Helper()
	step := s.projection(query, triples...)
	s.run(step)
	s.run(fixture.Prepared(step))
}

func (s *applicableRolesCase) missing(name string) {
	s.t.Helper()
	d := fixture.ControlsReferenceDiagnostic("42703", `column "`+name+`" does not exist`)
	d.Position = "8"
	for _, prepared := range []bool{false, true} {
		step := fixture.ErrorDiagnostic("SELECT "+name+" FROM information_schema.applicable_roles", d)
		step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
		if prepared {
			step.ErrorStage = "prepare"
		}
		s.run(step)
	}
}

// Consume the actual retained Rows, checking raw values and saved descriptors
// after EOF/Close. Bags retain multiplicities, including NO and YES for one edge.
func (s *applicableRolesCase) consume(rows *sql.Rows, triples ...[3]string) {
	s.t.Helper()
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, applicableRolesColumns) {
		s.t.Fatalf("columns=%v error=%v", names, err)
	}
	columns, err := rows.ColumnTypes()
	if err != nil {
		s.t.Fatal(err)
	}
	applicableRolesMetadata(s.t, columns, s.b.PG)
	got, want := map[[3]string]int{}, map[[3]string]int{}
	for _, triple := range triples {
		want[triple]++
	}
	for rows.Next() {
		var values [3]any
		if err := rows.Scan(&values[0], &values[1], &values[2]); err != nil {
			s.t.Fatal(err)
		}
		var triple [3]string
		for i, value := range values {
			triple[i] = applicableRolesRaw(s.t, s.b.PG, i, value)
		}
		got[triple]++
	}
	if err := rows.Err(); err != nil {
		s.t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		s.t.Fatalf("bag=%v want=%v", got, want)
	}
	if err := rows.Close(); err != nil {
		s.t.Fatal(err)
	}
	applicableRolesMetadata(s.t, columns, s.b.PG)
}

package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

type inetValuer struct{ value driver.Value }

func (v inetValuer) Value() (driver.Value, error) { return v.value, nil }

func inetMetadata(t *testing.T, rows *sql.Rows, pg bool, names, types []string, nullable []bool) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != len(types) {
		t.Fatal(columns)
	}
	for i, c := range columns {
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if c.Name() != names[i] || c.DatabaseTypeName() != types[i] || c.ScanType() != scan {
			t.Fatalf("column %d: name=%s type=%s scan=%v", i, c.Name(), c.DatabaseTypeName(), c.ScanType())
		}
		if _, ok := c.Length(); ok {
			t.Fatal("network length available")
		}
		if _, _, ok := c.DecimalSize(); ok {
			t.Fatal("network precision available")
		}
		n, known := c.Nullable()
		// Computed projections retain the fixture's existing unknown-nullability
		// contract; declared columns are checked independently below.
		if pg || nullable == nil {
			if n || known {
				t.Fatal(n, known)
			}
		} else if !known || n != nullable[i] {
			t.Fatal(n, known, nullable[i])
		}
	}
}

func TestReferenceINETBindsScansAndMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE n(h INET,c CIDR)", 0))
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			q := "INSERT INTO n VALUES($1,$2)"
			for _, prepared := range []bool{false, true} {
				for _, args := range [][]any{{"192.168.1.5", "10"}, {[]byte("::1"), []byte("2001:db8::/113")}, {nil, nil}, {inetValuer{"1.2.3.4/24"}, inetValuer{nil}}, {[]byte(nil), []byte(nil)}} {
					s := fixture.Exec(q, 1, args...)
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM n", 10))
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			source := []byte("192.168.0.1/24")
			for _, args := range [][]any{{source, "10"}, {nil, nil}, {"::1", "::1"}} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, args...))
			}
			source[0] = '0'
			d := inetDiagnostic("inet", "", false)
			d.Context = "unnamed portal parameter $1 = ''"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d, []byte{}, "10"))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, "1.2.3.4", "10"))
			predicate := "SELECT h FROM n WHERE h=$1 OR h=$1"
			filter, err := b.DB.PrepareContext(p.Ctx, predicate)
			if err != nil {
				t.Fatal(err)
			}
			defer filter.Close()
			reuse := fixture.HeldStatement{Executor: b.DB, Statement: filter, SQL: predicate}
			for _, frame := range []struct {
				arg  any
				rows [][]any
			}{{"192.168.0.1/24", [][]any{{"192.168.0.1/24"}}}, {nil, nil}, {"::1", [][]any{{"::1"}}}} {
				fixture.RunStep(t, p.Ctx, reuse, b.Recorder, fixture.Select(predicate, []string{"h"}, []string{"INET"}, frame.rows, frame.arg))
			}
			for _, input := range []any{[16]byte{}, struct{}{}} {
				_, err := stmt.ExecContext(p.Ctx, input, "10")
				var state interface{ SQLState() string }
				if err == nil || errors.As(err, &state) {
					t.Fatal("ordinary argument conversion expected", err)
				}
			}
			for _, tc := range []struct {
				q        string
				rows     [][]any
				nullable []bool
			}{
				{"SELECT h,c FROM n ORDER BY h", [][]any{{"1.2.3.4", "10.0.0.0/8"}, {"192.168.0.1/24", "10.0.0.0/8"}, {"::1", "::1/128"}, {nil, nil}}, []bool{true, true}},
				{"SELECT h,c FROM n WHERE h=NULL", nil, []bool{true, true}},
				{"SELECT h,c FROM n WHERE c IS NULL", [][]any{{nil, nil}}, []bool{true, true}},
				{"SELECT CAST(NULL AS inet) AS h,NULL::cidr AS c", [][]any{{nil, nil}}, nil},
			} {
				s := fixture.Select(tc.q, []string{"h", "c"}, []string{"INET", "CIDR"}, tc.rows)
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
					inetMetadata(t, r, b.PG, []string{"h", "c"}, []string{"INET", "CIDR"}, tc.nullable)
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
			}
			for _, column := range []string{"h", "c"} {
				expected := "192.168.0.1/24"
				if column == "c" {
					expected = "10.0.0.0/8"
				}
				query := "SELECT " + column + " FROM n WHERE h='192.168.0.1/24'"
				var text string
				var buf []byte
				var nullable sql.NullString
				if err := b.DB.QueryRowContext(p.Ctx, query).Scan(&text); err != nil || text != expected {
					t.Fatal(text, err)
				}
				if err := b.DB.QueryRowContext(p.Ctx, query).Scan(&buf); err != nil || string(buf) != expected {
					t.Fatal(buf, err)
				}
				buf[0] = 'x'
				if err := b.DB.QueryRowContext(p.Ctx, query).Scan(&nullable); err != nil || !nullable.Valid || nullable.String != expected {
					t.Fatal(nullable, err)
				}
				if err := b.DB.QueryRowContext(p.Ctx, "SELECT "+column+" FROM n WHERE h IS NULL").Scan(&nullable); err != nil || nullable.Valid {
					t.Fatal(nullable, err)
				}
				err := b.DB.QueryRowContext(p.Ctx, "SELECT "+column+" FROM n WHERE h IS NULL").Scan(&text)
				var state interface{ SQLState() string }
				if err == nil || errors.As(err, &state) {
					t.Fatal("ordinary NULL scan error", err)
				}
			}
		})
	}
	p.Step(t, fixture.Exec("CREATE TABLE required(h INET NOT NULL,c CIDR NOT NULL)", 0))
	for _, b := range p.Backends() {
		s := fixture.Select("SELECT h,c FROM required", []string{"h", "c"}, []string{"INET", "CIDR"}, nil)
		s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
			inetMetadata(t, r, b.PG, []string{"h", "c"}, []string{"INET", "CIDR"}, []bool{false, false})
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

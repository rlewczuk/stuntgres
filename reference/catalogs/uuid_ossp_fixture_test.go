package reference_test

import (
	"context"
	"database/sql"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// uuid-ossp is database-global; public-schema reset alone cannot isolate it.
func uuidOsspCase(t *testing.T, install bool, body func(*testing.T, *fixture.Pair, *sql.Conn, *fixture.Recorder, bool)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"uuid_ossp_owner"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS \"uuid-ossp\" CASCADE", "DROP SCHEMA IF EXISTS uuid_ossp_part CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
					var count int
					if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_roles WHERE rolname='uuid_ossp_owner'").Scan(&count); err != nil {
						t.Error(err)
					} else if count != 0 {
						// Database CREATE and schema ACL dependencies survive schema
						// reset. Release them after provider/schema teardown, while
						// still using the restored administrative physical owner.
						for _, q := range []string{"DROP OWNED BY uuid_ossp_owner", "DROP ROLE uuid_ossp_owner"} {
							if _, err := c.ExecContext(p.Ctx, q); err != nil {
								t.Error(q, err)
							}
						}
					}
				}
				clean()
				defer clean()
			}
			if install {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION \"uuid-ossp\"", 0))
			}
			body(t, p, c, b.Recorder, b.PG)
		})
	}
}

func uuidOsspDecode(t *testing.T, s string) []byte {
	t.Helper()
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' || strings.ToLower(s) != s {
		t.Fatalf("noncanonical UUID %q", s)
	}
	b, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err != nil || len(b) != 16 {
		t.Fatalf("UUID %q: %v", s, err)
	}
	if b[6]>>4 != 4 || b[8]>>6 != 2 {
		t.Fatalf("not RFC variant v4: %q", s)
	}
	return b
}

func uuidOsspColumns(t *testing.T, rows *sql.Rows, pg bool, count int, declaredNullable bool) {
	t.Helper()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != count {
		t.Fatalf("columns=%d want=%d", len(cols), count)
	}
	for _, col := range cols {
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if col.Name() != "u" || col.DatabaseTypeName() != "UUID" || col.ScanType() != scan {
			t.Fatalf("UUID metadata %s/%s/%v", col.Name(), col.DatabaseTypeName(), col.ScanType())
		}
		if _, ok := col.Length(); ok {
			t.Fatal("UUID length known")
		}
		if _, _, ok := col.DecimalSize(); ok {
			t.Fatal("UUID precision known")
		}
		wantNullable := declaredNullable && !pg
		if n, known := col.Nullable(); n != wantNullable || known != wantNullable {
			t.Fatalf("UUID nullability=%v,%v want=%v,%v", n, known, wantNullable, wantNullable)
		}
	}
}

// Check transport ownership before normalization, and independently decode each cell.
func uuidOsspRead(t *testing.T, p *fixture.Pair, e fixture.Executor, r *fixture.Recorder, pg bool, query string, count, width int, declaredNullable ...bool) []string {
	t.Helper()
	rows, err := e.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, r), query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	// Direct expressions have no nullability proof. A selected nullable view
	// column has native declaration metadata; pq exposes neither proof.
	uuidOsspColumns(t, rows, pg, width, len(declaredNullable) != 0 && declaredNullable[0])
	var result []string
	n := 0
	for rows.Next() {
		n++
		values := make([]any, width)
		ptrs := make([]any, width)
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for _, v := range values {
			var s string
			if pg {
				b, ok := v.([]byte)
				if !ok {
					t.Fatalf("pq UUID raw %T", v)
				}
				s = string(b)
			} else {
				var ok bool
				s, ok = v.(string)
				if !ok {
					t.Fatalf("native UUID raw %T", v)
				}
			}
			uuidOsspDecode(t, s)
			result = append(result, s)
		}
	}
	if rows.Err() != nil || n != count {
		t.Fatalf("rows=%d want=%d err=%v", n, count, rows.Err())
	}
	return result
}

func uuidOsspMissing(signature string, position int) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("42883", "function "+signature+" does not exist")
	d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	d.Position = fixture.Position(position)
	return d
}

func uuidOsspFailure(t *testing.T, p *fixture.Pair, c *sql.Conn, query string, d fixture.Diagnostic, args ...any) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, query, args...)
	if rows != nil {
		rows.Close()
		t.Fatal("error published rows")
	}
	fixture.AssertDiagnostic(t, err, &d)
	stmt, err := c.PrepareContext(p.Ctx, query)
	if stmt != nil {
		stmt.Close()
		t.Fatal("error published statement")
	}
	fixture.AssertDiagnostic(t, err, &d)
}

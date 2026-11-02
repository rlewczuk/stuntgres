package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const pkeyCall = "SELECT * FROM pk_ext.dblink_get_pkey($1::text) ORDER BY position"

// Expectations in this family come from the PostgreSQL 18 dblink_get_pkey
// manual and REL_18_6 contrib/dblink/{dblink.c,dblink--1.2.sql}, not native output.
func pkeyCase(t *testing.T, install bool, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	for _, name := range []string{"pk_ext", "pk_left", "pk_right", "pk_hidden", "pk_quoted"} {
		fixture.SchemaReferenceCleanup(t, name)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			})
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS dblink CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				t.Cleanup(clean)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SCHEMA pk_ext", 0))
			if install {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION dblink SCHEMA pk_ext", 0))
			}
			body(t, p, b, c)
		})
	}
}

func pkeyRows(rows [][]any, arg any) fixture.Step {
	return fixture.Select(pkeyCall, []string{"position", "colname"}, []string{"INT4", "TEXT"}, rows, arg)
}

func pkeyError(query, code, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: code, Message: message}, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func pkeyHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) fixture.HeldStatement {
	t.Helper()
	st, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal("prepare held pkey call", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: c, Statement: st, SQL: query}
}

// Separate route subtests let oracle discovery report all complete diagnostics,
// even if one source-derived candidate differs from the pinned release.
func pkeyRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held *fixture.HeldStatement, s fixture.Step) {
	t.Helper()
	t.Run("direct", func(t *testing.T) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	})
	t.Run("prepared", func(t *testing.T) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	})
	if held != nil {
		t.Run("held", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, *held, b.Recorder, s)
		})
	}
}

func pkeyMetadata(pg bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		meta, err := rows.ColumnTypes()
		if err != nil || len(meta) != 2 {
			t.Fatal("pkey declared descriptor", meta, err)
		}
		intScan := reflect.TypeFor[int64]()
		if pg {
			intScan = reflect.TypeFor[int32]()
		}
		for i, scan := range []reflect.Type{intScan, reflect.TypeFor[string]()} {
			if meta[i].ScanType() != scan {
				t.Errorf("column %d ScanType %v want %v", i, meta[i].ScanType(), scan)
			}
			nullable, known := meta[i].Nullable()
			// The installed composite declares nullable fields; pq does not
			// expose declaration nullability, including on an empty cursor.
			if nullable != !pg || known != !pg {
				t.Errorf("column %d Nullable=%v/%v want %v/%v", i, nullable, known, !pg, !pg)
			}
			if _, _, known := meta[i].DecimalSize(); known {
				t.Errorf("column %d unexpectedly has decimal metadata", i)
			}
		}
	}
}

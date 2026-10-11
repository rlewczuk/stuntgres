package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAttrdefEmptyMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder,
				fixture.Exec("CREATE TABLE attrdef_empty_metadata(v INT)", 0))

			// Scope PostgreSQL emptiness to the scenario owner: bootstrap and
			// unrelated relations may legitimately have stored defaults.
			var owner int64
			if err := conn.QueryRowContext(p.Ctx,
				"SELECT oid FROM pg_catalog.pg_class WHERE relname='attrdef_empty_metadata'",
			).Scan(&owner); err != nil {
				t.Fatal(err)
			}
			if owner <= 0 || owner > 4294967295 {
				t.Fatalf("invalid owning relation OID: %d", owner)
			}

			names := []string{"oid", "adrelid", "adnum", "adbin"}
			types := []string{"OID", "OID", "INT2", "PG_NODE_TREE"}
			check := func(t *testing.T, columns []*sql.ColumnType) {
				t.Helper()
				fixture.TableDDLColumnTypes(t, backend.PG, columns, names, types,
					[]bool{false, false, false, false})
				for i, column := range columns {
					if length, known := column.Length(); length != 0 || known {
						t.Fatalf("column %d Length=(%d,%t), want (0,false)", i, length, known)
					}
					if precision, scale, known := column.DecimalSize(); precision != 0 || scale != 0 || known {
						t.Fatalf("column %d DecimalSize=(%d,%d,%t), want (0,0,false)", i, precision, scale, known)
					}
				}
			}

			queries := []struct {
				name  string
				query string
				args  []any
			}{
				{"named", "SELECT oid,adrelid,adnum,adbin FROM pg_catalog.pg_attrdef WHERE adrelid=$1 ORDER BY adnum", []any{owner}},
				{"star", "SELECT * FROM pg_catalog.pg_attrdef WHERE adrelid=$1 ORDER BY adnum", []any{owner}},
			}
			if !backend.PG {
				// A fresh native engine has no bootstrap defaults. This extra
				// witness is not an assertion about PostgreSQL's global catalog.
				queries = append(queries, struct {
					name  string
					query string
					args  []any
				}{"native_catalog", "SELECT * FROM pg_catalog.pg_attrdef ORDER BY adnum", nil})
			}
			for _, query := range queries {
				for _, prepared := range []bool{false, true} {
					mode := "direct"
					if prepared {
						mode = "prepared"
					}
					t.Run(query.name+"/"+mode, func(t *testing.T) {
						var retained []*sql.ColumnType
						s := fixture.Select(query.query, names, types, nil, query.args...)
						s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
							var err error
							retained, err = rows.ColumnTypes()
							if err != nil {
								t.Fatal(err)
							}
							// The fixture invokes this on the actual cursor before
							// Next, even though no row can supply inferred types.
							check(t, retained)
						}
						if prepared {
							s = fixture.Prepared(s)
						}
						// The runner asserts zero rows, successful EOF/Close and
						// middleware-recorded metadata/closure independently.
						fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
						// Inspect saved descriptors, not ColumnTypes on closed rows.
						check(t, retained)
					})
				}
			}
		})
	}
}

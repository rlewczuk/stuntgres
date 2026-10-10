package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceAttrdefEmptyMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			referenceRunStep(t, p.ctx, conn, backend.recorder,
				referenceExec("CREATE TABLE attrdef_empty_metadata(v INT)", 0))

			// Scope PostgreSQL emptiness to the scenario owner: bootstrap and
			// unrelated relations may legitimately have stored defaults.
			var owner int64
			if err := conn.QueryRowContext(p.ctx,
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
				referenceTableDDLColumnTypes(t, backend.pg, columns, names, types,
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
			if !backend.pg {
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
						s := referenceSelect(query.query, names, types, nil, query.args...)
						s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
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
							s = referencePrepared(s)
						}
						// The runner asserts zero rows, successful EOF/Close and
						// middleware-recorded metadata/closure independently.
						referenceRunStep(t, p.ctx, conn, backend.recorder, s)
						// Inspect saved descriptors, not ColumnTypes on closed rows.
						check(t, retained)
					})
				}
			}
		})
	}
}

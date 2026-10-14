package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGConstraintShape(t *testing.T) {
	pgConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pc_shape(a INT CONSTRAINT pc_shape_u UNIQUE)")
		for _, q := range []string{"SELECT * FROM pg_catalog.pg_constraint WHERE FALSE", "SELECT * FROM pg_constraint WHERE conrelid='pc_shape'::regclass::oid"} {
			check := func(rows *sql.Rows) {
				defer rows.Close()
				pgConstraintMetadata(t, rows, b.PG)
				count := 0
				for rows.Next() {
					values := make([]any, 28)
					targets := make([]any, 28)
					for i := range values {
						targets[i] = &values[i]
					}
					if err := rows.Scan(targets...); err != nil {
						t.Fatal(err)
					}
					for i, value := range values {
						if i < 20 && value == nil {
							t.Fatal("NULL fixed field", i)
						}
						if i >= 21 && value != nil {
							t.Fatal("UNIQUE inapplicable tail", i, value)
						}
						switch pgConstraintTypes[i] {
						case "NAME", "OID", "_INT2":
							if value != nil {
								if b.PG {
									if _, ok := value.([]byte); !ok {
										t.Fatalf("raw %d got %T, want bytes", i, value)
									}
								} else {
									switch pgConstraintTypes[i] {
									case "OID":
										if _, ok := value.(int64); !ok {
											t.Fatalf("raw OID %d got %T", i, value)
										}
									default:
										if _, ok := value.(string); !ok {
											t.Fatalf("raw string %d got %T", i, value)
										}
									}
								}
							}
						case "CHAR":
							if text, ok := value.(string); !ok || len(text) != 1 {
								t.Fatalf("raw CHAR %d got %T %#v, want one-byte string", i, value, value)
							}
						case "INT2":
							if _, ok := value.(int64); !ok {
								t.Fatalf("raw INT2 %d got %T", i, value)
							}
						case "BOOL":
							if _, ok := value.(bool); !ok {
								t.Fatalf("raw bool %d got %T", i, value)
							}
						}
					}
					count++
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				want := 1
				if q == "SELECT * FROM pg_catalog.pg_constraint WHERE FALSE" {
					want = 0
				}
				if count != want {
					t.Fatal("count", count, want)
				}
			}
			for _, plain := range []bool{false, true} {
				var rows *sql.Rows
				var err error
				if plain {
					rows, err = b.DB.Query(q)
				} else {
					rows, err = b.DB.QueryContext(p.Ctx, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				check(rows)
				var stmt *sql.Stmt
				if plain {
					stmt, err = b.DB.Prepare(q)
				} else {
					stmt, err = b.DB.PrepareContext(p.Ctx, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				if plain {
					rows, err = stmt.Query()
				} else {
					rows, err = stmt.QueryContext(p.Ctx)
				}
				if err != nil {
					stmt.Close()
					t.Fatal(err)
				}
				check(rows)
				stmt.Close()
			}
			held := fixture.ViewHold(t, p, b, q)
			for range 2 {
				rows, err := held.Statement.QueryContext(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				check(rows)
			}
		}
		pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT relnatts FROM pg_class WHERE oid='pg_constraint'::regclass::oid", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(28)}}))
		// pg_attribute is an adjacent catalog, not a native provider for this
		// part. Native ordinals/origins/nullability are independently asserted by
		// TestPGConstraintShapeAndOwnership; the complete driver shape above is shared.
		if b.PG {
			want := make([][]any, 28)
			for i := range want {
				want[i] = []any{int64(i + 1), pgConstraintColumns[i], i < 20}
			}
			pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT attnum,attname,attnotnull FROM pg_attribute WHERE attrelid='pg_constraint'::regclass::oid AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "attnotnull"}, []string{"INT2", "NAME", "BOOL"}, want))
		}
	})
}

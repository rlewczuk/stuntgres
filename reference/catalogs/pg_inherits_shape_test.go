package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGInheritsShape(t *testing.T) {
	pgInheritsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pi_shape_p(a INT)", "CREATE TABLE pi_shape_c() INHERITS(pi_shape_p)")
		parent := pgInheritsID(t, p, b.DB, "pi_shape_p")
		child := pgInheritsID(t, p, b.DB, "pi_shape_c")
		for _, populated := range []bool{false, true} {
			q := "SELECT * FROM pg_catalog.pg_inherits WHERE FALSE"
			want := [][]any(nil)
			if populated {
				q = "SELECT * FROM pg_catalog.pg_inherits WHERE inhrelid=$1::oid"
				want = [][]any{pgInheritsEdge(child, parent, 1)}
			}
			args := []any(nil)
			if populated {
				args = []any{child}
			}
			check := func(rows *sql.Rows) {
				defer rows.Close()
				pgInheritsMetadata(t, rows, b.PG)
				got := make([][]any, 0)
				for rows.Next() {
					var a, z, seq, pending any
					if err := rows.Scan(&a, &z, &seq, &pending); err != nil {
						t.Fatal(err)
					}
					got = append(got, []any{fixture.OidReferenceValue(t, b.PG, "OID", a), fixture.OidReferenceValue(t, b.PG, "OID", z), fixture.OidReferenceValue(t, b.PG, "INT4", seq), fixture.OidReferenceValue(t, b.PG, "BOOL", pending)})
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if _, ok := fixture.CompareRows(got, want, false); !ok {
					t.Fatal(got, want)
				}
			}
			for _, plain := range []bool{false, true} {
				var rows *sql.Rows
				var stmt *sql.Stmt
				var err error
				if plain {
					rows, err = b.DB.Query(q, args...)
				} else {
					rows, err = b.DB.QueryContext(p.Ctx, q, args...)
				}
				if err != nil {
					t.Fatal(err)
				}
				check(rows)
				if plain {
					stmt, err = b.DB.Prepare(q)
				} else {
					stmt, err = b.DB.PrepareContext(p.Ctx, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				if plain {
					rows, err = stmt.Query(args...)
				} else {
					rows, err = stmt.QueryContext(p.Ctx, args...)
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
				rows, err := held.Statement.QueryContext(p.Ctx, args...)
				if err != nil {
					t.Fatal(err)
				}
				check(rows)
			}
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT relnatts,relhasindex FROM pg_catalog.pg_class WHERE oid=2611::oid", []string{"relnatts", "relhasindex"}, []string{"INT2", "BOOL"}, [][]any{{int64(4), true}}))
		fixture.OidReferenceRows(t, p.Ctx, b, b.DB, fixture.Select("SELECT attnum,attname,atttypid,attnotnull FROM pg_catalog.pg_attribute WHERE attrelid=2611::oid AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull"}, []string{"INT2", "NAME", "OID", "BOOL"}, [][]any{{int64(1), "inhrelid", int64(26), true}, {int64(2), "inhparent", int64(26), true}, {int64(3), "inhseqno", int64(23), true}, {int64(4), "inhdetachpending", int64(16), true}}))
	})
}

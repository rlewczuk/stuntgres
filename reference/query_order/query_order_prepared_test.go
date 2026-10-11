//go:build reference

package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderPreparedFrames(t *testing.T) {
	p := fixture.NewPair(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,a INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,2),(2,1),(3,NULL),(4,2)", 4))
	for _, backend := range []struct {
		name string
		db   *sql.DB
	}{{"memory", p.Memory}, {"postgres", p.Postgres}} {
		t.Run(backend.name, func(t *testing.T) {
			stmt, err := backend.db.PrepareContext(p.Ctx, "SELECT id FROM t ORDER BY a*$1 NULLS LAST,id")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for i, arg := range []any{1, -1, nil, 1} {
				want := []int64{2, 1, 4, 3}
				if i == 1 {
					want = []int64{1, 4, 2, 3}
				}
				if i == 2 {
					want = []int64{1, 2, 3, 4}
				}
				rows, err := stmt.QueryContext(p.Ctx, arg)
				if err != nil {
					t.Fatal(err)
				}
				names, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(names, []string{"id"}) {
					rows.Close()
					t.Fatal(names, err)
				}
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
					rows.Close()
					t.Fatal(types, err)
				}
				got := []int64{}
				for rows.Next() {
					var id int64
					if err := rows.Scan(&id); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					got = append(got, id)
				}
				err = rows.Err()
				rows.Close()
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatal(got, want, err)
				}
			}
		})
	}
}

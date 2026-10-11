package reference_test

import (
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryJoinsExportAndSchemaOwnership(t *testing.T) {
	p := queryJoinsReferenceSetup(t)
	p.Step(t, fixture.Exec("CREATE TABLE bytes(k INT,v BYTEA)", 0))
	p.Step(t, fixture.Exec("INSERT INTO bytes VALUES(1,'\\x0102')", 1))
	step := fixture.Select("SELECT bytes.v FROM bytes JOIN c USING(k)", []string{"v"}, []string{"BYTEA"}, [][]any{{string([]byte{1, 2})}, {string([]byte{1, 2})}})
	step.Bag = true
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
	for _, backend := range p.Backends() {
		func() {
			rows, err := backend.DB.QueryContext(p.Ctx, step.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			names, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(names, []string{"v"}) {
				t.Fatal(names, err)
			}
			if !backend.PG {
				// Native exports detached names; lib/pq may return a borrowed slice.
				names[0] = "changed"
				names, err = rows.Columns()
				if err != nil || names[0] != "v" {
					t.Fatal("schema export not detached", names, err)
				}
			}
			meta, err := rows.ColumnTypes()
			if err != nil || len(meta) != 1 || meta[0].Name() != "v" || meta[0].DatabaseTypeName() != "BYTEA" {
				t.Fatal(meta, err)
			}
			var first, second []byte
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			if err := rows.Scan(&first); err != nil {
				t.Fatal(err)
			}
			if !backend.PG {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
			}
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			if err := rows.Scan(&second); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(first, []byte{1, 2}) || !reflect.DeepEqual(second, []byte{1, 2}) {
				t.Fatal(first, second)
			}
			first[0] = 99
			if second[0] != 1 {
				t.Fatal("exports alias", first, second)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("extra joined row", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if first[0] != 99 || first[1] != 2 || second[0] != 1 || second[1] != 2 || meta[0].Name() != "v" {
				t.Fatal("close invalidated saved values/metadata", first, second, meta)
			}
		}()
	}
}

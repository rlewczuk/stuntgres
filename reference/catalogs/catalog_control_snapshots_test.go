package reference_test

import (
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceControlSnapshotSurvivesCatalogReplacement(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		for _, restore := range []bool{false, true} {
			t.Run(map[bool]string{false: "recorded/", true: "source/"}[nonContext]+map[bool]string{false: "reset", true: "restore"}[restore], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						db := backend.DB
						run := func(s fixture.Step) {
							s.NonContext = nonContext
							fixture.RunStep(t, p.Ctx, db, backend.Recorder, s)
						}
						run(fixture.Exec("CREATE TABLE public.snap(v INT)", 0))
						run(fixture.Exec("INSERT INTO public.snap VALUES(1),(2)", 2))
						rows, err := db.Query("SELECT v FROM public.snap ORDER BY v")
						if err != nil {
							t.Fatal(err)
						}
						defer rows.Close()
						run(fixture.Exec("INSERT INTO public.snap VALUES(3)", 1))
						if !backend.PG {
							if restore {
								err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE public.snap(replacement TEXT); INSERT INTO public.snap VALUES('99');"))
							} else {
								err = stuntgres.Reset(p.Ctx, p.Native)
							}
							if err != nil {
								t.Fatal(err)
							}
						}
						names, err := rows.Columns()
						if err != nil || len(names) != 1 || names[0] != "v" {
							t.Fatal("retained column names", names, err)
						}
						types, err := rows.ColumnTypes()
						if err != nil || len(types) != 1 || types[0].Name() != "v" || types[0].DatabaseTypeName() != "INT4" {
							t.Fatal("retained column types", types, err)
						}
						for _, want := range []int64{1, 2} {
							if !rows.Next() {
								t.Fatal("missing snapshot row", rows.Err())
							}
							var got int64
							if err = rows.Scan(&got); err != nil {
								t.Fatal(err)
							}
							if got != want {
								t.Fatalf("got %d want %d", got, want)
							}
						}
						if rows.Next() {
							t.Fatal("snapshot saw later insert")
						}
						if err = rows.Err(); err != nil {
							t.Fatal(err)
						}
						if err = rows.Close(); err != nil {
							t.Fatal(err)
						}
						if backend.PG {
							// Real SQL snapshot witness; PostgreSQL has no host replacement API.
							run(fixture.Select("SELECT v FROM snap ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
						} else if restore {
							run(fixture.Select("SELECT replacement FROM public.snap", []string{"replacement"}, []string{"TEXT"}, [][]any{{"99"}}))
						} else {
							missing := fixture.Error("SELECT v FROM snap", "42P01")
							missing.SelectRows = nonContext
							run(missing)
						}
					})
				}
			})
		}
	}
}

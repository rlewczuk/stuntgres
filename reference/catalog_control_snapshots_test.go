package reference_test

import (
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceControlSnapshotSurvivesCatalogReplacement(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		for _, restore := range []bool{false, true} {
			t.Run(map[bool]string{false: "recorded/", true: "source/"}[nonContext]+map[bool]string{false: "reset", true: "restore"}[restore], func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						db := backend.db
						run := func(s referenceStep) {
							s.nonContext = nonContext
							referenceRunStep(t, p.ctx, db, backend.recorder, s)
						}
						run(referenceExec("CREATE TABLE public.snap(v INT)", 0))
						run(referenceExec("INSERT INTO public.snap VALUES(1),(2)", 2))
						rows, err := db.Query("SELECT v FROM public.snap ORDER BY v")
						if err != nil {
							t.Fatal(err)
						}
						defer rows.Close()
						run(referenceExec("INSERT INTO public.snap VALUES(3)", 1))
						if !backend.pg {
							if restore {
								err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE public.snap(replacement TEXT); INSERT INTO public.snap VALUES('99');"))
							} else {
								err = stuntgres.Reset(p.ctx, p.native)
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
						if backend.pg {
							// Real SQL snapshot witness; PostgreSQL has no host replacement API.
							run(referenceSelect("SELECT v FROM snap ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
						} else if restore {
							run(referenceSelect("SELECT replacement FROM public.snap", []string{"replacement"}, []string{"TEXT"}, [][]any{{"99"}}))
						} else {
							missing := referenceError("SELECT v FROM snap", "42P01")
							missing.selectRows = nonContext
							run(missing)
						}
					})
				}
			})
		}
	}
}

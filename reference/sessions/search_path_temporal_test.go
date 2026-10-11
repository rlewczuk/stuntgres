package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSearchPathQualifiedTemporalDeclarations(t *testing.T) {
	for _, sourceSetup := range []bool{false, true} {
		t.Run(map[bool]string{false: "recorded", true: "source setup"}[sourceSetup], func(t *testing.T) {
			for _, tc := range []struct {
				name, typ, input, databaseType string
				want                           any
			}{
				{"time", "pg_catalog.time(3)", "12:34:56.123789", "TIME", "12:34:56.124"},
				{"timestamp", "pg_catalog.timestamp(3)", "2000-01-01 12:34:56.123789", "TIMESTAMP", time.Date(2000, 1, 1, 12, 34, 56, 124000000, time.UTC)},
				{"time-quoted", `pg_catalog."time"(3)`, "12:34:56.123789", "TIME", "12:34:56.124"},
				{"timestamp-quoted", `pg_catalog."timestamp"(3)`, "2000-01-01 12:34:56.123789", "TIMESTAMP", time.Date(2000, 1, 1, 12, 34, 56, 124000000, time.UTC)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE public.temporal(v "+tc.typ+")", 0), fixture.Exec("INSERT INTO public.temporal VALUES('"+tc.input+"')", 1)} {
						s.NonContext = sourceSetup
						p.Step(t, s)
					}
					for _, backend := range p.Backends() {
						t.Run(backend.Name, func(t *testing.T) {
							c, err := backend.DB.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer c.Close()
							const q = "SELECT v FROM public.temporal"
							held, err := c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer held.Close()
							check := func(rows *sql.Rows) {
								t.Helper()
								defer rows.Close()
								cols, err := rows.Columns()
								if err != nil || !reflect.DeepEqual(cols, []string{"v"}) {
									t.Fatal(cols, err)
								}
								metadata, err := rows.ColumnTypes()
								scan := reflect.TypeOf(tc.want)
								if backend.PG && tc.databaseType == "TIME" {
									scan = reflect.TypeFor[time.Time]()
								}
								if err != nil || len(metadata) != 1 || metadata[0].DatabaseTypeName() != tc.databaseType || metadata[0].ScanType() != scan {
									t.Fatal(metadata, scan, err)
								}
								if !rows.Next() {
									t.Fatal(rows.Err())
								}
								var raw any
								if err := rows.Scan(&raw); err != nil {
									t.Fatal(err)
								}
								if reflect.TypeOf(raw) != scan {
									t.Fatal("raw temporal codec", raw, scan)
								}
								want := fixture.TimeRows(t, backend.PG, []string{tc.databaseType}, [][]any{{tc.want}})[0][0]
								if !reflect.DeepEqual(fixture.Normalize(raw), want) {
									t.Fatal(raw, want)
								}
								if rows.Next() || rows.Err() != nil {
									t.Fatal(rows.Err())
								}
								if err := rows.Close(); err != nil {
									t.Fatal(err)
								}
							}
							for _, path := range []string{"public", "pg_catalog"} {
								fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Exec("SET search_path TO "+path, 0))
								rows, err := c.QueryContext(p.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								check(rows)
								rows, err = held.QueryContext(p.Ctx)
								if err != nil {
									t.Fatal(err)
								}
								check(rows)
							}
						})
					}
				})
			}
		})
	}
}

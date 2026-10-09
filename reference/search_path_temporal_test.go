package reference_test

import (
	"database/sql"
	"reflect"
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
					p := newReferenceScenario(t)
					for _, s := range []referenceStep{referenceExec("CREATE TABLE public.temporal(v "+tc.typ+")", 0), referenceExec("INSERT INTO public.temporal VALUES('"+tc.input+"')", 1)} {
						s.nonContext = sourceSetup
						p.step(t, s)
					}
					for _, backend := range p.backends() {
						t.Run(backend.name, func(t *testing.T) {
							c, err := backend.db.Conn(p.ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer c.Close()
							const q = "SELECT v FROM public.temporal"
							held, err := c.PrepareContext(p.ctx, q)
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
								if backend.pg && tc.databaseType == "TIME" {
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
								want := referenceTimeRows(t, backend.pg, []string{tc.databaseType}, [][]any{{tc.want}})[0][0]
								if !reflect.DeepEqual(referenceNormalize(raw), want) {
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
								referenceRunStep(t, p.ctx, c, backend.recorder, referenceExec("SET search_path TO "+path, 0))
								rows, err := c.QueryContext(p.ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								check(rows)
								rows, err = held.QueryContext(p.ctx)
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

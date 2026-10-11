package reference_test

import (
	"context"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceAnyValueMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(v INTEGER NOT NULL,s VARCHAR(4) COLLATE \"C\",c CHAR(4),n NUMERIC(10,4),tm TIME(3),ts TIMESTAMP(3))", 0))
			run(fixture.Exec("INSERT INTO t VALUES(41,'xy','x',1.2300,'12:34:56.123','2000-02-29 12:34:56.123')", 1))
			run(fixture.Exec("CREATE VIEW av_metadata AS SELECT any_value(s) AS s,any_value(c) AS c,any_value(n) AS n,any_value(tm) AS tm,any_value(ts) AS ts FROM t", 0))
			for _, q := range []string{"SELECT any_value(v) FROM t", "SELECT any_value(v) FROM t WHERE FALSE"} {
				for _, prepared := range []bool{false, true} {
					ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
					var executor fixture.Executor = backend.DB
					if prepared {
						stmt, err := backend.DB.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = fixture.HeldStatement{backend.DB, stmt, q}
					}
					rows, err := executor.QueryContext(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					metadata, err := rows.ColumnTypes()
					if err != nil || len(metadata) != 1 {
						t.Fatal(metadata, err)
					}
					c := metadata[0]
					nullable, known := c.Nullable()
					wantScan := reflect.TypeFor[int64]()
					if backend.PG {
						wantScan = reflect.TypeFor[int32]()
					}
					if c.Name() != "any_value" || c.DatabaseTypeName() != "INT4" || c.ScanType() != wantScan {
						t.Fatalf("metadata %v", c)
					}
					if backend.PG {
						if nullable || known {
							t.Fatalf("pq nullable %v/%v", nullable, known)
						}
					} else if !nullable || !known {
						t.Fatalf("native nullable %v/%v", nullable, known)
					}
					want := any(int64(41))
					if q == "SELECT any_value(v) FROM t WHERE FALSE" {
						want = nil
					}
					if got := fixture.AnyValueConsume(t, rows, backend.Recorder, q, []string{"any_value"}, []string{"INT4"}); !reflect.DeepEqual(got, [][]any{{want}}) {
						t.Fatal(got)
					}
				}
			}
			var clock any = "12:34:56.123"
			if backend.PG {
				clock = time.Date(0, 1, 1, 12, 34, 56, 123000000, time.UTC)
			}
			timestamp := time.Date(2000, 2, 29, 12, 34, 56, 123000000, time.UTC)
			for _, tc := range []struct {
				q              string
				columns, types []string
				row            []any
			}{
				{"SELECT any_value(s),any_value(c),any_value(n),any_value(tm),any_value(ts) FROM t", []string{"any_value", "any_value", "any_value", "any_value", "any_value"}, []string{"VARCHAR", "BPCHAR", "NUMERIC", "TIME", "TIMESTAMP"}, []any{"xy", "x   ", "1.2300", clock, timestamp}},
				{"SELECT any_value(s),any_value(c),any_value(n),any_value(tm),any_value(ts) FROM t WHERE FALSE", []string{"any_value", "any_value", "any_value", "any_value", "any_value"}, []string{"VARCHAR", "BPCHAR", "NUMERIC", "TIME", "TIMESTAMP"}, []any{nil, nil, nil, nil, nil}},
				{"SELECT s,c,n,tm,ts FROM av_metadata", []string{"s", "c", "n", "tm", "ts"}, []string{"VARCHAR", "BPCHAR", "NUMERIC", "TIME", "TIMESTAMP"}, []any{"xy", "x   ", "1.2300", clock, timestamp}},
				{"SELECT any_value(s COLLATE \"POSIX\") FROM t", []string{"any_value"}, []string{"VARCHAR"}, []any{"xy"}},
				{"SELECT any_value(NULL::varchar(4)),any_value(NULL::numeric(10,4)),any_value(NULL::time(3)) FROM t", []string{"any_value", "any_value", "any_value"}, []string{"VARCHAR", "NUMERIC", "TIME"}, []any{nil, nil, nil}},
			} {
				for _, prepared := range []bool{false, true} {
					anyValueResult(t, p, backend, tc.q, prepared, tc.columns, tc.types, [][]any{tc.row})
				}
			}
		})
	}
}

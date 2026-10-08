package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceInsertSelectTypedControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, s := range []referenceStep{
				referenceExec("CREATE TABLE s(n NUMERIC(5,2),stamp TIMESTAMP(3),clock TIME,b BIGINT,u UUID)", 0),
				referenceExec("INSERT INTO s VALUES('1.234',TIMESTAMP '2000-01-01 00:00:00.1234',TIME '24:00',2147483648,'A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11'),(NULL,NULL,NULL,NULL,NULL)", 2),
				referenceExec("CREATE TABLE t(n NUMERIC(5,2),stamp TIMESTAMP(3),clock TIME,b BIGINT,u UUID)", 0),
			} {
				p.step(t, s)
			}
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
					step := referenceExec("INSERT INTO t SELECT * FROM s", 2)
					step.prepared = prepared
					insertSelectRun(t, ctx, backend.db, backend.recorder, step)
					var clock any = "24:00:00"
					scanType := reflect.TypeFor[string]()
					if backend.pg {
						clock = time.Date(0, time.January, 2, 0, 0, 0, 0, time.UTC)
						scanType = reflect.TypeFor[time.Time]()
					}
					// Assert civil TIME representation and capability metadata before normalization.
					rows, err := backend.db.QueryContext(ctx, "SELECT clock FROM t ORDER BY n")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					metadata, err := rows.ColumnTypes()
					if err != nil || len(metadata) != 1 || metadata[0].DatabaseTypeName() != "TIME" || metadata[0].ScanType() != scanType {
						t.Fatal(metadata, err)
					}
					if backend.pg {
						length, known := metadata[0].Length()
						if length != 0 || known {
							t.Fatal("pq TIME length", length, known)
						}
						nullable, known := metadata[0].Nullable()
						if nullable || known {
							t.Fatal("pq TIME nullable", nullable, known)
						}
					}
					for _, want := range []any{clock, nil} {
						if !rows.Next() {
							t.Fatal("missing raw TIME row", rows.Err())
						}
						var value any
						if err := rows.Scan(&value); err != nil || !reflect.DeepEqual(value, want) {
							t.Fatal(value, err, want)
						}
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("extra raw TIME row", rows.Err())
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					expected := referenceSelect("SELECT * FROM t ORDER BY n", []string{"n", "stamp", "clock", "b", "u"}, []string{"NUMERIC", "TIMESTAMP", "TIME", "INT8", "UUID"}, [][]any{{"1.23", time.Date(2000, 1, 1, 0, 0, 0, 123000000, time.UTC), clock, int64(2147483648), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"}, {nil, nil, nil, nil, nil}})
					referenceRunStep(t, ctx, backend.db, backend.recorder, expected)
				})
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			d := stuntgres.NewDriver()
			connector, err := d.OpenConnector(t.Name() + "/clone")
			if err != nil {
				t.Fatal(err)
			}
			clone := sql.OpenDB(connector)
			defer clone.Close()
			if err := stuntgres.Restore(p.ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			wrapped, recorder := insertSelectRecordedPool(t, connector)
			defer wrapped.Close()
			expected := referenceSelect("SELECT * FROM t ORDER BY n", []string{"n", "stamp", "clock", "b", "u"}, []string{"NUMERIC", "TIMESTAMP", "TIME", "INT8", "UUID"}, [][]any{{"1.23", time.Date(2000, 1, 1, 0, 0, 0, 123000000, time.UTC), "24:00:00", int64(2147483648), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"}, {nil, nil, nil, nil, nil}})
			referenceRunStep(t, p.ctx, wrapped, recorder, expected)
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, expected)
		})
	}
}

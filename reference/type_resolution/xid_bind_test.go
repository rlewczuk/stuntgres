package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

type xidReferenceValuer struct{ value driver.Value }

func (v xidReferenceValuer) Value() (driver.Value, error) { return v.value, nil }

func TestReferenceXIDBindsAndNulls(t *testing.T) {
	p := fixture.NewScenario(t)
	xidReferenceBackends(t, p, func(t *testing.T, b fixture.Backend) {
		for _, typ := range []string{"xid", "cid", "xid8"} {
			max := "4294967295"
			if typ == "xid8" {
				max = "18446744073709551615"
			}
			name := map[string]string{"xid": "XID", "cid": "CID", "xid8": "XID8"}[typ]
			table := "xid_bind_" + typ
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE "+table+"(id INT,v "+typ+",w "+typ+")", 0))
			q := "INSERT INTO " + table + " VALUES($1,$2,$2)"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			var typednil []byte
			cases := []struct {
				arg  any
				want any
			}{{"0", "0"}, {[]byte(max), max}, {int64(-1), max}, {xidReferenceValuer{"1"}, "1"}, {nil, nil}, {typednil, nil}, {"-1", max}}
			for _, prepared := range []bool{false, true} {
				var exec fixture.Executor = b.DB
				offset := 0
				if prepared {
					exec = held
					offset = 20
				}
				for i, tc := range cases {
					fixture.RunStep(t, p.Ctx, exec, b.Recorder, fixture.Exec(q, 1, i+offset, tc.arg))
					xidReferenceRows(t, p.Ctx, b, b.DB, fixture.Select("SELECT v,w FROM "+table+" WHERE id=$1", []string{"v", "w"}, []string{name, name}, [][]any{{tc.want, tc.want}}, i+offset))
				}
			}
			owned := []byte(max)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 1, 50, owned))
			owned[0] = '0'
			xidReferenceRows(t, p.Ctx, b, b.DB, fixture.Select("SELECT v,w FROM "+table+" WHERE id=50", []string{"v", "w"}, []string{name, name}, [][]any{{max, max}}))
			query := "SELECT v,w FROM " + table + " WHERE v=$1 ORDER BY id"
			filter, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer filter.Close()
			for _, tc := range []struct {
				arg   any
				want  any
				count int
			}{{"0", "0", 2}, {max, max, 7}, {nil, nil, 0}} {
				rows := make([][]any, tc.count)
				for i := range rows {
					rows[i] = []any{tc.want, tc.want}
				}
				xidReferenceRows(t, p.Ctx, b, fixture.HeldStatement{Executor: b.DB, Statement: filter, SQL: query}, fixture.Select(query, []string{"v", "w"}, []string{name, name}, rows, tc.arg))
			}
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type ` + typ + `: ""`, Context: "unnamed portal parameter $2 = ''"}, 60, []byte{})
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			xidReferenceRows(t, p.Ctx, b, b.DB, fixture.Select("SELECT v FROM "+table+" WHERE id=60", []string{"v"}, []string{name}, nil))
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
			for _, contextual := range []bool{false, true} {
				var value any
				if contextual {
					err = b.DB.QueryRowContext(ctx, "SELECT v FROM "+table+" WHERE id=50").Scan(&value)
				} else {
					err = b.DB.QueryRow("SELECT v FROM " + table + " WHERE id=50").Scan(&value)
				}
				if err != nil {
					t.Fatal(err)
				}
				bytes, ok := value.([]byte)
				if !ok || string(bytes) != max {
					t.Fatal(value)
				}
				bytes[0] = '0'
				for _, dest := range []any{new(string), new([]byte), new(sql.NullString)} {
					if contextual {
						err = b.DB.QueryRowContext(ctx, "SELECT v FROM "+table+" WHERE id=50").Scan(dest)
					} else {
						err = b.DB.QueryRow("SELECT v FROM " + table + " WHERE id=50").Scan(dest)
					}
					if err != nil {
						t.Fatal(err)
					}
					switch v := dest.(type) {
					case *string:
						if *v != max {
							t.Fatal(v)
						}
					case *[]byte:
						if string(*v) != max {
							t.Fatal(v)
						}
					case *sql.NullString:
						if !v.Valid || v.String != max {
							t.Fatal(v)
						}
					}
				}
				var dest string
				if contextual {
					err = b.DB.QueryRowContext(ctx, "SELECT v FROM "+table+" WHERE id=4").Scan(&dest)
				} else {
					err = b.DB.QueryRow("SELECT v FROM " + table + " WHERE id=4").Scan(&dest)
				}
				var state interface{ SQLState() string }
				if err == nil || errors.As(err, &state) {
					t.Fatal("ordinary NULL scan error", err)
				}
			}
			xidReferenceRows(t, p.Ctx, b, b.DB, fixture.Select("SELECT v,w FROM "+table+" WHERE id=50", []string{"v", "w"}, []string{name, name}, [][]any{{max, max}}))
			if !b.PG {
				result, err := stmt.ExecContext(ctx, 70, uint64(math.MaxInt64)+1)
				var state interface{ SQLState() string }
				if result != nil || err == nil || errors.As(err, &state) {
					t.Fatal("ordinary converter boundary", result, err)
				}
				canceled, cancel := context.WithCancel(p.Ctx)
				cancel()
				rows, err := b.DB.QueryContext(canceled, "SELECT v FROM "+table)
				if rows != nil || !errors.Is(err, context.Canceled) {
					t.Fatal(rows, err)
				}
			}
			rows, err := b.DB.QueryContext(ctx, "SELECT v,w FROM "+table+" WHERE id=50")
			if err != nil {
				t.Fatal(err)
			}
			if !rows.Next() {
				rows.Close()
				t.Fatal(rows.Err())
			}
			var v, w []byte
			if err = rows.Scan(&v, &w); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			v[0] = '0'
			if string(w) != max {
				rows.Close()
				t.Fatal("aliased result columns", w)
			}
			metadata, err := rows.ColumnTypes()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if rows.Next() || rows.Err() != nil {
				rows.Close()
				t.Fatal("extra row", rows.Err())
			}
			if err = rows.Close(); err != nil {
				t.Fatal(err)
			}
			expectedName := name
			expectedScan := reflect.TypeFor[[]byte]()
			if b.PG {
				expectedScan = reflect.TypeFor[any]()
				if typ == "xid8" {
					expectedName = ""
				}
			}
			for _, column := range metadata {
				if column.DatabaseTypeName() != expectedName || column.ScanType() != expectedScan {
					t.Fatal("retained metadata", column)
				}
			}
		}
	})
}

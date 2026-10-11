package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFloatMigratedSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx, db := p.Ctx, backend.DB
			var err error

			exec := func(q string, count int64, args ...any) {
				t.Helper()
				result, err := db.ExecContext(ctx, q, args...)
				if err != nil {
					t.Fatal(q, err)
				}
				n, err := result.RowsAffected()
				if err != nil || n != count {
					t.Fatal(q, n, err)
				}
			}
			exec(`CREATE TABLE aliases(a REAL,b FLOAT4,c DOUBLE PRECISION,d FLOAT8,e FLOAT,f FLOAT(1),g FLOAT(24),h FLOAT(25),i FLOAT(53))`, 0)
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				if prepared {
					s, e := db.PrepareContext(ctx, "SELECT * FROM aliases")
					if e != nil {
						t.Fatal(e)
					}
					rows, err = s.QueryContext(ctx)
					defer s.Close()
				} else {
					rows, err = db.QueryContext(ctx, "SELECT * FROM aliases")
				}
				if err != nil {
					t.Fatal(err)
				}
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				for i, want := range []string{"FLOAT4", "FLOAT4", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT4", "FLOAT4", "FLOAT8", "FLOAT8"} {
					if types[i].DatabaseTypeName() != want || types[i].ScanType() != func() reflect.Type {
						if backend.PG && want == "FLOAT4" {
							return reflect.TypeFor[float32]()
						}
						return reflect.TypeFor[float64]()
					}() {
						t.Fatal(types[i])
					}
				}
				rows.Close()
			}
			for _, tc := range []struct{ q, code, message, pos string }{{"CREATE TABLE f(x FLOAT(0))", "22023", "precision for type float must be at least 1 bit", "24"}, {"CREATE TABLE f(x FLOAT(54))", "22023", "precision for type float must be less than 54 bits", "24"}, {"CREATE TABLE f(x FLOAT(-1))", "42601", `syntax error at or near "-"`, "24"}} {
				for _, prepared := range []bool{false, true} {
					var err error
					if prepared {
						s, e := db.PrepareContext(ctx, tc.q)
						err = e
						if s != nil {
							s.Close()
							t.Fatal("failed prepare handle")
						}
					} else {
						_, err = db.ExecContext(ctx, tc.q)
					}
					if err = referenceMigratedDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Position: tc.pos}); err != nil {
						t.Fatal(err)
					}
				}
			}
			exec("CREATE TABLE f(id INTEGER,r REAL,d DOUBLE PRECISION)", 0)
			exec("INSERT INTO f VALUES(1,'16777217','-0'),(2,'7.038531e-26','5e-324'),(3,'33554448','0.1')", 3)
			count := func(q string, want int, args ...any) {
				t.Helper()
				rows, err := db.QueryContext(ctx, q, args...)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				n := 0
				for rows.Next() {
					n++
				}
				if rows.Err() != nil || n != want {
					t.Fatal(q, n, want, rows.Err())
				}
			}
			count("SELECT r FROM f WHERE r=16777217", 0)
			count("SELECT r FROM f WHERE r='16777217'", 1)
			held, err := db.PrepareContext(ctx, "SELECT r FROM f WHERE r=$1")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, v := range []any{"16777217", int64(16777217), float64(16777217), nil} {
				rows, err := held.QueryContext(ctx, v)
				if err != nil {
					t.Fatal(err)
				}
				if v == nil {
					// database/sql closes an exhausted cursor automatically; capture
					// metadata while the empty cursor is still open, before Next.
					types, err := rows.ColumnTypes()
					if err != nil || len(types) != 1 {
						t.Fatal("NULL-result metadata", types, err)
					}
					if types[0].DatabaseTypeName() != "FLOAT4" || types[0].ScanType() != func() reflect.Type {
						if backend.PG {
							return reflect.TypeFor[float32]()
						}
						return reflect.TypeFor[float64]()
					}() {
						t.Fatal(types)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("NULL predicate", rows.Err())
					}
				} else {
					var r float64
					if !rows.Next() || rows.Scan(&r) != nil || r != 16777216 || rows.Next() {
						t.Fatal(v, r)
					}
				}
				rows.Close()
			}
			var r, n float64
			if err = db.QueryRowContext(ctx, "SELECT r,d FROM f WHERE id=2").Scan(&r, &n); err != nil || math.Float64bits(n) != 1 {
				t.Fatal(r, n, err)
			}
			if backend.PG {
				// pq parses PostgreSQL's shortest REAL text into a float64. Do not
				// narrow that payload again: it differs from native direct32 input.
				if r != 7.038531e-26 {
					t.Fatal("PostgreSQL REAL text payload", r)
				}
			} else if math.Float32bits(float32(r)) != 0x15ae43fd {
				t.Fatal("native direct32 input bits", r)
			}
			exec("CREATE TABLE ordered(id INTEGER,r REAL)", 0)
			insert, err := db.PrepareContext(ctx, "INSERT INTO ordered VALUES($1,$2)")
			if err != nil {
				t.Fatal(err)
			}
			defer insert.Close()
			for i, v := range []any{math.Inf(-1), float64(-1), math.Copysign(0, -1), float64(0), float64(1), math.Inf(1), math.NaN(), "NaN", nil} {
				res, err := insert.ExecContext(ctx, i+1, v)
				if err != nil {
					t.Fatal(err)
				}
				n, err := res.RowsAffected()
				if err != nil || n != 1 {
					t.Fatal(n, err)
				}
			}
			count("SELECT r FROM ordered WHERE r='NaN'", 2)
			for _, desc := range []bool{false, true} {
				q := "SELECT id FROM ordered ORDER BY r"
				groups := []int{0, 1, 2, 2, 3, 4, 5, 5, 6}
				if desc {
					q += " DESC"
					groups = []int{6, 5, 5, 4, 3, 2, 2, 1, 0}
				}
				rows, err := db.QueryContext(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				i := 0
				seen := map[int64]bool{}
				for rows.Next() {
					var id int64
					if err = rows.Scan(&id); err != nil {
						t.Fatal(err)
					}
					group := map[int64]int{1: 0, 2: 1, 3: 2, 4: 2, 5: 3, 6: 4, 7: 5, 8: 5, 9: 6}[id]
					if seen[id] || i >= 9 || group != groups[i] {
						t.Fatal(q, id, i)
					}
					seen[id] = true
					i++
				}
				if rows.Err() != nil || i != 9 {
					t.Fatal(i, rows.Err())
				}
				rows.Close()
			}
			for _, tc := range []struct{ q, code, message, pos string }{{"INSERT INTO f(r) VALUES('oops')", "22P02", `invalid input syntax for type real: "oops"`, "25"}, {"INSERT INTO f(r) VALUES('1e999')", "22003", `"1e999" is out of range for type real`, "25"}} {
				_, err = db.ExecContext(ctx, tc.q)
				if err = referenceMigratedDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Position: tc.pos}); err != nil {
					t.Fatal(err)
				}
			}
			_, err = db.ExecContext(ctx, "INSERT INTO f VALUES(4,'1','1'),(5,'oops','2')")
			if err == nil {
				t.Fatal("later malformed tuple")
			}
			count("SELECT * FROM f", 3)
			if backend.PG {
				return
			}
			db = p.Native
			raw, err := p.NativeConnector.Connect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT * FROM f ORDER BY id", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cursor.Close()
			var dump bytes.Buffer
			if err = stuntgres.Dump(ctx, db, &dump); err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{"REAL", "DOUBLE PRECISION", "'3.3554448e+07'", "'-0'", "'7.038531e-26'"} {
				if !strings.Contains(dump.String(), text) {
					t.Fatal(text, dump.String())
				}
			}
			if err = stuntgres.Reset(ctx, db); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			var redump bytes.Buffer
			if err = stuntgres.Dump(ctx, db, &redump); err != nil || dump.String() != redump.String() {
				t.Fatal(redump.String(), err)
			}
			var zero float64
			if err = db.QueryRowContext(ctx, "SELECT d FROM f WHERE id=1").Scan(&zero); err != nil || !math.Signbit(zero) {
				t.Fatal(zero, err)
			}
			if err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(r REAL); INSERT INTO replacement VALUES('1'),('oops')")); err == nil {
				t.Fatal("invalid restore")
			}
			count("SELECT * FROM f", 3)
			if err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(t TEXT); INSERT INTO replacement VALUES('new')")); err != nil {
				t.Fatal(err)
			}
			if cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(1) != "FLOAT4" {
				t.Fatal("retained metadata")
			}
			dst := make([]driver.Value, 3)
			for i := 1; i <= 3; i++ {
				if err = cursor.Next(dst); err != nil || dst[0] != int64(i) {
					t.Fatal(dst, err)
				}
			}
			if err = cursor.Next(dst); err != io.EOF {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			_, err = db.ExecContext(canceled, "INSERT INTO replacement VALUES('bad')")
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			count("SELECT * FROM replacement", 1)
		})
	}
}

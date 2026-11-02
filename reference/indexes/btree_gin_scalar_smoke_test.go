package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"math"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBtreeGINScalarFeatureSmoke(t *testing.T) {
	var emitted string
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		compoundCleanup(t, p, b, conn)
		run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
		run(fixture.Exec("CREATE TABLE scalar_smoke(id integer NOT NULL,s int2,i int4,l int8,r float4,d float8,b bool)", 0))
		run(fixture.Exec("CREATE INDEX scalar_smoke_i ON scalar_smoke USING gin(s public.int2_ops,i public.int4_ops,l public.int8_ops,r public.float4_ops,d public.float8_ops,b public.bool_ops)", 0))
		run(fixture.Exec("INSERT INTO scalar_smoke VALUES(1,0,0,9007199254740993,'-0','-0',false),(2,0,0,9007199254740994,'0','0',false),(3,1,1,9223372036854775807,'Infinity','Infinity',true),(4,-1,-1,-9223372036854775808,'NaN','NaN',true),(5,1,1,1,'NaN','NaN',false),(6,NULL,NULL,NULL,NULL,NULL,NULL)", 6))
		q := "SELECT id FROM scalar_smoke WHERE l=$1::int8 ORDER BY id"
		held := classHeld(t, p, conn, q)
		values := fixture.Select("SELECT s,i,l,r,d,b FROM scalar_smoke WHERE id=$1", []string{"s", "i", "l", "r", "d", "b"}, []string{"INT2", "INT4", "INT8", "FLOAT4", "FLOAT8", "BOOL"}, [][]any{{int64(0), int64(0), int64(9007199254740993), math.Copysign(0, -1), math.Copysign(0, -1), false}}, 1)
		values.CheckRaw = func(t *testing.T, column, row int, value any) bool {
			if column != 3 && column != 4 {
				return false
			}
			got, ok := value.(float64)
			if !ok || got != 0 || !math.Signbit(got) {
				t.Fatalf("negative zero column %d=%T %v", column, value, value)
			}
			return true
		}
		values.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			cols, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			fixture.ScalarAssertDescription(t, cols, values.Columns, values.Types)
			fixture.ScalarAssertMetadata(t, b.PG, cols)
			for i, c := range cols {
				if i == 3 || i == 4 {
					scan := reflect.TypeFor[float64]()
					if b.PG && i == 3 {
						scan = reflect.TypeFor[float32]()
					}
					if c.ScanType() != scan {
						t.Fatalf("float scan %v want %v", c.ScanType(), scan)
					}
				}
				nullable, known := c.Nullable()
				if known != !b.PG || nullable != !b.PG {
					t.Fatalf("nullable column %d=%t/%t", i, nullable, known)
				}
				if n, ok := c.Length(); n != 0 || ok {
					t.Fatalf("length %d/%t", n, ok)
				}
				if p, s, ok := c.DecimalSize(); p != 0 || s != 0 || ok {
					t.Fatalf("decimal %d,%d/%t", p, s, ok)
				}
			}
		}
		valueHeld := classHeld(t, p, conn, values.Query)
		check := func() {
			classRoutes(t, p, b, conn, ginIDs(q, []int64{1}, int64(9007199254740993)))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(q, []int64{1}, int64(9007199254740993)))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(q, nil, nil))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM scalar_smoke WHERE r='0'::float4 ORDER BY id", []int64{1, 2}))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM scalar_smoke WHERE d>'Infinity'::float8 ORDER BY id", []int64{4, 5}))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM scalar_smoke WHERE b<>false ORDER BY id", []int64{3, 4}))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM scalar_smoke WHERE l IS NULL ORDER BY id", []int64{6}))
			classRoutes(t, p, b, conn, values)
			fixture.RunStep(t, p.Ctx, valueHeld, b.Recorder, values)
		}
		check()
		ginUtilityError(t, p, b, conn, "CREATE INDEX scalar_smoke_bad ON scalar_smoke USING gin(i int4_ops,r float8_ops)", fixture.ControlsReferenceDiagnostic("42804", `operator class "float8_ops" does not accept data type real`))
		compoundIndexAbsent(run, "scalar_smoke", "scalar_smoke_bad", 1)
		for _, pair := range []struct{ typ, value, label string }{{"int8", "1", "bigint"}, {"bool", "false", "boolean"}} {
			query := "SELECT '" + pair.value + "'::" + pair.typ + " <-> '" + pair.value + "'::" + pair.typ
			d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+pair.label+" <-> "+pair.label)
			d.Hint = compoundOperatorHint
			d.Position = fixture.Position(strings.Index(query, "<->") + 1)
			s := fixture.ErrorDiagnostic(query, d)
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			s.Prepared = true
			s.ErrorStage = "prepare"
			run(s)
		}
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = dump.String()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
		} else if emitted != "" {
			run(fixture.Exec("DROP TABLE scalar_smoke", 0))
			run(fixture.Exec("DROP EXTENSION btree_gin CASCADE", 0))
			if _, err := conn.ExecContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), emitted); err != nil {
				t.Fatalf("actual scalar smoke Dump replay: %v", err)
			}
		}
		check()
		scalarGraph(t, p, b, conn, "public")
		run(fixture.Prepared(fixture.Exec("INSERT INTO scalar_smoke(id,l,d) VALUES(7,$1::int8,$2::float8)", 1, int64(9007199254740993), "NaN")))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(q, []int64{1, 7}, int64(9007199254740993)))
		run(fixture.Exec("UPDATE scalar_smoke SET l=1 WHERE id=7", 1))
		run(fixture.Exec("DELETE FROM scalar_smoke WHERE id=7", 1))
		check()
	})
}

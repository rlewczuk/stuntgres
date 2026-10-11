package reference_test

import (
	"bytes"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Preserve the legacy ten-row corpus and its held INSERT/raw export routes.
func TestReferenceByteaSmokeMigration(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			exec := func(s fixture.Step) { referenceByteaExec(t, p.Ctx, conn, b, s) }
			exec(fixture.Exec("CREATE TABLE t(id INTEGER,v BYTEA)", 0))
			exec(fixture.Exec(`INSERT INTO t VALUES(1,''),(2,'\x00aAfF'),(3,'\000\177\200\377'),(4,'\\'),(5,'é'),(6,NULL)`, 6))
			q := "INSERT INTO t VALUES($1,$2)"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for i, arg := range []any{[]byte{0, 128, 255}, `\x41`, []byte(nil), []byte{}} {
				referenceByteaExec(t, p.Ctx, fixture.HeldStatement{conn, stmt, q}, b, fixture.Exec(q, 1, 7+i, arg))
			}
			want := [][]any{{[]byte{}}, {[]byte{0, 170, 255}}, {[]byte{0, 127, 128, 255}}, {[]byte{92}}, {[]byte{195, 169}}, {nil}, {[]byte{0, 128, 255}}, {[]byte{92, 120, 52, 49}}, {nil}, {[]byte{}}}
			check := func(mode string) {
				t.Helper()
				expected := make([][]any, len(want))
				for i, row := range want {
					expected[i] = append([]any(nil), row...)
				}
				if b.PG && mode == "escape" {
					expected[0][0], expected[9][0] = []byte(nil), []byte(nil)
				}
				referenceByteaRaw(t, p.Ctx, conn, b, "SELECT v FROM t ORDER BY id", false, []string{"v"}, []int{1}, expected, true)
				referenceByteaRaw(t, p.Ctx, conn, b, "SELECT v FROM t ORDER BY id", false, []string{"v"}, []int{1}, expected, false)
			}
			check("hex")
			exec(fixture.Exec("CREATE TABLE nn(v BYTEA,n INTEGER NOT NULL)", 0))
			for _, mode := range []struct{ name, detail string }{
				{"hex", `Failing row contains (\x0020275c7e7f80ff, null).`},
				{"escape", `Failing row contains (\000 '\\~\177\200\377, null).`},
			} {
				exec(fixture.Exec("SET bytea_output="+mode.name, 0))
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SHOW bytea_output", []string{"bytea_output"}, []string{"TEXT"}, [][]any{{mode.name}}))
				check(mode.name)
				s := fixture.ImplicitError(`INSERT INTO nn VALUES('\x0020275c7e7f80ff',NULL)`, fixture.Diagnostic{Code: "23502", Message: `null value in column "n" of relation "nn" violates not-null constraint`, Detail: mode.detail, Schema: "public", Table: "nn", Column: "n"})
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
			}
			s := fixture.ImplicitError(`INSERT INTO t VALUES(11,'\x41'),(12,'\q')`, fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type bytea", Position: "37"})
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
			check("escape")
			exec(fixture.Exec("SET bytea_output=hex", 0))
			exec(fixture.Exec("CREATE TABLE binary_schema(id INTEGER,v BYTEA,r BYTEA NOT NULL)", 0))
			exec(fixture.Exec(`INSERT INTO binary_schema VALUES(1,'',''),(2,'\x0080ff','\x0080ff'),(3,NULL,'\xff')`, 3))
			checkSchema := func() {
				t.Helper()
				referenceByteaRaw(t, p.Ctx, conn, b, "SELECT v,r FROM binary_schema ORDER BY id", false, []string{"v", "r"}, []int{1, 0}, [][]any{{[]byte{}, []byte{}}, {[]byte{0, 128, 255}, []byte{0, 128, 255}}, {nil, []byte{255}}}, false)
			}
			checkSchema()
			exec(fixture.Exec("SET bytea_output=escape", 0))
			if !b.PG {
				if err := conn.Raw(func(raw any) error {
					held, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT v FROM t ORDER BY id", nil)
					if err != nil {
						return err
					}
					defer held.Close()
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						return err
					}
					for _, piece := range []string{`"v" BYTEA, "r" BYTEA CONSTRAINT "binary_schema_r_not_null" NOT NULL`, `'\x00aaff'`, `'\x0080ff'`, `'\xff'`, `'\x'`, `NULL`} {
						if !strings.Contains(dump.String(), piece) {
							t.Fatal(piece, dump.String())
						}
					}
					if _, err := raw.(driver.ExecerContext).ExecContext(p.Ctx, "SET bytea_output=hex", nil); err != nil {
						return err
					}
					var hex bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &hex); err != nil {
						return err
					}
					if !bytes.Equal(dump.Bytes(), hex.Bytes()) {
						t.Fatal("dump changed with output mode")
					}
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						return err
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						return err
					}
					dst := make([]driver.Value, 1)
					for _, row := range want {
						if err := held.Next(dst); err != nil || !reflect.DeepEqual(dst[0], row[0]) {
							t.Fatal(dst, row, err)
						}
					}
					if err := held.Next(dst); err != io.EOF {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(held.Columns(), []string{"v"}) || held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "BYTEA" || held.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[[]byte]() {
						t.Fatal("retained metadata")
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				check("hex")
				checkSchema()
			}
			exec(fixture.Exec("SET bytea_output=hex", 0))
			s = fixture.ImplicitError("INSERT INTO binary_schema VALUES(4,NULL,$1),(5,NULL,NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "r" of relation "binary_schema" violates not-null constraint`, Detail: "Failing row contains (5, null, null).", Schema: "public", Table: "binary_schema", Column: "r"}, []byte{})
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
			checkSchema()
		})
	}
}

package reference_test

import (
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceByteaLiteralForms(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec(`CREATE TABLE t(id INTEGER,v "bytea")`, 0))
			for pass, prepared := range []bool{false, true} {
				for i, tc := range []struct {
					literal string
					want    []byte
				}{
					{`''`, []byte{}}, {`'\x'`, []byte{}}, {`'\x00aAfF'`, []byte{0, 170, 255}},
					{"'\\x 00\tAA\nff\r'", []byte{0, 170, 255}}, {`'\000\177\200\377'`, []byte{0, 127, 128, 255}},
					{`'\\'`, []byte{92}}, {`''''`, []byte{39}}, {`'é'`, []byte{195, 169}},
					{`$$\x0080ff$$`, []byte{0, 128, 255}}, {`E'\\x0080ff'`, []byte{0, 128, 255}},
					{`U&'\005Cx41'`, []byte{65}}, {`U&'\005Cx0080ff'`, []byte{0, 128, 255}},
				} {
					s := fixture.Exec("INSERT INTO t VALUES($1,"+tc.literal+")", 1, i)
					s.Prepared = prepared
					referenceByteaExec(t, p.Ctx, b.DB, b, s)
					want := [][]any{{tc.want}}
					if pass == 1 {
						want = append(want, []any{tc.want})
					}
					referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v FROM t WHERE id=$1", false, []string{"v"}, []int{1}, want, false, i)
				}
			}
			_, err := b.DB.ExecContext(p.Ctx, `CREATE TABLE bad(v "BYTEA")`)
			fixture.State(t, err, "42704")
			if !b.PG {
				var se *stuntgres.Error
				if !errors.As(err, &se) || se.Code != "42704" {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestReferenceByteaOrderingAndHeldParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec("CREATE TABLE t(v BYTEA)", 0))
			referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec(`INSERT INTO t VALUES('\xff'),('\x80'),('\x7f'),('\x0000'),('\x00'),(''),(NULL)`, 7))
			referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v FROM t ORDER BY v", false, []string{"v"}, []int{1}, [][]any{{[]byte{}}, {[]byte{0}}, {[]byte{0, 0}}, {[]byte{127}}, {[]byte{128}}, {[]byte{255}}, {nil}}, false)
			referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v FROM t ORDER BY v DESC", false, []string{"v"}, []int{1}, [][]any{{nil}, {[]byte{255}}, {[]byte{128}}, {[]byte{127}}, {[]byte{0, 0}}, {[]byte{0}}, {[]byte{}}}, false)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v=v FROM t ORDER BY v", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}, {true}, {true}, {true}, {true}, {true}, {nil}}))
			referenceByteaRaw(t, p.Ctx, b.DB, b, `SELECT v FROM t WHERE v='\x00'`, false, []string{"v"}, []int{1}, [][]any{{[]byte{0}}}, false)
			referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v FROM t WHERE v=NULL", false, []string{"v"}, []int{1}, nil, false)
			q := "SELECT v FROM t WHERE v=$1"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			}()
			x := fixture.HeldStatement{b.DB, stmt, q}
			for _, arg := range []any{[]byte{0}, []byte{255}, nil} {
				var want [][]any
				if arg != nil {
					want = [][]any{{arg}}
				}
				referenceByteaRaw(t, p.Ctx, x, b, q, false, []string{"v"}, []int{1}, want, false, arg)
			}
			referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec("CREATE TABLE repeats(a BYTEA,b BYTEA)", 0))
			referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec("INSERT INTO repeats VALUES($1,$1)", 1, []byte{0, 255}))
			referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT * FROM repeats", false, []string{"a", "b"}, []int{1, 1}, [][]any{{[]byte{0, 255}, []byte{0, 255}}}, false)
		})
	}
}

func TestReferenceByteaSettingsAndAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			}()
			exec := func(q string, count int64) { referenceByteaExec(t, p.Ctx, conn, b, fixture.Exec(q, count)) }
			show := func(want string) {
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SHOW bytea_output", []string{"bytea_output"}, []string{"TEXT"}, [][]any{{want}}))
			}
			exec("CREATE TABLE ordered(v BYTEA)", 0)
			exec(`INSERT INTO ordered VALUES('\xff'),('\x80'),('\x7f'),('\x0000'),('\x00'),(''),(NULL)`, 7)
			asc := [][]any{{[]byte{}}, {[]byte{0}}, {[]byte{0, 0}}, {[]byte{127}}, {[]byte{128}}, {[]byte{255}}, {nil}}
			show("hex")
			exec("SET bytea_output=ESCAPE", 0)
			show("escape")
			escape := [][]any{{[]byte{}}, {[]byte{0}}, {[]byte{0, 0}}, {[]byte{127}}, {[]byte{128}}, {[]byte{255}}, {nil}}
			if b.PG {
				escape[0][0] = []byte(nil)
			}
			referenceByteaRaw(t, p.Ctx, conn, b, "SELECT v FROM ordered ORDER BY v", false, []string{"v"}, []int{1}, escape, false)
			exec("SET bytea_output=DEFAULT", 0)
			show("hex")
			referenceByteaRaw(t, p.Ctx, conn, b, "SELECT v FROM ordered ORDER BY v", false, []string{"v"}, []int{1}, asc, false)
			exec("SET bytea_output=escape", 0)
			exec("RESET bytea_output", 0)
			show("hex")
			exec("CREATE TABLE t(v BYTEA,n INTEGER NOT NULL)", 0)
			exec(`INSERT INTO t VALUES('\x41',1)`, 1)
			exec("BEGIN", 0)
			exec("SET LOCAL bytea_output=escape", 0)
			show("escape")
			s := fixture.ImplicitError(`INSERT INTO t VALUES('\x00',NULL)`, fixture.Diagnostic{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: `Failing row contains (\000, null).`, Schema: "public", Table: "t", Column: "n"})
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
			exec("ROLLBACK", 0)
			show("hex")
			exec("SET standard_conforming_strings=off", 0)
			exec(`INSERT INTO t VALUES('\\x42',2)`, 1)
			exec("SET standard_conforming_strings=on", 0)
			for _, tc := range []struct{ q, state string }{
				{`INSERT INTO t VALUES('\x43',3),('\q',4)`, "22P02"}, {`INSERT INTO t VALUES('\x43',3),('\x44',NULL)`, "23502"},
			} {
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Error(tc.q, tc.state))
				referenceByteaRaw(t, p.Ctx, conn, b, "SELECT v FROM t ORDER BY n", false, []string{"v"}, []int{1}, [][]any{{[]byte{65}}, {[]byte{66}}}, false)
			}
		})
	}
}

func TestReferenceByteaLexing(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			}()
			referenceByteaExec(t, p.Ctx, conn, b, fixture.Exec("CREATE TABLE lex(id INTEGER,v BYTEA)", 0))
			for i, literal := range []string{`$$\x0080ff$$`, `E'\\x0080ff'`, `U&'\005Cx0080ff'`} {
				referenceByteaExec(t, p.Ctx, conn, b, fixture.Exec("INSERT INTO lex VALUES($1,"+literal+")", 1, i))
			}
			referenceByteaExec(t, p.Ctx, conn, b, fixture.Exec("SET standard_conforming_strings=off", 0))
			referenceByteaExec(t, p.Ctx, conn, b, fixture.Exec(`INSERT INTO lex VALUES(3,'\\x0080ff')`, 1))
			referenceByteaExec(t, p.Ctx, conn, b, fixture.Exec("SET standard_conforming_strings=on", 0))
			referenceByteaRaw(t, p.Ctx, conn, b, "SELECT v FROM lex ORDER BY id", false, []string{"v"}, []int{1}, [][]any{{[]byte{0, 128, 255}}, {[]byte{0, 128, 255}}, {[]byte{0, 128, 255}}, {[]byte{0, 128, 255}}}, false)
		})
	}
}

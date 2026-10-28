package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceByteaUTF8Diagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, ch := range []string{"é", "界", "😀"} {
			for _, input := range []string{`\x` + ch + "0", `\x0` + ch} {
				t.Run(b.Name+"/"+input, func(t *testing.T) {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := conn.Close(); err != nil {
							t.Error(err)
						}
					}()
					referenceByteaExec(t, p.Ctx, conn, b, fixture.Exec("CREATE TABLE t(v BYTEA)", 0))
					defer referenceByteaExec(t, p.Ctx, conn, b, fixture.Exec("DROP TABLE t", 0))
					q := fmt.Sprintf("/*é*/ INSERT INTO t VALUES('%s')", input)
					referenceByteaDiagnosticRoutes(t, p, b, conn, q, fixture.Diagnostic{Code: "22023", Message: fmt.Sprintf("invalid hexadecimal digit: \"%s\"", ch), Position: "28"}, false)
					referenceByteaRaw(t, p.Ctx, conn, b, "SELECT v FROM t", false, []string{"v"}, []int{1}, nil, false)
				})
			}
		}
	}
}

func TestReferenceByteaSQLBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec("CREATE TABLE t(v BYTEA)", 0))
			referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec(`INSERT INTO t VALUES('\x41')`, 1))
			for _, tc := range []struct{ q, message string }{
				{`INSERT INTO t VALUES(E'\000')`, `invalid byte sequence for encoding "UTF8": 0x00`},
				{`INSERT INTO t VALUES(E'\xff')`, `invalid byte sequence for encoding "UTF8": 0xff`},
			} {
				referenceByteaDiagnosticRoutes(t, p, b, b.DB, tc.q, fixture.Diagnostic{Code: "22021", Message: tc.message}, false)
				referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v FROM t", false, []string{"v"}, []int{1}, [][]any{{[]byte{65}}}, false)
			}
		})
	}
}

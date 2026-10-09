package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceByteaUTF8Diagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		for _, ch := range []string{"é", "界", "😀"} {
			for _, input := range []string{`\x` + ch + "0", `\x0` + ch} {
				t.Run(b.name+"/"+input, func(t *testing.T) {
					conn, err := b.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := conn.Close(); err != nil {
							t.Error(err)
						}
					}()
					referenceByteaExec(t, p.ctx, conn, b, referenceExec("CREATE TABLE t(v BYTEA)", 0))
					defer referenceByteaExec(t, p.ctx, conn, b, referenceExec("DROP TABLE t", 0))
					q := fmt.Sprintf("/*é*/ INSERT INTO t VALUES('%s')", input)
					referenceByteaDiagnosticRoutes(t, p, b, conn, q, referenceDiagnostic{Code: "22023", Message: fmt.Sprintf("invalid hexadecimal digit: \"%s\"", ch), Position: "28"}, false)
					referenceByteaRaw(t, p.ctx, conn, b, "SELECT v FROM t", false, []string{"v"}, []int{1}, nil, false)
				})
			}
		}
	}
}

func TestReferenceByteaSQLBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			referenceByteaExec(t, p.ctx, b.db, b, referenceExec("CREATE TABLE t(v BYTEA)", 0))
			referenceByteaExec(t, p.ctx, b.db, b, referenceExec(`INSERT INTO t VALUES('\x41')`, 1))
			for _, tc := range []struct{ q, message string }{
				{`INSERT INTO t VALUES(E'\000')`, `invalid byte sequence for encoding "UTF8": 0x00`},
				{`INSERT INTO t VALUES(E'\xff')`, `invalid byte sequence for encoding "UTF8": 0xff`},
			} {
				referenceByteaDiagnosticRoutes(t, p, b, b.db, tc.q, referenceDiagnostic{Code: "22021", Message: tc.message}, false)
				referenceByteaRaw(t, p.ctx, b.db, b, "SELECT v FROM t", false, []string{"v"}, []int{1}, [][]any{{[]byte{65}}}, false)
			}
		})
	}
}

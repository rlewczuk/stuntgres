//go:build !noservertests

package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// ABS is immutable for REAL and DOUBLE PRECISION. IEEE NaN is a supported
// operand, so unchanged Bind inputs must remain executable, including when the
// named definition has been closed and replaced. Expectations are SQL results
// and protocol command counts, not values copied from the native backend.
func TestReferencePreparedFoldProtocolNaN(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, typ := range []string{"real", "double precision"} {
				for _, phase := range []string{"bind_execute", "statement_replacement"} {
					t.Run(typ+"/"+phase, func(t *testing.T) {
						w := serverAuthenticated(t, f.DSNFor(b))
						command := func(sql, tag string, state byte) {
							t.Helper()
							frames := w.query(t, sql)
							preparedFoldWireCheck(t, frames, "CZ", state)
							serverWantCompletions(t, frames, tag)
						}
						inspect := func(want [][]string) {
							t.Helper()
							frames := w.query(t, "SELECT id,v FROM fold_nan ORDER BY id")
							tags := "T"
							for range want {
								tags += "D"
							}
							preparedFoldWireCheck(t, frames, tags+"CZ", 'T')
							if got := serverValues(t, frames); !reflect.DeepEqual(got, want) {
								t.Fatalf("table rows=%v want=%v", got, want)
							}
						}
						parse := func(query string) {
							t.Helper()
							preparedFoldWireCheck(t, serverCycle(t, w, serverParse("fold_nan_stmt", query)), "1Z", 'T')
						}
						bind := func(portal string) {
							t.Helper()
							preparedFoldWireCheck(t, serverCycle(t, w, serverBind(portal, "fold_nan_stmt", nil, nil), serverDescribe('P', portal)), "2nZ", 'T')
							// Same transaction sees any accidental Bind side effects.
							inspect(nil)
						}
						execute := func(portal string) {
							t.Helper()
							frames := serverCycle(t, w, serverExecute(portal, 0))
							preparedFoldWireCheck(t, frames, "CZ", 'T')
							serverWantCompletions(t, frames, "INSERT 0 1")
							if got := serverValues(t, frames); len(got) != 0 {
								t.Fatalf("INSERT unexpectedly returned rows: %v", got)
							}
							preparedFoldWireCheck(t, serverCycle(t, w, serverClose('P', portal)), "3Z", 'T')
						}
						command("BEGIN", "BEGIN", 'T')
						command("CREATE TABLE fold_nan(id integer,v "+typ+")", "CREATE TABLE", 'T')
						// INSERT SELECT uses ordinary projection operands rather than
						// the narrower scalar-input replay applied to INSERT VALUES.
						parse("INSERT INTO fold_nan SELECT 1,abs('NaN'::"+typ+") AS v")
						bind("fold_nan_old")
						if phase == "statement_replacement" {
							preparedFoldWireCheck(t, serverCycle(t, w, serverClose('S', "fold_nan_stmt")), "3Z", 'T')
							parse("INSERT INTO fold_nan SELECT 2,abs(-7::"+typ+") AS v")
							bind("fold_nan_new")
						}
						execute("fold_nan_old")
						inspect([][]string{{"1", "NaN"}})
						if phase == "statement_replacement" {
							execute("fold_nan_new")
							inspect([][]string{{"1", "NaN"}, {"2", "7"}})
							// A new Bind must still use the replacement, not artifacts
							// published by execution of the retired NaN owner.
							command("DELETE FROM fold_nan", "DELETE 2", 'T')
							bind("fold_nan_after_old")
							execute("fold_nan_after_old")
							inspect([][]string{{"2", "7"}})
						}
						preparedFoldWireCheck(t, serverCycle(t, w, serverClose('S', "fold_nan_stmt")), "3Z", 'T')
						command("ROLLBACK", "ROLLBACK", 'I')
					})
				}
			}
		})
	}
}

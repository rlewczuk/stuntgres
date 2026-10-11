package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumFunctionsFeatureSmoke(t *testing.T) {
	enumFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		enumFunctionsSelect(t, p, b, conn, "SELECT enum_first(NULL::mood) AS v", "z", false, false)
		enumFunctionsSelect(t, p, b, conn, "SELECT enum_last('a'::mood) AS v", "m", false, true)
		enumFunctionsSelect(t, p, b, conn, "SELECT enum_range(NULL::mood) AS v", "{z,a,m}", true, false)
		q := "SELECT enum_range($1::mood,$2::mood) AS v"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			lo, hi any
			want   string
		}{{"z", "a", "{z,a}"}, {nil, "a", "{z,a}"}, {"a", nil, "{a,m}"}, {nil, nil, "{z,a,m}"}, {"a", "a", "{a}"}} {
			enumFunctionsSelect(t, p, b, held, q, tc.want, true, false, tc.lo, tc.hi)
		}
		fixture.EnumReferenceErrorChecked(t, p, conn, "SELECT enum_range(NULL::mood,NULL::other_mood)", true, "prepare", func(t *testing.T, got fixture.Diagnostic) {
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function enum_range(mood, other_mood) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}
			if got != want {
				t.Errorf("smoke diagnostic got %#v want %#v", got, want)
			}
		})
		enumFunctionsSelect(t, p, b, conn, "SELECT enum_first(NULL::mood) AS v", "z", false, false)
		enumFunctionsControls(t, p, b, conn)
	})
}

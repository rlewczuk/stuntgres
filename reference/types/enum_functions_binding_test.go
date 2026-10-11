package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumFunctionsBinding(t *testing.T) {
	enumFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		for _, query := range []string{
			"SELECT enum_range($1::mood,$2::mood) AS v",
			"SELECT enum_range($1,NULL::mood) AS v",
			"SELECT enum_range(NULL::mood,$1) AS v",
		} {
			t.Run(query, func(t *testing.T) {
				stmt, err := conn.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
				cases := []struct {
					args []any
					want string
				}{
					{[]any{"z", "a"}, "{z,a}"}, {[]any{"a", "m"}, "{a,m}"},
					{[]any{nil, "a"}, "{z,a}"}, {[]any{"a", nil}, "{a,m}"},
					{[]any{nil, nil}, "{z,a,m}"}, {[]any{"a", "a"}, "{a}"},
					{[]any{[]byte("z"), []byte("m")}, "{z,a,m}"},
				}
				if query == "SELECT enum_range($1,NULL::mood) AS v" {
					cases = []struct {
						args []any
						want string
					}{{[]any{"a"}, "{a,m}"}, {[]any{nil}, "{z,a,m}"}}
				} else if query == "SELECT enum_range(NULL::mood,$1) AS v" {
					cases = []struct {
						args []any
						want string
					}{{[]any{"a"}, "{z,a}"}, {[]any{nil}, "{z,a,m}"}}
				}
				for _, tc := range cases {
					enumFunctionsSelect(t, p, b, held, query, tc.want, true, false, tc.args...)
					enumFunctionsSelect(t, p, b, conn, query, tc.want, true, false, tc.args...)
				}
			})
		}
		for _, prepared := range []bool{false, true} {
			for _, expr := range []string{"enum_range('z',NULL::mood)", "enum_range(NULL::mood,'m')", "enum_range(NULL::mood_domain::mood)"} {
				enumFunctionsSelect(t, p, b, conn, "SELECT "+expr+" AS v", "{z,a,m}", true, prepared)
			}
			enumFunctionsSelect(t, p, b, conn, "SELECT enum_first($1::mood) AS v", "z", false, prepared, nil)
			enumFunctionsSelect(t, p, b, conn, "SELECT enum_last($1::mood) AS v", "m", false, prepared, []byte("a"))
		}
	})
}

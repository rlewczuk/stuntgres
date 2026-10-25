package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

// The same physical table and alias are a prohibited UPDATE target in one
// statement and an ordinary preceding FROM source in the other. Exercise the
// real parser/admission/binder, not a hand-built diagnostic namespace.
func TestUpdateFromValuesTargetAndSourceCorrelationDiagnostics(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(id INT,v INT)")
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	for _, prefix := range []string{"", "/*é*/ "} {
		for _, tc := range []struct{ name, query, token, code, message, detail, hint string }{
			{"target_qualified", "UPDATE t x SET v=q.n FROM (VALUES (x.id,7)) q(k,n)", "x.id", "42P01", `invalid reference to FROM-clause entry for table "x"`, `There is an entry for table "x", but it cannot be referenced from this part of the query.`, ""},
			{"source_qualified", "SELECT q.n FROM t x CROSS JOIN (VALUES (x.id,7)) q(k,n)", "x.id", "42P01", `invalid reference to FROM-clause entry for table "x"`, `There is an entry for table "x", but it cannot be referenced from this part of the query.`, "To reference that table, you must mark this subquery with LATERAL."},
			{"target_unqualified", "UPDATE t x SET v=q.n FROM (VALUES (id,7)) q(k,n)", "id", "42703", `column "id" does not exist`, `There is a column named "id" in table "x", but it cannot be referenced from this part of the query.`, ""},
			{"source_unqualified", "SELECT q.n FROM t x CROSS JOIN (VALUES (id,7)) q(k,n)", "id", "42703", `column "id" does not exist`, `There is a column named "id" in table "x", but it cannot be referenced from this part of the query.`, "To reference that column, you must mark this subquery with LATERAL."},
		} {
			t.Run(prefix+tc.name, func(t *testing.T) {
				q := prefix + tc.query
				_, err := e.Describe(op, enginetest.Statement(t, q))
				var actual *sqlstate.Error
				if !errors.As(err, &actual) {
					t.Fatal(err)
				}
				position := strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1)
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail, Hint: tc.hint, Position: position}
				if !reflect.DeepEqual(*actual, want) {
					t.Fatal("namespace-origin diagnostic", actual, want)
				}
			})
		}
	}
}

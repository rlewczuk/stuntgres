package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func endpointError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, want fixture.Diagnostic, first endpointSnapshot) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, query)
	if rows != nil {
		rows.Close()
		t.Fatal("failed query published rows")
	}
	fixture.AssertDiagnostic(t, err, &want)
	endpointSame(t, first, endpointCapture(t, p, b, c))
	stmt, err := c.PrepareContext(p.Ctx, query)
	if stmt != nil {
		stmt.Close()
		t.Fatal("failed prepare published statement")
	}
	fixture.AssertDiagnostic(t, err, &want)
	endpointSame(t, first, endpointCapture(t, p, b, c))
}
func endpointSignature(message string, position int) fixture.Diagnostic {
	want := activityDiagnostic("42883", message, position)
	want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	return want
}
func TestReferenceSessionEndpointsDiagnosticsAndCatalog(t *testing.T) {
	endpointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		first := endpointCapture(t, p, b, c)
		for _, name := range endpointNames {
			for _, tc := range []struct{ args, signature string }{{"1", "integer"}, {"true", "boolean"}, {"NULL", "unknown"}, {"'x'", "unknown"}, {"NULL::text", "text"}, {"NULL::inet", "inet"}, {"1,2", "integer, integer"}, {"x => 1", "x => integer"}} {
				endpointError(t, p, b, c, "SELECT "+name+"("+tc.args+")", endpointSignature("function "+name+"("+tc.signature+") does not exist", 8), first)
			}
			for _, tc := range []struct{ call, signature string }{{"public." + name + "()", "public." + name + "()"}, {"\"" + strings.ToUpper(name) + "\"()", strings.ToUpper(name) + "()"}, {name + "_missing()", name + "_missing()"}} {
				endpointError(t, p, b, c, "SELECT "+tc.call, endpointSignature("function "+tc.signature+" does not exist", 8), first)
			}
			endpointError(t, p, b, c, "SELECT 'λ', "+name+"(1)", endpointSignature("function "+name+"(integer) does not exist", 13), first)
			endpointError(t, p, b, c, "SELECT "+name+"(endpoint_missing_column)", activityDiagnostic("42703", "column \"endpoint_missing_column\" does not exist", 9+len(name)), first)
			for _, tc := range []struct{ suffix, message string }{{"(*)", name + "(*) specified, but " + name + " is not an aggregate function"}, {"() FILTER (WHERE true)", "FILTER specified, but " + name + " is not an aggregate function"}, {"() OVER ()", "OVER specified, but " + name + " is not a window function nor an aggregate function"}} {
				endpointError(t, p, b, c, "SELECT "+name+tc.suffix, activityDiagnostic("42809", tc.message, 8), first)
			}
		}
		query := `SELECT oid::bigint AS oid, proname::text AS name, pronargs::bigint AS nargs,
prorettype::bigint AS result, proisstrict AS strict, provolatile::text AS volatility,
proparallel::text AS parallel, proretset AS is_set, prorows::text AS rows,
procost::text AS cost, proargtypes::text AS inputs, proallargtypes::text AS alltypes,
proargmodes::text AS modes, proargnames::text AS names, pronargdefaults::bigint AS defaults,
prosecdef AS definer, proleakproof AS leakproof, provariadic::bigint AS variadic,
prosupport::oid::bigint AS support, prolang::bigint AS language, prosrc AS source
FROM pg_proc WHERE oid IN (2196,2197,2198,2199) ORDER BY oid`
		columns := []string{"oid", "name", "nargs", "result", "strict", "volatility", "parallel", "is_set", "rows", "cost", "inputs", "alltypes", "modes", "names", "defaults", "definer", "leakproof", "variadic", "support", "language", "source"}
		types := []string{"INT8", "TEXT", "INT8", "INT8", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT8", "BOOL", "BOOL", "INT8", "INT8", "INT8", "TEXT"}
		var rows [][]any
		for i, name := range endpointNames {
			result := int64(869)
			if i%2 == 1 {
				result = 23
			}
			rows = append(rows, []any{int64(2196 + i), name, int64(0), result, false, "s", "r", false, "0", "1", "", nil, nil, nil, int64(0), false, false, int64(0), int64(0), int64(12), name})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(query, columns, types, rows))
	})
}

func TestReferenceSessionEndpointsPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"endpoint_reader"})
	endpointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		first := endpointCapture(t, p, b, c)
		activityExec(t, p, c, "CREATE ROLE endpoint_reader")
		defer activityExec(t, p, c, "RESET ROLE")
		for _, name := range endpointNames {
			func() {
				query := "SELECT pg_catalog." + name + "()"
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				activityExec(t, p, c, "REVOKE EXECUTE ON FUNCTION pg_catalog."+name+"() FROM PUBLIC")
				defer func() {
					activityExec(t, p, c, "RESET ROLE")
					activityExec(t, p, c, "GRANT EXECUTE ON FUNCTION pg_catalog."+name+"() TO PUBLIC")
				}()
				activityExec(t, p, c, "SET ROLE endpoint_reader")
				want := activityDiagnostic("42501", "permission denied for function "+name, 0)
				rows, err := c.QueryContext(p.Ctx, query)
				if rows != nil {
					rows.Close()
					t.Fatal("denied query published rows")
				}
				fixture.AssertDiagnostic(t, err, &want)
				rows, err = stmt.QueryContext(p.Ctx)
				if rows != nil {
					rows.Close()
					t.Fatal("denied prepared query published rows")
				}
				fixture.AssertDiagnostic(t, err, &want)
				activityExec(t, p, c, "RESET ROLE")
				activityExec(t, p, c, "GRANT EXECUTE ON FUNCTION pg_catalog."+name+"() TO PUBLIC")
				activityExec(t, p, c, "SET ROLE endpoint_reader")
				rows, err = stmt.QueryContext(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				if !rows.Next() {
					t.Fatalf("granted query missing row %v", rows.Err())
				}
				var got any
				if err := rows.Scan(&got); err != nil {
					t.Fatal(err)
				}
				index := 0
				for i, n := range endpointNames {
					if n == name {
						index = i
					}
				}
				if data, ok := got.([]byte); ok {
					got = string(data)
				}
				if got != first[index] {
					t.Fatalf("granted endpoint %#v want %#v", got, first[index])
				}
				if rows.Next() || rows.Err() != nil || rows.NextResultSet() {
					t.Fatalf("granted trailing result %v", rows.Err())
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
			}()
		}
	})
}

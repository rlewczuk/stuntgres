package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent REL_18_6/manual examples. Packed comparisons, never short output,
// witness the invalid-input flag and the ISSN publication digits.
var isnConversionRoles = []struct {
	name, input, compact, output, low, high string
}{
	{"ean13", "9781234567897", "9781234567897", "978-1-234-56789-7", "0000000000000", "9999999999994"},
	{"isbn", "123456789X", "9781234567897", "1-234-56789-X", "9780000000002", "9781234567897"},
	{"isbn13", "9781234567897", "9781234567897", "978-1-234-56789-7", "9780000000002", "9781234567897"},
	{"ismn", "M123456785", "9790123456785", "M-1234-5678-5", "9790000000001", "9790123456785"},
	{"ismn13", "9790123456785", "9790123456785", "979-0-1234-5678-5", "9790000000001", "9790123456785"},
	{"issn", "12345679", "9771234567003", "1234-5679", "9771234567003", "9771234567898"},
	{"issn13", "9771234567003", "9771234567003", "977-1234-567-00-3", "9771234567003", "9771234567898"},
	{"upc", "123456789012", "0123456789012", "123456789012", "0000000000000", "0123456789012"},
}

var isnConversionOps = []struct {
	operator, routine, body, commutator, negator, restriction, join string
}{
	{"=", "isneq", "int8eq", "=", "<>", "eqsel", "eqjoinsel"},
	{"<>", "isnne", "int8ne", "<>", "=", "neqsel", "neqjoinsel"},
	{"<", "isnlt", "int8lt", ">", ">=", "scalarltsel", "scalarltjoinsel"},
	{"<=", "isnle", "int8le", ">=", ">", "scalarlesel", "scalarlejoinsel"},
	{">", "isngt", "int8gt", "<", "<=", "scalargtsel", "scalargtjoinsel"},
	{">=", "isnge", "int8ge", "<=", "<", "scalargesel", "scalargejoinsel"},
}

func isnConversionPairs() [][2]int {
	pairs := make([][2]int, 0, 28)
	for i := range isnConversionRoles {
		pairs = append(pairs, [2]int{i, i})
		if i != 0 {
			pairs = append(pairs, [2]int{0, i}, [2]int{i, 0})
		}
	}
	for _, pair := range [][2]int{{1, 2}, {3, 4}, {5, 6}} {
		pairs = append(pairs, pair, [2]int{pair[1], pair[0]})
	}
	return pairs
}

func isnConversionEdges() [][2]int {
	edges := make([][2]int, 0, 20)
	for i := 1; i < len(isnConversionRoles); i++ {
		edges = append(edges, [2]int{0, i}, [2]int{i, 0})
	}
	for _, pair := range [][2]int{{1, 2}, {3, 4}, {5, 6}} {
		edges = append(edges, pair, [2]int{pair[1], pair[0]})
	}
	return edges
}

func isnConversionLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func isnConversionRoleList() string {
	names := make([]string, len(isnConversionRoles))
	for i, role := range isnConversionRoles {
		names[i] = isnConversionLiteral(role.name)
	}
	return "(" + strings.Join(names, ",") + ")"
}

func isnConversionScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	for _, schema := range []string{"eo_old", "eo_new", "eo_other"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
				defer cancel()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path"} {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Error("ISN session cleanup", q, err)
					}
				}
			}()
			body(t, p, backend, c)
		})
	}
}

func isnConversionRun(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, b.Recorder, s) }
}

func isnConversionRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	run := isnConversionRun(t, p, b, c)
	run(s)
	run(fixture.Prepared(s))
	held := fixture.IdentityHeld(t, p.Ctx, c, s.Query)
	defer held.Statement.Close()
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
}

func isnConversionDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func isnConversionRuntimeError(query string, d fixture.Diagnostic, args ...any) fixture.Step {
	s := fixture.ErrorDiagnostic(query, d, args...)
	s.ErrorStage = "execute"
	return s
}

func isnConversionIDs(q string, ids ...int64) fixture.Step {
	rows := make([][]any, len(ids))
	for i, id := range ids {
		rows[i] = []any{id}
	}
	return fixture.Select(q, []string{"id"}, []string{"INT4"}, rows)
}

func isnConversionComparison(left, right string, sign int, null bool, args ...any) fixture.Step {
	parts, names, types, expected := []string{}, []string{}, []string{}, []any{}
	truth := []bool{sign == 0, sign != 0, sign < 0, sign <= 0, sign > 0, sign >= 0}
	for i, op := range isnConversionOps {
		for j, expression := range []string{left + " " + op.operator + " " + right, op.routine + "(" + left + "," + right + ")"} {
			alias := "v" + strconv.Itoa(2*i+j)
			parts = append(parts, expression+" AS "+alias)
			names, types = append(names, alias), append(types, "BOOL")
			var value any = truth[i]
			if null {
				value = nil
			}
			expected = append(expected, value)
		}
	}
	return fixture.Select("SELECT "+strings.Join(parts, ","), names, types, [][]any{expected}, args...)
}

func isnConversionAssertDump(t *testing.T, want, got string) {
	t.Helper()
	before, after := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range min(len(before), len(after)) {
		if before[i] != after[i] {
			t.Fatalf("canonical dump differs at line %d\nbefore: %s\nafter: %s", i+1, before[i], after[i])
		}
	}
	if want != got {
		t.Fatalf("canonical dump line count changed: before %d, after %d", len(before), len(after))
	}
}

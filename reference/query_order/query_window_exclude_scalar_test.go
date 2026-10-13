package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func queryWindowExcludeScalarScenario(t *testing.T, body func(*fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		body(p, b)
	}
}

// R2-F1: separately analyzed child queries must not prevent SQL99 matching
// between the selected SUM target and the repeated DISTINCT ORDER expression.
func TestReferenceQueryWindowExcludeScalarDistinctOrder(t *testing.T) {
	queryWindowExcludeScalarScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowExcludeSeed(t, p, b)
		q := "SELECT DISTINCT sum((SELECT 1)) OVER w AS s FROM ex_t WINDOW w AS (ROWS CURRENT ROW EXCLUDE TIES) ORDER BY sum((SELECT 1)) OVER w"
		queryWindowRoutes(t, p, b, fixture.Select(q, []string{"s"}, []string{"INT8"}, [][]any{{int64(1)}}))
	})
}

// SQL92 duplicate-label resolution admits equal targets, while each selected
// SUM still owns its execution occurrence rather than sharing a result slot.
func TestReferenceQueryWindowExcludeScalarDuplicateLabel(t *testing.T) {
	queryWindowExcludeScalarScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowExcludeSeed(t, p, b)
		q := "SELECT sum((SELECT 1)) OVER w AS s, sum((SELECT 1)) OVER w AS s FROM ex_t WINDOW w AS (ROWS CURRENT ROW EXCLUDE TIES) ORDER BY s"
		queryWindowRoutes(t, p, b, fixture.Select(q, []string{"s", "s"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(1)}, {int64(1), int64(1)}}))
	})
}

func queryWindowExcludeScalarUnequal(t *testing.T, p *fixture.Pair, b fixture.Backend, left, right, source string, values [][]any) {
	t.Helper()
	w := " WINDOW w AS (ROWS CURRENT ROW EXCLUDE TIES)"
	for _, tc := range []struct {
		q, code, message, marker string
	}{
		{
			"SELECT DISTINCT sum(" + left + ") OVER w AS s FROM " + source + w + " ORDER BY sum(" + right + ") OVER w",
			"42P10", "for SELECT DISTINCT, ORDER BY expressions must appear in select list", "sum(",
		},
		{
			"SELECT sum(" + left + ") OVER w AS s, sum(" + right + ") OVER w AS s FROM " + source + w + " ORDER BY s",
			"42702", `ORDER BY "s" is ambiguous`, "s",
		},
	} {
		for _, prefix := range []string{"", "/*é🙂*/ "} {
			// The failing ORDER item owns the caret; prefix lengths are characters,
			// not UTF-8 bytes. ImplicitError checks the complete logical record.
			position := len([]rune(prefix+tc.q[:strings.LastIndex(tc.q, tc.marker)])) + 1
			s := fixture.ImplicitError(prefix+tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: fixture.Position(position)})
			s.SelectRows = true
			queryWindowRoutes(t, p, b, s)
		}
	}
	// Legal distinct labels independently expose both results. An equality fix
	// must not erase argument differences or collapse selected execution slots.
	q := "SELECT sum(" + left + ") OVER w AS a, sum(" + right + ") OVER w AS b FROM " + source + w + " ORDER BY b,a"
	queryWindowRoutes(t, p, b, fixture.Select(q, []string{"a", "b"}, []string{"INT8", "INT8"}, values))
}

func TestReferenceQueryWindowExcludeScalarUnequalTargets(t *testing.T) {
	queryWindowExcludeScalarScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowExcludeSeed(t, p, b)
		queryWindowExcludeScalarUnequal(t, p, b, "(SELECT 1)", "(SELECT 2)", "ex_t", [][]any{{int64(1), int64(2)}, {int64(1), int64(2)}, {int64(1), int64(2)}})
	})
}

func TestReferenceQueryWindowExcludeScalarCorrelationCoordinates(t *testing.T) {
	queryWindowExcludeScalarScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowExcludeSeed(t, p, b)
		argument := "(SELECT q.v FROM ex_t AS q WHERE q.id=e.id)"
		w := " WINDOW w AS (ROWS CURRENT ROW EXCLUDE TIES)"
		q := "SELECT DISTINCT sum(" + argument + ") OVER w AS s FROM ex_t AS e" + w + " ORDER BY sum(" + argument + ") OVER w"
		queryWindowRoutes(t, p, b, fixture.Select(q, []string{"s"}, []string{"INT8"}, [][]any{{int64(10)}, {int64(20)}, {int64(30)}}))
		q = "SELECT sum(" + argument + ") OVER w AS s, sum(" + argument + ") OVER w AS s FROM ex_t AS e" + w + " ORDER BY s"
		queryWindowRoutes(t, p, b, fixture.Select(q, []string{"s", "s"}, []string{"INT8", "INT8"}, [][]any{{int64(10), int64(10)}, {int64(20), int64(20)}, {int64(30), int64(30)}}))
		// Both child sources/predicates and column slots agree, but q.v is a
		// child coordinate and e.v is an outer correlation. They are unequal
		// even on the first row where both happen to return 10.
		queryWindowExcludeScalarUnequal(t, p, b,
			"(SELECT q.v FROM ex_t AS q WHERE q.id=1)",
			"(SELECT e.v FROM ex_t AS q WHERE q.id=1)",
			"ex_t AS e", [][]any{{int64(10), int64(10)}, {int64(10), int64(20)}, {int64(10), int64(30)}})
	})
}

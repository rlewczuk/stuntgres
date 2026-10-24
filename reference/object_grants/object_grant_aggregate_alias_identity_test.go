package reference_test

import (
	"database/sql"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

// Equal aggregate values do not make the selected SQL declarations identical.
type aggregateAliasFamily struct {
	names [2]string
	arg, signature, typ, sinkType string
	value, filtered any
}

var aggregateAliasFamilies = []aggregateAliasFamily{
	{[2]string{"bool_and", "every"}, "b", "boolean", "BOOL", "BOOLEAN", false, true},
	{[2]string{"variance", "var_samp"}, "x", "double precision", "FLOAT8", "DOUBLE PRECISION", float64(4), float64(2)},
	{[2]string{"stddev", "stddev_samp"}, "x", "double precision", "FLOAT8", "DOUBLE PRECISION", float64(2), math.Sqrt(2)},
}

var aggregateAliasShapes = []string{"plain", "all", "distinct", "ordered", "filter", "combined"}

func aggregateAliasCall(name, arg, shape string) string {
	switch shape {
	case "all":
		return name + "(ALL " + arg + ")"
	case "distinct":
		return name + "(DISTINCT " + arg + ")"
	case "ordered":
		return name + "(" + arg + " ORDER BY " + arg + " DESC NULLS FIRST)"
	case "filter":
		return name + "(" + arg + ") FILTER(WHERE id<=2)"
	case "combined":
		return name + "(DISTINCT " + arg + " ORDER BY " + arg + " DESC NULLS FIRST) FILTER(WHERE id<=3)"
	default:
		return name + "(" + arg + ")"
	}
}

func (f aggregateAliasFamily) projection(order int, shape string) string {
	return aggregateAliasCall(f.names[order], f.arg, shape) + " AS a," + aggregateAliasCall(f.names[1-order], f.arg, shape) + " AS z"
}

func (f aggregateAliasFamily) selectStep(query, shape string) fixture.Step {
	value := f.value
	if shape == "filter" {
		value = f.filtered
	}
	return fixture.Select(query, []string{"a", "z"}, []string{f.typ, f.typ}, [][]any{{value, value}})
}

func aggregateAliasInput(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE og_ai_input(id INTEGER,b BOOLEAN,x DOUBLE PRECISION)", 0))
	run(fixture.Exec("INSERT INTO og_ai_input VALUES(1,TRUE,1),(2,TRUE,3),(3,FALSE,5),(4,NULL,NULL)", 4))
	aggregateAliasAccess(run)
}

func aggregateAliasAccess(run func(fixture.Step)) {
	run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader,og_object_other", 0))
	run(fixture.Exec("GRANT SELECT ON og_ai_input TO og_object_reader,og_object_other", 0))
}

func aggregateAliasMatrix(t *testing.T, shapes []string, body func(*testing.T, aggregateAliasFamily, int, int, string)) {
	t.Helper()
	for _, family := range aggregateAliasFamilies {
		for order := range 2 {
			for revoked := range 2 {
				for _, shape := range shapes {
					t.Run(family.names[order]+"_then_"+family.names[1-order]+"/revoke_"+family.names[revoked]+"/"+shape, func(t *testing.T) {
						body(t, family, order, revoked, shape)
					})
				}
			}
		}
	}
}

func aggregateAliasControl(f aggregateAliasFamily, revoked int, shape string) fixture.Step {
	value := f.value
	if shape == "filter" {
		value = f.filtered
	}
	return fixture.Select("SELECT "+aggregateAliasCall(f.names[1-revoked], f.arg, shape)+" AS v FROM og_ai_input", []string{"v"}, []string{f.typ}, [][]any{{value}})
}

func TestReferenceObjectGrantAggregateAliasIdentity(t *testing.T) {
	aggregateAliasMatrix(t, aggregateAliasShapes, func(t *testing.T, f aggregateAliasFamily, order, revoked int, shape string) {
		objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
			aggregateAliasInput(run)
			query := "SELECT " + f.projection(order, shape) + " FROM og_ai_input"
			cases := []objectGrantProviderCase{{f.selectStep(query, shape), true, true}}
			// Repeated written declarations must retain the same values and metadata.
			repeat := "SELECT " + aggregateAliasCall(f.names[order], f.arg, shape) + " AS a," + aggregateAliasCall(f.names[order], f.arg, shape) + " AS z FROM og_ai_input"
			cases = append(cases, objectGrantProviderCase{f.selectStep(repeat, shape), revoked == order, true})
			for _, empty := range []string{
				query + " WHERE id>99",
				"SELECT " + f.names[order] + "(" + f.arg + ") FILTER(WHERE FALSE) AS a," + f.names[1-order] + "(" + f.arg + ") FILTER(WHERE FALSE) AS z FROM og_ai_input",
			} {
				cases = append(cases, objectGrantProviderCase{fixture.Select(empty, []string{"a", "z"}, []string{f.typ, f.typ}, [][]any{{nil, nil}}), true, true})
			}
			objectGrantProviderLifecycle(t, p, b, c, run, "pg_catalog."+f.names[revoked]+"("+f.signature+")", "aggregate", f.names[revoked], cases, []fixture.Step{aggregateAliasControl(f, revoked, shape)})
		})
	})
}

func TestReferenceObjectGrantAggregateAliasFeatureSmoke(t *testing.T) {
	aggregateAliasMatrix(t, []string{"combined"}, func(t *testing.T, f aggregateAliasFamily, order, revoked int, shape string) {
		aggregateAliasStoredScenario(t, f, order, revoked, shape, false)
		aggregateAliasPublicationScenario(t, f, order, revoked, shape)
	})
}

package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHypotheticalAggregateTypes(t *testing.T) {
	hypotheticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
		for _, tc := range []struct{ typ, value string }{
			{"boolean", "true"}, {"character", "a"}, {"bytea", "\\x00ff"}, {"date", "2020-01-01"},
			{"real", "1"}, {"double precision", "1"}, {"smallint", "1"}, {"integer", "1"}, {"bigint", "1"}, {"money", "1"}, {"interval", "1 day"}, {"name", "a"}, {"numeric", "1"},
			{"oid", "1"}, {"regclass", "pg_class"}, {"regtype", "integer"}, {"regproc", "1"}, {"regprocedure", "1"}, {"regrole", "1"}, {"regoper", "1"}, {"regoperator", "1"},
			{"regcollation", `pg_catalog."C"`}, {"regnamespace", "pg_catalog"}, {"regconfig", "-"}, {"regdictionary", "-"},
			{"text", "a"}, {"character varying", "a"}, {"time without time zone", "01:02:03"}, {"timestamp without time zone", "2020-01-01 01:02:03"}, {"timestamp with time zone", "2020-01-01 01:02:03+00"}, {"time with time zone", "01:02:03+00"},
			{"uuid", "00000000-0000-0000-0000-000000000001"}, {"jsonb", "{}"}, {"inet", "10.0.0.1"}, {"cidr", "10.0.0.0/24"}, {"xid8", "1"},
		} {
			t.Run(tc.typ, func(t *testing.T) {
				value := "'" + tc.value + "'::" + tc.typ
				orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection(value, value, ""), hypotheticalRow(1, 1, 0, 1)))
				orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("NULL::"+tc.typ, value, ""), hypotheticalRow(2, 2, 1, 1)))
			})
		}
		for _, tc := range []struct {
			direct, values string
			row            []any
		}{
			{"1.00::numeric", "(1::numeric),(1.0),(1.00),(2)", hypotheticalRow(1, 1, 0, .8)},
			{"'a'::char(3)", "('a'::char(3)),('a '::char(3)),('b')", hypotheticalRow(1, 1, 0, .75)},
			{"'-0'::float8", "('0'::float8),('-0'),(1)", hypotheticalRow(1, 1, 0, .75)},
			{"'NaN'::float8", "(1::float8),('NaN'),('NaN')", hypotheticalRow(2, 2, 1.0/3, 1)},
		} {
			orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection(tc.direct, "v", "")+" FROM (VALUES"+tc.values+") AS a(v)", tc.row))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TYPE hs_type_enum AS ENUM('z','a')", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN hs_type_domain AS INT", 0))
		for _, direct := range []string{"2::hs_type_domain", "2::int", "'2'"} {
			orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection(direct, "v", "")+" FROM (VALUES(1::hs_type_domain),(2::hs_type_domain),(2::hs_type_domain)) AS a(v)", hypotheticalRow(2, 2, 1.0/3, 1)))
		}
		orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("'a'::hs_type_enum", "v", "")+" FROM (VALUES('z'::hs_type_enum),('a'),('a')) AS a(v)", hypotheticalRow(2, 2, 1.0/3, 1)))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE hs_type_char(id INT)", 0))
		orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("relkind", "relkind", "")+" FROM pg_catalog.pg_class WHERE relname='hs_type_char' GROUP BY relkind", hypotheticalRow(1, 1, 0, 1)))
		// PostgreSQL has more ordering providers; selected hypothetical forms do
		// not silently expand the native aggregate ordering capability boundary.
		for _, typ := range []string{"int[]", "int4range", "bit(2)"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE hs_boundary(v "+typ+")", 0))
			// Resolve the direct NULL from the typed column carrier, without
			// requiring an independently unsupported explicit range/BIT cast.
			q := "SELECT rank(NULL) WITHIN GROUP(ORDER BY v) AS v FROM hs_boundary"
			if b.PG {
				orderedSetBoth(t, p, c, b, orderedSetValue(q, "INT8", int64(1)))
			} else {
				name := typ
				if typ == "int[]" {
					name = "integer[]"
				}
				if typ == "bit(2)" {
					name = "bit"
				}
				orderedSetBoth(t, p, c, b, orderedSetDiagnostic(q, "0A000", "aggregate ordering is unsupported for type "+name, "v) AS", ""))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE hs_boundary", 0))
		}
	})
}

func TestReferenceHypotheticalAggregateCollations(t *testing.T) {
	hypotheticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
		for _, collation := range []string{"C", "POSIX"} {
			orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("1,'a' COLLATE \""+collation+"\"", "x,y", "")+" FROM (VALUES(1,'A'::text),(1,'Z'),(1,'a'),(2,'a')) AS a(x,y)", hypotheticalRow(3, 3, .5, .8)))
		}
		orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("'a' COLLATE \"C\",'b' COLLATE \"POSIX\"", "x,y", "")+" FROM (VALUES('a'::text,'a'::text),('a','b'),('a','b'),('b','a')) AS a(x,y)", hypotheticalRow(2, 2, .25, .8)))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE hs_collation(d TEXT COLLATE \"C\",s TEXT COLLATE \"POSIX\")", 0))
		q := "SELECT rank(d) WITHIN GROUP(ORDER BY s) FROM hs_collation GROUP BY d"
		s := orderedSetDiagnostic(q, "42P21", "collation mismatch between implicit collations \"C\" and \"POSIX\"", "s)", "You can choose the collation by applying the COLLATE clause to one or both expressions.")
		orderedSetBoth(t, p, c, b, s)
		orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("d COLLATE \"C\"", "s", "")+" FROM hs_collation GROUP BY d"))
	})
}

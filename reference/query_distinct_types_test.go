package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceQueryDistinctTypes(t *testing.T) {
	queryDistinctScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		// Quotient counts intentionally avoid choosing a representative spelling
		// for SQL-equal NUMERIC scales, BPCHAR padding, NaNs or signed zero.
		for i, tc := range []struct {
			typ, values string
			count       int64
		}{
			{"NUMERIC", "(1.0),(1.00),(1.000),(2),(NULL),(NULL)", 3},
			{"REAL", "('0'),('-0'),('NaN'),('NaN'),('1'),(NULL),(NULL)", 4},
			{"DOUBLE PRECISION", "('0'),('-0'),('NaN'),('NaN'),('1'),(NULL),(NULL)", 4},
			{"CHAR(4)", "('a'),('a '),('a  '),('A'),(NULL),(NULL)", 3},
			{"TEXT", "('a'),('a'),('a '),('A'),(NULL),(NULL)", 4},
			{"BYTEA", "('\\x00'),('\\x00'),('\\x0000'),('\\xff'),(NULL),(NULL)", 4},
			{"UUID", "('00000000-0000-0000-0000-000000000001'),('00000000-0000-0000-0000-000000000001'),('00000000-0000-0000-0000-000000000002'),(NULL),(NULL)", 3},
			{"OID", "(1),(1),(2),(NULL),(NULL)", 3},
			{"REGCLASS", "('pg_class'),('pg_catalog.pg_class'),('pg_attrdef'),(NULL),(NULL)", 3},
			{"INTERVAL", "('1 month'),('30 days'),('1 day'),(NULL),(NULL)", 3},
			{"TIMETZ", "('09:00+01'),('08:00+00'),('09:00+01'),(NULL),(NULL)", 3},
			{"DATE", "('2001-01-01'),('2001-01-01'),('2001-01-02'),(NULL),(NULL)", 3},
			{"TIME", "('09:00'),('09:00'),('10:00'),(NULL),(NULL)", 3},
			{"TIMESTAMP", "('2001-01-01 09:00'),('2001-01-01 09:00'),('2001-01-02 09:00'),(NULL),(NULL)", 3},
			{"TIMESTAMPTZ", "('2001-01-01 09:00+01'),('2001-01-01 08:00+00'),('2001-01-02 08:00+00'),(NULL),(NULL)", 3},
			{"SMALLINT", "(1),(1),(2),(NULL),(NULL)", 3},
			{"BIGINT", "(1),(1),(2),(NULL),(NULL)", 3},
			{"BOOLEAN", "(TRUE),(TRUE),(FALSE),(NULL),(NULL)", 3},
			{"NAME", "('a'),('a'),('A'),(NULL),(NULL)", 3},
			{"VARCHAR(5)", "('a'),('a'),('a '),(NULL),(NULL)", 3},
		} {
			t.Run(tc.typ, func(t *testing.T) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
				table := fmt.Sprintf("scalar_%d", i)
				view := table + "_distinct"
				stored, projection, values := tc.typ, "v", tc.values
				if tc.typ == "REGCLASS" {
					// Literal REGCLASS providers are admitted; TEXT-column casts and
					// REGCLASS storage are separate, unsupported prerequisites.
					stored = "INT"
					values = "(1),(2),(3),(NULL),(NULL)"
					projection = "CASE v WHEN 1 THEN 'pg_class'::regclass WHEN 2 THEN 'pg_catalog.pg_class'::regclass WHEN 3 THEN 'pg_attrdef'::regclass ELSE NULL::regclass END AS v"
				}
				run(referenceExec("CREATE TABLE "+table+"(v "+stored+")", 0))
				run(referenceExec("INSERT INTO "+table+" VALUES"+values, int64(1+strings.Count(values, "),("))))
				run(referenceExec("CREATE VIEW "+view+" AS SELECT DISTINCT "+projection+" FROM "+table, 0))
				queryDistinctBoth(run, referenceSelect("SELECT count(*) AS n FROM "+view, []string{"n"}, []string{"INT8"}, [][]any{{tc.count}}))
				queryDistinctBoth(run, referenceSelect("SELECT count(*) AS n FROM "+view+" WHERE v IS NULL", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
				first := tc.values[1:strings.Index(tc.values, ")")]
				if tc.typ == "REGCLASS" {
					first += "::regclass"
				}
				queryDistinctBoth(run, referenceSelect("SELECT count(*) AS n FROM "+view+" WHERE v="+first, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			})
		}
		for _, collation := range []string{"default", "C", "POSIX", "pg_c_utf8", "pg_unicode_fast"} {
			s := referenceSelect("SELECT DISTINCT v COLLATE \""+collation+"\" AS v FROM scalar_4", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {"a "}, {"A"}, {nil}})
			s.bag = true
			queryDistinctBoth(run, s)
		}
		run(referenceExec("CREATE DOMAIN distinct_amount AS NUMERIC(7,2)", 0))
		run(referenceExec("CREATE TABLE domain_scalar(v distinct_amount)", 0))
		run(referenceExec("INSERT INTO domain_scalar VALUES(1),(1),(2),(NULL),(NULL)", 5))
		s := referenceSelect("SELECT DISTINCT v FROM domain_scalar ORDER BY v", []string{"v"}, []string{"NUMERIC"}, [][]any{{"1.00"}, {"2.00"}, {nil}})
		queryDistinctBoth(run, s)
	})
}

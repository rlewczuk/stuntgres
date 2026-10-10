package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceAggregateModifiersTypedDistinct(t *testing.T) {
	aggregateModifiersScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		for _, tc := range []struct{ name, typ, values string }{
			{"numeric", "NUMERIC", "(1),(1.0),(1.00),(NULL)"},
			{"float", "FLOAT8", "('0'),('-0'),(NULL)"},
			{"nan", "FLOAT8", "('NaN'),('NaN'),(NULL)"},
			{"bpchar", "CHAR(4)", "('a'),('a '),('a   '),(NULL)"},
			{"interval", "INTERVAL", "('1 day'),('24 hours'),(NULL)"},
			{"bool", "BOOL", "(TRUE),(TRUE),(NULL)"},
			{"date", "DATE", "('2020-01-01'),('2020-01-01'),(NULL)"},
			{"uuid", "UUID", "('00000000-0000-0000-0000-000000000001'),('00000000-0000-0000-0000-000000000001'),(NULL)"},
			{"bytes", "BYTEA", "('\\x00ff'),('\\x00ff'),(NULL)"},
			{"int2", "SMALLINT", "(1),(1),(NULL)"},
			{"int4", "INTEGER", "(1),(1),(NULL)"},
			{"int8", "BIGINT", "(1),(1),(NULL)"},
			{"float4", "REAL", "('NaN'),('NaN'),(NULL)"},
			{"text", "TEXT", "('a'),('a'),(NULL)"},
			{"varchar", "VARCHAR(4)", "('a'),('a'),(NULL)"},
			{"name", "NAME", "('a'),('a'),(NULL)"},
			{"oid", "OID", "(1),(1),(NULL)"},
			{"time", "TIME", "('01:02:03'),('01:02:03'),(NULL)"},
			{"timetz", "TIMETZ", "('01:02:03+00'),('01:02:03+00'),(NULL)"},
			{"timestamp", "TIMESTAMP", "('2020-01-01 01:02:03'),('2020-01-01 01:02:03'),(NULL)"},
			{"timestamptz", "TIMESTAMPTZ", "('2020-01-01 01:02:03+00'),('2020-01-01 01:02:03+00'),(NULL)"},
		} {
			name := "am_type_" + tc.name
			run(referenceExec("CREATE TABLE "+name+"(v "+tc.typ+")", 0))
			run(referenceExec("INSERT INTO "+name+" VALUES"+tc.values, int64(lenTypeModifierRows(tc.name))))
			aggregateModifiersBoth(run, referenceSelect("SELECT count(DISTINCT v ORDER BY v) FROM "+name, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		}
		run(referenceExec("CREATE DOMAIN am_domain AS NUMERIC", 0))
		run(referenceExec("CREATE TYPE am_enum AS ENUM('a','b')", 0))
		run(referenceExec("CREATE TABLE am_owned(d am_domain,e am_enum)", 0))
		run(referenceExec("INSERT INTO am_owned VALUES(1,'a'),(1.00,'a'),(NULL,NULL)", 3))
		aggregateModifiersBoth(run, referenceSelect("SELECT count(DISTINCT d ORDER BY d),count(DISTINCT e ORDER BY e) FROM am_owned", []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}}))
	})
}

func lenTypeModifierRows(name string) int {
	switch name {
	case "numeric", "bpchar":
		return 4
	default:
		return 3
	}
}

func TestReferenceAggregateModifiersCarrierLimits(t *testing.T) {
	aggregateModifiersScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE am_arrays(v INT[])", 0))
		run(referenceExec("INSERT INTO am_arrays VALUES('{1,2}'),('{1,2}'),(NULL)", 3))
		run(referenceExec("CREATE TABLE am_tree(v INT DEFAULT 1)", 0))
		run(referenceExec("CREATE VIEW am_tree_carrier AS SELECT d.adbin FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='am_tree'", 0))
		run(referenceExec("CREATE TABLE am_repeat(id INT)", 0))
		run(referenceExec("INSERT INTO am_repeat VALUES(1),(2)", 2))
		run(referenceExec("CREATE DOMAIN am_array_domain AS INT[]", 0))
		for _, tc := range []struct {
			q, typ, token string
			want          int64
			distinct      bool
		}{
			{"SELECT count(DISTINCT v) FROM am_arrays", "integer[]", "v)", 1, true},
			{"SELECT count(v ORDER BY v) FROM am_arrays", "integer[]", "v)", 2, false},
			{"SELECT count(DISTINCT v) FILTER(WHERE FALSE) FROM am_arrays", "integer[]", "v)", 0, true},
			{"SELECT count(DISTINCT v) FROM am_arrays WHERE FALSE", "integer[]", "v)", 0, true},
			{"SELECT count(DISTINCT NULL::integer[])", "integer[]", "NULL", 0, true},
			{"SELECT count(v ORDER BY v) FILTER(WHERE FALSE) FROM am_arrays", "integer[]", "v)", 0, false},
			{"SELECT count(v ORDER BY v) FROM am_arrays WHERE FALSE", "integer[]", "v)", 0, false},
			{"SELECT count(DISTINCT NULL::am_array_domain)", "integer[]", "NULL", 0, true},
			{"SELECT count(DISTINCT adbin) FROM am_tree_carrier", "pg_node_tree", "adbin)", 1, true},
			{"SELECT count(adbin ORDER BY adbin) FROM am_tree_carrier", "pg_node_tree", "adbin)", 1, false},
			{"SELECT count(DISTINCT NULL::pg_node_tree)", "pg_node_tree", "NULL", 0, true},
			{"SELECT count(DISTINCT adbin) FROM am_tree_carrier CROSS JOIN am_repeat", "pg_node_tree", "adbin)", 1, true},
			{"SELECT count(DISTINCT adbin) FILTER(WHERE FALSE) FROM am_tree_carrier", "pg_node_tree", "adbin)", 0, true},
			{"SELECT count(DISTINCT adbin) FROM pg_catalog.pg_attrdef WHERE FALSE", "pg_node_tree", "adbin)", 0, true},
			{"SELECT count(adbin ORDER BY adbin) FILTER(WHERE FALSE) FROM am_tree_carrier", "pg_node_tree", "adbin)", 0, false},
			{"SELECT count(adbin ORDER BY adbin) FROM pg_catalog.pg_attrdef WHERE FALSE", "pg_node_tree", "adbin)", 0, false},
		} {
			s := referenceSelect(tc.q, []string{"count"}, []string{"INT8"}, [][]any{{tc.want}})
			if !b.pg {
				action := "ordering"
				if tc.distinct {
					action = "DISTINCT"
				}
				s = aggregateModifiersError(tc.q, "0A000", "aggregate "+action+" is unsupported for type "+tc.typ, tc.token)
			}
			aggregateModifiersBoth(run, s)
		}
		aggregateModifiersBoth(run, referenceSelect("SELECT count(DISTINCT adbin::text ORDER BY adbin::text) FROM am_tree_carrier", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		for _, tc := range []struct{ q, msg, token string }{
			{"SELECT count(DISTINCT NULL::json)", "could not identify an equality operator for type json", "NULL"},
			{"SELECT count(DISTINCT setseed(0))", "could not identify an equality operator for type void", "setseed"},
		} {
			aggregateModifiersBoth(run, aggregateModifiersError(tc.q, "42883", tc.msg, tc.token))
		}
		q := "SELECT count(NULL::json ORDER BY NULL::json)"
		s := aggregateModifiersError(q, "42883", "could not identify an ordering operator for type json", "NULL::json)")
		s.diagnostic.Hint = "Use an explicit ordering operator or modify the query."
		aggregateModifiersBoth(run, s)
	})
}

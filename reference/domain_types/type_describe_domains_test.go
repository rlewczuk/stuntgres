package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTypeDescribeDomains(t *testing.T) {
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		typeDescribeGraph(run)
		typeDescribeGraphProof(run)
		typeDescribeValue(run, "pg_basetype('td.outer_domain'::regtype)", "REGTYPE", "numeric")
		typeDescribeValue(run, "pg_basetype(23::oid::regtype)", "REGTYPE", "integer")
		for _, v := range []string{"NULL", "NULL::regtype", "0::oid::regtype", "4294967295::oid::regtype"} {
			typeDescribeValue(run, "pg_basetype("+v+")", "REGTYPE", nil)
		}
		typeDescribeValue(run, "pg_basetype('td.names'::regtype)", "REGTYPE", "name[]")
		typeDescribeValue(run, "pg_basetype('td.outer_domain[]'::regtype)::oid='td.outer_domain[]'::regtype::oid", "BOOL", true)
		typeDescribeValue(run, "pg_typeof(1.23::td.outer_domain)::oid='td.outer_domain'::regtype::oid", "BOOL", true)
		typeDescribeBoth(run, fixture.Select("SELECT typbasetype='td.inner_domain'::regtype::oid AS immediate,typtype::text AS kind FROM pg_type WHERE oid='td.outer_domain'::regtype::oid", []string{"immediate", "kind"}, []string{"BOOL", "TEXT"}, [][]any{{true, "d"}}))
		typeDescribeBoth(run, fixture.Select("SELECT typbasetype=1700::oid AS immediate,typtypmod FROM pg_type WHERE oid='td.inner_domain'::regtype::oid", []string{"immediate", "typtypmod"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(327686)}}))
	})
}

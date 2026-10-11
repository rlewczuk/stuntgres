package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAggregateModifiersFilterDemand(t *testing.T) {
	aggregateModifiersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE am_demand(id INT,v TEXT)", 0))
		run(fixture.Exec("INSERT INTO am_demand VALUES(1,'x'),(0,NULL)", 2))
		aggregateModifiersBoth(run, fixture.Select("SELECT count(10/id) FILTER(WHERE id<>0),string_agg(v,(10/id)::text ORDER BY 10/id) FILTER(WHERE id<>0) FROM am_demand", []string{"count", "string_agg"}, []string{"INT8", "TEXT"}, [][]any{{int64(1), "x"}}))
		run(fixture.Exec("CREATE SEQUENCE am_demand_seq", 0))
		for _, filter := range []string{"FALSE", "NULL"} {
			aggregateModifiersBoth(run, fixture.Select("SELECT count(nextval('am_demand_seq') ORDER BY nextval('am_demand_seq')) FILTER(WHERE "+filter+") FROM am_demand", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		run(fixture.Select("SELECT nextval('am_demand_seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
		for _, q := range []string{
			"SELECT string_agg(v,(10/id)::text ORDER BY id) FROM am_demand",
			"SELECT string_agg(v,',' ORDER BY 10/id) FROM am_demand",
			"SELECT any_value(id ORDER BY 10/id) FROM am_demand",
			"SELECT bool_or(id=1 ORDER BY 10/id) FROM am_demand",
		} {
			aggregateModifiersBoth(run, aggregateModifiersError(q, "22012", "division by zero", ""))
		}
	})
}

func TestReferenceAggregateModifiersVolatility(t *testing.T) {
	aggregateModifiersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE am_volatile(v INT)", 0))
		run(fixture.Exec("INSERT INTO am_volatile VALUES(1),(1),(1)", 3))
		run(fixture.Exec("CREATE SEQUENCE am_reuse", 0))
		run(fixture.Select("SELECT count(DISTINCT nextval('am_reuse') ORDER BY nextval('am_reuse')) FROM am_volatile", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
		run(fixture.Select("SELECT nextval('am_reuse')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(4)}}))
		run(fixture.Exec("CREATE SEQUENCE am_keys", 0))
		run(fixture.Select("SELECT count(v ORDER BY nextval('am_keys'),nextval('am_keys')) FROM am_volatile", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
		run(fixture.Select("SELECT nextval('am_keys')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(4)}}))
		run(fixture.Exec("CREATE SEQUENCE am_calls", 0))
		run(fixture.Select("SELECT count(v) FILTER(WHERE nextval('am_calls')>0),count(v) FILTER(WHERE nextval('am_calls')>0) FROM am_volatile", []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(3)}}))
		run(fixture.Select("SELECT nextval('am_calls')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(7)}}))
		run(fixture.Exec("CREATE SEQUENCE am_duplicate", 0))
		run(fixture.Select("SELECT count(DISTINCT v) FILTER(WHERE nextval('am_duplicate')>0) FROM am_volatile", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.Select("SELECT nextval('am_duplicate')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(4)}}))
		run(fixture.Exec("CREATE SEQUENCE am_null", 0))
		run(fixture.Select("SELECT string_agg(NULL::text,nextval('am_null')::text ORDER BY nextval('am_null')) FROM am_volatile", []string{"string_agg"}, []string{"TEXT"}, [][]any{{nil}}))
		run(fixture.Select("SELECT nextval('am_null')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(7)}}))
		run(fixture.Exec("CREATE SEQUENCE am_local_calls", 0))
		run(fixture.Select("SELECT count(v ORDER BY nextval('am_local_calls')),count(v ORDER BY nextval('am_local_calls')) FROM am_volatile", []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(3)}}))
		run(fixture.Select("SELECT nextval('am_local_calls')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(7)}}))
		run(fixture.Exec("CREATE SEQUENCE am_rejected_filter", 0))
		run(fixture.Exec("CREATE SEQUENCE am_rejected_input", 0))
		run(fixture.Select("SELECT count(nextval('am_rejected_input') ORDER BY nextval('am_rejected_input')) FILTER(WHERE nextval('am_rejected_filter')<0) FROM am_volatile", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT nextval('am_rejected_filter'),nextval('am_rejected_input')", []string{"nextval", "nextval"}, []string{"INT8", "INT8"}, [][]any{{int64(4), int64(1)}}))
	})
}

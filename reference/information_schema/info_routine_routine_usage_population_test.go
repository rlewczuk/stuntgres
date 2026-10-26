package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoRoutineRoutineUsagePopulation(t *testing.T) {
	rruScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		rruExec(t, p, b, c,
			"CREATE FUNCTION rru_pop_c() RETURNS integer RETURN NULL::integer",
			"CREATE FUNCTION rru_pop_b() RETURNS integer RETURN rru_pop_c()",
			"CREATE FUNCTION rru_pop_over(integer) RETURNS integer RETURN 1",
			"CREATE FUNCTION rru_pop_over(bigint) RETURNS integer RETURN 2",
			"CREATE FUNCTION rru_pop_a() RETURNS integer RETURN rru_pop_b()+rru_pop_b()+rru_pop_over(1)+rru_pop_over(1::bigint)",
			"CREATE PROCEDURE rru_pop_proc() BEGIN ATOMIC SELECT rru_pop_c(); END",
			"CREATE TABLE rru_pop_table(v integer)",
			"CREATE SEQUENCE rru_pop_seq",
			"CREATE TYPE rru_pop_type AS ENUM ('ok')",
			"CREATE FUNCTION rru_pop_type_use() RETURNS rru_pop_type RETURN NULL::rru_pop_type",
			"CREATE FUNCTION rru_pop_table_use() RETURNS integer BEGIN ATOMIC SELECT v FROM rru_pop_table; END",
			"CREATE FUNCTION rru_pop_seq_use() RETURNS bigint RETURN nextval('rru_pop_seq')",
			"CREATE FUNCTION rru_pop_pin() RETURNS integer RETURN pg_catalog.abs(-1)",
			"CREATE FUNCTION rru_pop_null(a integer DEFAULT NULL) RETURNS integer RETURN NULL::integer")
		id := func(name, inputs string) string { return rruID(t, p, c, "public", name, inputs) }
		row := func(u, v, inputs string) []any { return rruRow(database, "public", id(u, ""), "public", id(v, inputs)) }
		rruCheck(t, p, b, c, "specific_schema='public'", [][]any{
			row("rru_pop_a", "rru_pop_b", ""), row("rru_pop_a", "rru_pop_over", "integer"), row("rru_pop_a", "rru_pop_over", "bigint"), row("rru_pop_b", "rru_pop_c", ""), row("rru_pop_proc", "rru_pop_c", "")})
	})
}

func TestReferenceInfoRoutineRoutineUsageKinds(t *testing.T) {
	rruScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		rruExec(t, p, b, c,
			"CREATE FUNCTION rru_kind_step(integer,integer) RETURNS integer RETURN $1+$2",
			"CREATE AGGREGATE rru_kind_agg(integer) (SFUNC=rru_kind_step,STYPE=integer,INITCOND='0')",
			"CREATE FUNCTION rru_kind_leaf() RETURNS integer RETURN 1",
			"CREATE FUNCTION rru_kind_scalar() RETURNS integer RETURN rru_kind_leaf()",
			"CREATE PROCEDURE rru_kind_proc() BEGIN ATOMIC SELECT rru_kind_leaf(); END",
			"CREATE FUNCTION rru_kind_agg_use() RETURNS integer BEGIN ATOMIC SELECT rru_kind_agg(x) FROM (VALUES(1),(2)) t(x); END",
			"CREATE FUNCTION rru_kind_explicit() RETURNS integer BEGIN ATOMIC SELECT rru_kind_agg(x)+rru_kind_step(1,2) FROM (VALUES(1),(2)) t(x); END",
			"CREATE FUNCTION rru_kind_window() RETURNS bigint BEGIN ATOMIC SELECT row_number() OVER (); END")
		leaf := rruID(t, p, c, "public", "rru_kind_leaf", "")
		rruCheck(t, p, b, c, "specific_schema='public'", [][]any{
			rruRow(database, "public", rruID(t, p, c, "public", "rru_kind_scalar", ""), "public", leaf),
			rruRow(database, "public", rruID(t, p, c, "public", "rru_kind_proc", ""), "public", leaf),
			rruRow(database, "public", rruID(t, p, c, "public", "rru_kind_explicit", ""), "public", rruID(t, p, c, "public", "rru_kind_step", "integer,integer"))})
	})
}

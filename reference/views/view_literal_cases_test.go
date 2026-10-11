package reference_test

import (
	"stuntgres/reference/fixture"
	"time"
)

type referenceViewLiteral struct {
	name, body, projection string
	columns, types         []string
	native, postgres       [][]any
}

type referenceViewLiteralSeed struct {
	name  string
	setup []fixture.Step
	views []referenceViewLiteral
}

func referenceViewAnalyzedSeeds() []referenceViewLiteralSeed {
	stamp := time.Date(2000, 1, 2, 4, 5, 6, 124000000, time.UTC)
	date := time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)
	clock := time.Date(0, 1, 1, 4, 5, 6, 124000000, time.UTC)
	setup := []fixture.Step{fixture.Exec("CREATE TABLE t(id INT,d DATE,ts TIMESTAMP)", 0), fixture.Exec("INSERT INTO t VALUES(1,'2000-02-01','2000-02-02'),(2,'2000-01-02','2000-01-03')", 2)}
	integrated := []referenceViewLiteral{
		{"literals", "SELECT DATE '2000-01-02',TIME(3) '04:05:06.1235',TIMESTAMP(3) '2000-01-02 04:05:06.1235' FROM t WHERE id=1", "d,clock,stamp", []string{"d", "clock", "stamp"}, []string{"DATE", "TIME", "TIMESTAMP"}, [][]any{{"2000-01-02", "04:05:06.124", stamp}}, [][]any{{date, clock, stamp}}},
		{"contextual", "SELECT id FROM t WHERE d='2000-02-01'", "id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}, [][]any{{int64(1)}}},
		{"repeated", "SELECT '2000-02-01' BETWEEN d AND ts FROM t WHERE id=1", "matches", []string{"matches"}, []string{"BOOL"}, [][]any{{true}}, [][]any{{true}}},
	}
	seeds := []referenceViewLiteralSeed{{"integrated", setup, integrated}}
	richer := referenceViewLegacyLiteralSetup()
	for _, v := range []referenceViewLiteral{
		{"date", "SELECT DATE '2000-01-02' FROM t WHERE id=1", "value", []string{"value"}, []string{"DATE"}, [][]any{{"2000-01-02"}}, [][]any{{date}}},
		{"time_precision", "SELECT TIME(3) '04:05:06.1235' FROM t WHERE id=1", "value", []string{"value"}, []string{"TIME"}, [][]any{{"04:05:06.124"}}, [][]any{{clock}}},
		{"timestamp_precision", "SELECT TIMESTAMP(3) '2000-01-02 04:05:06.1235' FROM t WHERE id=1", "value", []string{"value"}, []string{"TIMESTAMP"}, [][]any{{stamp}}, [][]any{{stamp}}},
		{"contextual_date", "SELECT id FROM t WHERE d='2000-02-01'", "value", []string{"value"}, []string{"INT4"}, [][]any{{int64(1)}}, [][]any{{int64(1)}}},
		{"repeated_context", "SELECT '2000-02-01' BETWEEN d AND ts FROM t WHERE id=1", "value", []string{"value"}, []string{"BOOL"}, [][]any{{true}}, [][]any{{true}}},
	} {
		seeds = append(seeds, referenceViewLiteralSeed{v.name, richer, []referenceViewLiteral{v}})
	}
	return seeds
}

func referenceViewLegacyLiteralSetup() []fixture.Step {
	return []fixture.Step{fixture.Exec("CREATE TABLE t(id INT,d DATE,ts TIMESTAMP,n NAME,u UUID,b BYTEA,num NUMERIC(5,2))", 0), fixture.Exec("INSERT INTO t(id,d,ts) VALUES(1,'2000-02-01','2000-02-02'),(2,'2000-01-02','2000-01-03')", 2)}
}

func referenceViewEndpointSeeds() []referenceViewLiteralSeed {
	endpoint := time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC)
	seeds := []referenceViewLiteralSeed{{"integrated", []fixture.Step{fixture.Exec("CREATE TABLE t(id INT)", 0), fixture.Exec("INSERT INTO t VALUES(1)", 1)}, []referenceViewLiteral{
		{"endpoints", "SELECT DATE 'infinity',TIME(0) '24:00:00',TIMESTAMP(0) '294276-12-31 23:59:59.999999',CASE WHEN id=0 THEN DATE '2000-01-01' ELSE NULL END FROM t", "day,clock,stamp,optional", []string{"day", "clock", "stamp", "optional"}, []string{"DATE", "TIME", "TIMESTAMP", "DATE"}, [][]any{{"infinity", "24:00:00", endpoint, nil}}, [][]any{{"infinity", time.Date(0, 1, 2, 0, 0, 0, 0, time.UTC), endpoint, nil}}},
	}}}
	for _, v := range []referenceViewLiteral{
		{"date_infinity", "SELECT DATE 'infinity' FROM t WHERE id=1", "value", []string{"value"}, []string{"DATE"}, [][]any{{"infinity"}}, [][]any{{"infinity"}}},
		{"timestamp_infinity", "SELECT TIMESTAMP 'infinity' FROM t WHERE id=1", "value", []string{"value"}, []string{"TIMESTAMP"}, [][]any{{"infinity"}}, [][]any{{"infinity"}}},
		{"typed_null", "SELECT CASE WHEN id=0 THEN DATE '2000-01-01' ELSE NULL END FROM t WHERE id=1", "value", []string{"value"}, []string{"DATE"}, [][]any{{nil}}, [][]any{{nil}}},
	} {
		seeds = append(seeds, referenceViewLiteralSeed{v.name, referenceViewLegacyLiteralSetup(), []referenceViewLiteral{v}})
	}
	return seeds
}

func referenceViewScalarSeeds() []referenceViewLiteralSeed {
	scalar := referenceViewLiteral{"contextual", `SELECT COALESCE(n,'MiXeD'),COALESCE(u,'550e8400-e29b-41d4-a716-446655440000'),COALESCE(b,'\x61'),COALESCE(num,'1.24') FROM t WHERE id=1`, "n,u,b,num", []string{"n", "u", "b", "num"}, []string{"NAME", "UUID", "BYTEA", "NUMERIC"}, [][]any{{"MiXeD", "550e8400-e29b-41d4-a716-446655440000", "a", "1.24"}}, [][]any{{"MiXeD", "550e8400-e29b-41d4-a716-446655440000", "a", "1.24"}}}
	numeric := referenceViewLiteral{"numeric_scale", "SELECT 1e3 FROM t WHERE id=1", "value", []string{"value"}, []string{"NUMERIC"}, [][]any{{"1000"}}, [][]any{{"1000"}}}
	legacyScalar := scalar
	legacyScalar.name = "other_contexts"
	return []referenceViewLiteralSeed{
		{"root_nullable_scalars", []fixture.Step{fixture.Exec("CREATE TABLE t(id INT,n NAME,u UUID,b BYTEA,num NUMERIC(5,2))", 0), fixture.Exec("INSERT INTO t(id) VALUES(1)", 1)}, []referenceViewLiteral{scalar, numeric}},
		{"other_contexts", referenceViewLegacyLiteralSetup(), []referenceViewLiteral{legacyScalar}},
		{"numeric_scale", referenceViewLegacyLiteralSetup(), []referenceViewLiteral{numeric}},
	}
}

func referenceViewGroupingSeeds() []referenceViewLiteralSeed {
	return []referenceViewLiteralSeed{{"group_and_branches", []fixture.Step{fixture.Exec("CREATE TABLE t(d DATE,txt TEXT)", 0), fixture.Exec("INSERT INTO t VALUES('2000-02-01','bad')", 1)}, []referenceViewLiteral{
		{"grouped", "SELECT TIME(3) '04:05:06.1235',COUNT(*) FROM t WHERE d=DATE '2000-02-01' GROUP BY TIME(3) '04:05:06.1235' HAVING DATE '2000-02-01'=DATE '2000-02-01'", "clock,n", []string{"clock", "n"}, []string{"TIME", "INT8"}, [][]any{{"04:05:06.124", int64(1)}}, [][]any{{time.Date(0, 1, 1, 4, 5, 6, 124000000, time.UTC), int64(1)}}},
		{"branches", "SELECT CASE DATE '2000-02-01' WHEN d THEN true ELSE false END,CASE WHEN d=DATE '2000-02-01' THEN 7 ELSE CAST(txt AS int4) END FROM t", "matches,safe", []string{"matches", "safe"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(7)}}, [][]any{{true, int64(7)}}},
	}}}
}

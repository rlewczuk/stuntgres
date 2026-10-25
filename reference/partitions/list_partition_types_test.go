package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceListPartitionTypes(t *testing.T) {
	referenceListScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		for _, c := range []struct {
			name, typ, key, bound, input string
			types                        []string
			rows                         [][]any
		}{
			{"wide", "BIGINT", "k", "9007199254740993", "9007199254740993", []string{"INT8"}, [][]any{{int64(9007199254740993)}}},
			{"text", "TEXT", `k COLLATE "C" text_ops`, "'MINVALUE','MAXVALUE','NULL',NULL", "'MINVALUE'),('MAXVALUE'),('NULL'),(NULL", []string{"TEXT"}, [][]any{{"MAXVALUE"}, {"MINVALUE"}, {"NULL"}, {nil}}},
			{"bool", "BOOL", "k", "FALSE,NULL", "FALSE),(NULL", []string{"BOOL"}, [][]any{{false}, {nil}}},
			{"numeric", "NUMERIC(5,2)", "k", "'1.001'", "1.00", []string{"NUMERIC"}, [][]any{{"1.00"}}},
			{"expression", "TEXT", "(lower(k))", "'a'", "'A'", []string{"TEXT"}, [][]any{{"A"}}},
		} {
			name := "lp_type_" + c.name
			run(fixture.Exec("CREATE TABLE "+name+"(k "+c.typ+") PARTITION BY LIST("+c.key+")", 0))
			run(fixture.Exec("CREATE TABLE "+name+"_leaf PARTITION OF "+name+" FOR VALUES IN("+c.bound+")", 0))
			run(fixture.Exec("INSERT INTO "+name+" VALUES("+c.input+")", int64(len(c.rows))))
			run(fixture.Select("SELECT k FROM "+name+"_leaf ORDER BY k NULLS LAST", []string{"k"}, c.types, c.rows))
		}
		run(referenceRangeMiss("INSERT INTO lp_type_wide VALUES(9007199254740992)", "lp_type_wide", "k", "9007199254740992"))
		run(fixture.Exec("CREATE TYPE lp_color AS ENUM('red','green','blue')", 0))
		run(fixture.Exec("CREATE DOMAIN lp_id AS BIGINT", 0))
		for _, c := range []struct{ name, typ, bound, input, output string }{
			{"enum", "lp_color", "'green',NULL", "'green'", "green"},
			{"domain", "lp_id", "9007199254740993,NULL", "9007199254740993", "9007199254740993"},
		} {
			run(fixture.Exec("CREATE TABLE lp_"+c.name+"(k "+c.typ+") PARTITION BY LIST(k)", 0))
			run(fixture.Exec("CREATE TABLE lp_"+c.name+"_leaf PARTITION OF lp_"+c.name+" FOR VALUES IN("+c.bound+")", 0))
			run(fixture.Exec("INSERT INTO lp_"+c.name+" VALUES("+c.input+")", 1))
			run(fixture.Select("SELECT k::TEXT AS k FROM lp_"+c.name, []string{"k"}, []string{"TEXT"}, [][]any{{c.output}}))
		}
		run(fixture.Exec("CREATE SEQUENCE lp_once_seq START 10", 0))
		run(fixture.Exec("CREATE TABLE lp_once(k BIGINT) PARTITION BY LIST(k)", 0))
		run(fixture.Exec("CREATE TABLE lp_once_leaf PARTITION OF lp_once FOR VALUES IN(nextval('lp_once_seq'))", 0))
		run(fixture.Exec("INSERT INTO lp_once VALUES(10),(10)", 2))
		run(fixture.Select("SELECT last_value FROM lp_once_seq", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(10)}}))
	})
}

func TestReferenceListPartitionMetadata(t *testing.T) {
	referenceListScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE lp_meta(k INT,payload TEXT NOT NULL) PARTITION BY LIST(k)", 0))
		run(fixture.Exec("CREATE TABLE lp_meta_a PARTITION OF lp_meta FOR VALUES IN(1,NULL)", 0))
		run(fixture.Exec("CREATE TABLE lp_meta_def PARTITION OF lp_meta DEFAULT", 0))
		run(fixture.Exec("INSERT INTO lp_meta VALUES(1,'one'),(NULL,'null'),(9,'default')", 3))
		s := fixture.Select("SELECT k,payload FROM lp_meta ORDER BY k NULLS LAST", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(9), "default"}, {nil, "null"}})
		s.CheckMetadata = referenceRangeMetadata(b.PG, s.Columns, s.Types, []bool{true, false})
		run(s)
		run(fixture.Select("SELECT relname,relkind,relispartition,relhassubclass FROM pg_class WHERE relname IN ('lp_meta','lp_meta_a','lp_meta_def') ORDER BY relname", []string{"relname", "relkind", "relispartition", "relhassubclass"}, []string{"NAME", "CHAR", "BOOL", "BOOL"}, [][]any{{"lp_meta", "p", false, true}, {"lp_meta_a", "r", true, false}, {"lp_meta_def", "r", true, false}}))
		run(fixture.Select("SELECT c.relname,p.relname AS parent,i.inhseqno,i.inhdetachpending FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid JOIN pg_class p ON p.oid=i.inhparent WHERE p.relname='lp_meta' ORDER BY c.relname", []string{"relname", "parent", "inhseqno", "inhdetachpending"}, []string{"NAME", "NAME", "INT4", "BOOL"}, [][]any{{"lp_meta_a", "lp_meta", int64(1), false}, {"lp_meta_def", "lp_meta", int64(1), false}}))
		if b.PG {
			run(fixture.Select("SELECT pg_get_expr(relpartbound,oid) AS bound FROM pg_class WHERE relname='lp_meta_a'", []string{"bound"}, []string{"TEXT"}, [][]any{{"FOR VALUES IN (1, NULL)"}}))
			run(fixture.Select("SELECT pg_get_expr(relpartbound,oid) AS bound FROM pg_class WHERE relname='lp_meta_def'", []string{"bound"}, []string{"TEXT"}, [][]any{{"DEFAULT"}}))
		} else {
			run(fixture.Select("SELECT relpartbound FROM pg_class WHERE relname='lp_meta_a'", []string{"relpartbound"}, []string{"PG_NODE_TREE"}, [][]any{{`{PartitionBound strategy list default false values ({type 23 typmod -1 collation 0 value "1"} {type 23 typmod -1 collation 0 value NULL})}`}}))
			run(fixture.Select("SELECT relpartbound FROM pg_class WHERE relname='lp_meta_def'", []string{"relpartbound"}, []string{"PG_NODE_TREE"}, [][]any{{`{PartitionBound strategy list default true}`}}))
		}
	})
}

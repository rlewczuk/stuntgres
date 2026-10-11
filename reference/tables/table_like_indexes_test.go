package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTableLikeIndexes(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE src(dead TEXT,a INT CONSTRAINT strange UNIQUE NULLS NOT DISTINCT,b TEXT)", 0))
		run(fixture.Exec("ALTER TABLE src DROP COLUMN dead", 0))
		run(fixture.Exec(`CREATE INDEX strange_b ON src(b COLLATE "C" text_pattern_ops DESC NULLS LAST) INCLUDE(a)`, 0))
		run(fixture.Exec("CREATE INDEX strange_expr ON src((a+1))", 0))
		run(fixture.Exec("CREATE INDEX strange_hash ON src USING hash(a)", 0))
		run(fixture.Exec("CREATE TABLE clone_a_idx(dummy INT)", 0))
		run(fixture.Exec("CREATE TABLE clone(lead BOOL,LIKE src INCLUDING INDEXES)", 0))
		run(fixture.Select("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename='clone' ORDER BY indexname", []string{"indexname"}, []string{"NAME"}, [][]any{{"clone_a_idx1"}, {"clone_a_key"}, {"clone_b_a_idx"}, {"clone_expr_idx"}}))
		if b.PG {
			run(fixture.Select("SELECT ci.relname,am.amname,i.indisunique,i.indnullsnotdistinct,i.indisvalid,i.indisready FROM pg_index i JOIN pg_class ci ON ci.oid=i.indexrelid JOIN pg_am am ON am.oid=ci.relam WHERE i.indrelid='clone'::regclass ORDER BY ci.relname", []string{"relname", "amname", "indisunique", "indnullsnotdistinct", "indisvalid", "indisready"}, []string{"NAME", "NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"clone_a_idx1", "hash", false, false, true, true}, {"clone_a_key", "btree", true, true, true, true}, {"clone_b_a_idx", "btree", false, false, true, true}, {"clone_expr_idx", "btree", false, false, true, true}}))
		}
		run(fixture.Select("SELECT pg_get_indexdef('clone_b_a_idx'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{`CREATE INDEX clone_b_a_idx ON public.clone USING btree (b COLLATE "C" text_pattern_ops DESC NULLS LAST) INCLUDE (a)`}}))
		run(fixture.Select("SELECT pg_get_indexdef('clone_expr_idx'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE INDEX clone_expr_idx ON public.clone USING btree (((a + 1)))"}}))
		if b.PG {
			run(fixture.Select("SELECT opc.opcname FROM pg_index i JOIN pg_opclass opc ON opc.oid=i.indclass[0] WHERE i.indexrelid='clone_a_idx1'::regclass", []string{"opcname"}, []string{"NAME"}, [][]any{{"int4_ops"}}))
			run(fixture.Select("SELECT opc.opcname,coll.collname FROM pg_index i JOIN pg_opclass opc ON opc.oid=i.indclass[0] JOIN pg_collation coll ON coll.oid=i.indcollation[0] WHERE i.indexrelid='clone_b_a_idx'::regclass", []string{"opcname", "collname"}, []string{"NAME", "NAME"}, [][]any{{"text_pattern_ops", "C"}}))
			run(fixture.Select("SELECT conname,conindid='clone_a_key'::regclass AS backing FROM pg_constraint WHERE conrelid='clone'::regclass AND contype='u'", []string{"conname", "backing"}, []string{"NAME", "BOOL"}, [][]any{{"clone_a_key", true}}))
		}
		run(fixture.Exec("INSERT INTO clone VALUES(true,NULL,'z')", 1))
		run(fixture.TableLikeError("INSERT INTO clone VALUES(false,NULL,'x')", fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "clone_a_key"`, Detail: "Key (a)=(null) already exists.", Schema: "public", Table: "clone", Constraint: "clone_a_key"}))
		run(fixture.Exec("INSERT INTO clone VALUES(false,4,'a')", 1))
		run(fixture.Select("SELECT b,a FROM clone ORDER BY b DESC NULLS LAST", []string{"b", "a"}, []string{"TEXT", "INT4"}, [][]any{{"z", nil}, {"a", int64(4)}}))
		run(fixture.Select("SELECT a+1 AS expr FROM clone WHERE a+1=5", []string{"expr"}, []string{"INT4"}, [][]any{{int64(5)}}))
		run(fixture.Exec("CREATE TABLE checks_only(LIKE src INCLUDING CONSTRAINTS)", 0))
		run(fixture.Exec("INSERT INTO checks_only VALUES(1,'x'),(1,'x'),(NULL,'x'),(NULL,'x')", 4))
		run(fixture.Exec("DROP TABLE src", 0))
		run(fixture.Select("SELECT a FROM clone WHERE a=4", []string{"a"}, []string{"INT4"}, [][]any{{int64(4)}}))
		run(fixture.Exec("CREATE TABLE equivalent(LIKE clone INCLUDING INDEXES,UNIQUE NULLS NOT DISTINCT(a))", 0))
		run(fixture.Select("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename='equivalent' ORDER BY indexname", []string{"indexname"}, []string{"NAME"}, [][]any{{"equivalent_a_idx"}, {"equivalent_a_key"}, {"equivalent_a_key1"}, {"equivalent_b_a_idx"}, {"equivalent_expr_idx"}}))
	})
}

func TestReferenceTableLikeIndexUTF8Names(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		name := strings.Repeat("é", 30)
		key := strings.Repeat("é", 28) + "_a_key"
		run(fixture.Exec("CREATE TABLE src(a INT UNIQUE)", 0))
		run(fixture.Exec(`CREATE TABLE "`+name+`"(LIKE src INCLUDING INDEXES)`, 0))
		run(fixture.Select("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename=$1", []string{"indexname"}, []string{"NAME"}, [][]any{{key}}, name))
		run(fixture.Exec(`INSERT INTO "`+name+`" VALUES(1)`, 1))
		run(fixture.TableLikeError(`INSERT INTO "`+name+`" VALUES(1)`, fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "` + key + `"`, Detail: "Key (a)=(1) already exists.", Schema: "public", Table: name, Constraint: key}))
	})
}

func TestReferenceTableLikeInvalidIndex(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE src(a INT)", 0))
		run(fixture.Exec("INSERT INTO src VALUES(1),(1)", 2))
		run(fixture.TableLikeError("CREATE UNIQUE INDEX CONCURRENTLY invalid_src ON src(a)", fixture.Diagnostic{Code: "23505", Message: `could not create unique index "invalid_src"`, Detail: "Key (a)=(1) is duplicated.", Schema: "public", Table: "src", Constraint: "invalid_src"}))
		if b.PG {
			run(fixture.Select("SELECT indisvalid,indisready,indislive FROM pg_index WHERE indexrelid='invalid_src'::regclass", []string{"indisvalid", "indisready", "indislive"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, false, true}}))
		}
		run(fixture.Exec("CREATE TABLE clone(LIKE src INCLUDING INDEXES)", 0))
		run(fixture.Select("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename='clone'", []string{"indexname"}, []string{"NAME"}, [][]any{{"clone_a_idx"}}))
		if b.PG {
			run(fixture.Select("SELECT indisvalid,indisready,indislive FROM pg_index WHERE indrelid='clone'::regclass", []string{"indisvalid", "indisready", "indislive"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
		}
		run(fixture.Exec("INSERT INTO clone VALUES(1)", 1))
		run(fixture.TableLikeError("INSERT INTO clone VALUES(1)", fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "clone_a_idx"`, Detail: "Key (a)=(1) already exists.", Schema: "public", Table: "clone", Constraint: "clone_a_idx"}))
		run(fixture.Exec("DROP TABLE src", 0))
	})
}

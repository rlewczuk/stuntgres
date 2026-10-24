package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

type batchUtilityCase struct {
	name          string
	seed          []string
	change, probe string
	before, after any
	typ           string
}

func TestReferenceDriverBatchUtilityAtomicity(t *testing.T) {
	cases := []batchUtilityCase{
		{"table_create", nil, "CREATE TABLE u(i INT); INSERT INTO u VALUES(3); ALTER TABLE u ADD COLUMN j INT DEFAULT 4", "SELECT count(*) AS v FROM pg_class WHERE relname='u'", int64(0), int64(1), "INT8"},
		{"table_drop", []string{"CREATE TABLE u(i INT)", "INSERT INTO u VALUES(3)"}, "DROP TABLE u", "SELECT count(*) AS v FROM pg_class WHERE relname='u'", int64(1), int64(0), "INT8"},
		{"schema", nil, "CREATE SCHEMA us; CREATE TABLE us.u(i INT); INSERT INTO us.u VALUES(3)", "SELECT count(*) AS v FROM pg_tables WHERE schemaname='us'", int64(0), int64(1), "INT8"},
		{"schema_alter", []string{"CREATE SCHEMA us", "CREATE TABLE us.schema_anchor(i INT)"}, "ALTER SCHEMA us RENAME TO renamed", "SELECT count(*) AS v FROM pg_tables WHERE schemaname='renamed'", int64(0), int64(1), "INT8"},
		{"schema_drop", []string{"SET client_min_messages TO warning", "CREATE SCHEMA us", "CREATE TABLE us.schema_anchor(i INT)"}, "DROP SCHEMA us CASCADE", "SELECT count(*) AS v FROM pg_tables WHERE schemaname='us'", int64(1), int64(0), "INT8"},
		{"enum", nil, "CREATE TYPE ue AS ENUM('a','b'); CREATE TABLE u(v ue); INSERT INTO u VALUES('a')", "SELECT count(*) AS v FROM pg_class WHERE relname='u'", int64(0), int64(1), "INT8"},
		{"composite", nil, "CREATE TYPE uc AS(i INT); CREATE TABLE u(v uc); INSERT INTO u VALUES('(3)')", "SELECT count(*) AS v FROM pg_class WHERE relname='u'", int64(0), int64(1), "INT8"},
		{"domain", nil, "CREATE DOMAIN ud AS INT; CREATE TABLE u(v ud); INSERT INTO u VALUES(3)", "SELECT count(*) AS v FROM pg_class WHERE relname='u'", int64(0), int64(1), "INT8"},
		{"domain_drop", []string{"CREATE DOMAIN ud AS INT", "CREATE TABLE u(v ud,i INT)", "INSERT INTO u VALUES(3,4)"}, "DROP DOMAIN ud CASCADE", "SELECT count(*) AS v FROM pg_class WHERE relname='u'", int64(1), int64(1), "INT8"},
		{"collation", nil, "CREATE COLLATION uc(provider=libc,locale='C'); CREATE TABLE u(v TEXT COLLATE uc)", "SELECT count(*) AS v FROM pg_collation WHERE collname='uc'", int64(0), int64(1), "INT8"},
		{"collation_alter", []string{"CREATE COLLATION uc(provider=libc,locale='C')"}, "ALTER COLLATION uc RENAME TO renamed", "SELECT count(*) AS v FROM pg_collation WHERE collname='renamed'", int64(0), int64(1), "INT8"},
		{"collation_drop", []string{"CREATE COLLATION uc(provider=libc,locale='C')"}, "DROP COLLATION uc", "SELECT count(*) AS v FROM pg_collation WHERE collname='uc'", int64(1), int64(0), "INT8"},
		{"conversion", nil, "CREATE CONVERSION ub_conv FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1", "SELECT count(*) AS v FROM pg_conversion WHERE conname='ub_conv'", int64(0), int64(1), "INT8"},
		{"conversion_alter", []string{"CREATE CONVERSION ub_conv FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1"}, "ALTER CONVERSION ub_conv RENAME TO ub_renamed", "SELECT count(*) AS v FROM pg_conversion WHERE conname='ub_renamed'", int64(0), int64(1), "INT8"},
		{"conversion_drop", []string{"CREATE CONVERSION ub_conv FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1"}, "DROP CONVERSION ub_conv", "SELECT count(*) AS v FROM pg_conversion WHERE conname='ub_conv'", int64(1), int64(0), "INT8"},
		{"cast", []string{"CREATE TYPE ub_src AS ENUM ('red')", "CREATE TYPE ub_dst AS ENUM ('red')"}, "CREATE CAST (ub_src AS ub_dst) WITH INOUT", "SELECT count(*) AS v FROM pg_cast WHERE castsource='ub_src'::regtype AND casttarget='ub_dst'::regtype", int64(0), int64(1), "INT8"},
		{"cast_drop", []string{"CREATE TYPE ub_src AS ENUM ('red')", "CREATE TYPE ub_dst AS ENUM ('red')", "CREATE CAST (ub_src AS ub_dst) WITH INOUT"}, "DROP CAST (ub_src AS ub_dst)", "SELECT count(*) AS v FROM pg_cast WHERE castsource='ub_src'::regtype AND casttarget='ub_dst'::regtype", int64(1), int64(0), "INT8"},
		{"comment_table", []string{"CREATE TABLE u(i INT)"}, "COMMENT ON TABLE u IS 'new'", "SELECT obj_description('u'::regclass,'pg_class') AS v", nil, "new", "TEXT"},
		{"comment_column", []string{"CREATE TABLE u(i INT)"}, "COMMENT ON COLUMN u.i IS 'new'", "SELECT col_description('u'::regclass,1) AS v", nil, "new", "TEXT"},
		{"comment_schema", []string{"CREATE SCHEMA us", "CREATE TABLE us.schema_anchor(i INT)"}, "COMMENT ON SCHEMA us IS 'new'", "SELECT obj_description(relnamespace,'pg_namespace') AS v FROM pg_class WHERE relname='schema_anchor' AND relkind='r'", nil, "new", "TEXT"},
		{"index_create", []string{"CREATE TABLE u(i INT)", "INSERT INTO u VALUES(3)"}, "INSERT INTO u VALUES(4); CREATE INDEX ui ON u(i)", "SELECT count(*) AS v FROM pg_class WHERE relname='ui'", int64(0), int64(1), "INT8"},
		{"index_drop", []string{"CREATE TABLE u(i INT)", "CREATE INDEX ui ON u(i)"}, "DROP INDEX ui", "SELECT count(*) AS v FROM pg_class WHERE relname='ui'", int64(1), int64(0), "INT8"},
		{"index_alter", []string{"CREATE TABLE u(i INT)", "CREATE INDEX ui ON u(i)"}, "ALTER INDEX ui RENAME TO renamed", "SELECT count(*) AS v FROM pg_class WHERE relname='renamed'", int64(0), int64(1), "INT8"},
		{"reindex_index", []string{"CREATE TABLE u(i INT)", "CREATE INDEX ui ON u(i)", "INSERT INTO u VALUES(3)"}, "INSERT INTO u VALUES(4); REINDEX INDEX ui", "SELECT count(*) AS v FROM u", int64(1), int64(2), "INT8"},
		{"reindex_table", []string{"CREATE TABLE u(i INT)", "CREATE INDEX ui ON u(i)", "INSERT INTO u VALUES(3)"}, "INSERT INTO u VALUES(4); REINDEX TABLE u", "SELECT count(*) AS v FROM u", int64(1), int64(2), "INT8"},
		{"ctas", []string{"CREATE TABLE source(i INT)", "INSERT INTO source VALUES(3)"}, "INSERT INTO source VALUES(4); CREATE TABLE u AS SELECT i FROM source", "SELECT count(*) AS v FROM pg_class WHERE relname='u'", int64(0), int64(1), "INT8"},
		{"select_into", []string{"CREATE TABLE source(i INT)", "INSERT INTO source VALUES(3)"}, "SELECT i INTO u FROM source", "SELECT count(*) AS v FROM pg_class WHERE relname='u'", int64(0), int64(1), "INT8"},
		{"materialized_view", []string{"CREATE TABLE source(i INT)", "INSERT INTO source VALUES(3)"}, "CREATE MATERIALIZED VIEW u AS SELECT i FROM source", "SELECT count(*) AS v FROM pg_class WHERE relname='u'", int64(0), int64(1), "INT8"},
		{"view_create", []string{"CREATE TABLE source(i INT)"}, "CREATE VIEW u AS SELECT i FROM source", "SELECT count(*) AS v FROM pg_class WHERE relname='u'", int64(0), int64(1), "INT8"},
		{"view_replace", []string{"CREATE TABLE source(i INT)", "INSERT INTO source VALUES(3)", "CREATE VIEW u AS SELECT i FROM source"}, "CREATE OR REPLACE VIEW u AS SELECT i+1 AS i FROM source", "SELECT i AS v FROM u", int64(3), int64(4), "INT4"},
		{"view_alter", []string{"CREATE VIEW u AS SELECT 3 AS i"}, "ALTER VIEW u RENAME TO renamed", "SELECT count(*) AS v FROM pg_class WHERE relname='renamed'", int64(0), int64(1), "INT8"},
		{"view_drop", []string{"CREATE VIEW u AS SELECT 3 AS i"}, "DROP VIEW u", "SELECT count(*) AS v FROM pg_class WHERE relname='u'", int64(1), int64(0), "INT8"},
		{"mv_drop", []string{"CREATE MATERIALIZED VIEW u AS SELECT 3 AS i"}, "DROP MATERIALIZED VIEW u", "SELECT count(*) AS v FROM pg_class WHERE relname='u'", int64(1), int64(0), "INT8"},
		{"rename", []string{"CREATE TABLE u(i INT)"}, "ALTER TABLE u RENAME TO renamed; INSERT INTO renamed VALUES(3)", "SELECT count(*) AS v FROM pg_class WHERE relname='renamed'", int64(0), int64(1), "INT8"},
		{"column_rename", []string{"CREATE TABLE u(i INT)", "INSERT INTO u VALUES(3)", "CREATE VIEW column_anchor AS SELECT i FROM u"}, "ALTER TABLE u RENAME i TO j; UPDATE u SET j=4", "SELECT definition LIKE '%j%' AS v FROM pg_views WHERE viewname='column_anchor'", false, true, "BOOL"},
		{"default", []string{"CREATE TABLE u(i INT DEFAULT 3)"}, "ALTER TABLE u ALTER COLUMN i SET DEFAULT 4; INSERT INTO u DEFAULT VALUES", "SELECT count(*) AS v FROM u WHERE i=4", int64(0), int64(1), "INT8"},
		{"set_schema", []string{"CREATE TABLE u(i INT)", "CREATE SCHEMA us"}, "ALTER TABLE u SET SCHEMA us; INSERT INTO us.u VALUES(3)", "SELECT count(*) AS v FROM pg_tables WHERE tablename='u' AND schemaname='us'", int64(0), int64(1), "INT8"},
		{"constraint_rename", []string{"CREATE TABLE u(i INT,CONSTRAINT old EXCLUDE USING btree(i WITH =))"}, "ALTER TABLE u RENAME CONSTRAINT old TO renamed", "SELECT count(*) AS v FROM pg_class WHERE relname='renamed'", int64(0), int64(1), "INT8"},
		{"sequence_create", nil, "CREATE SEQUENCE us; ALTER SEQUENCE us RESTART WITH 4; COMMENT ON SEQUENCE us IS 'new'", "SELECT count(*) AS v FROM pg_class WHERE relname='us'", int64(0), int64(1), "INT8"},
		{"sequence_alter", []string{"CREATE SEQUENCE us START 3"}, "ALTER SEQUENCE us RESTART WITH 4; ALTER SEQUENCE us RENAME TO renamed", "SELECT count(*) AS v FROM pg_class WHERE relname='renamed'", int64(0), int64(1), "INT8"},
		{"sequence_drop", []string{"CREATE SEQUENCE us"}, "DROP SEQUENCE us", "SELECT count(*) AS v FROM pg_class WHERE relname='us'", int64(1), int64(0), "INT8"},
		{"sequence_comment", []string{"CREATE SEQUENCE us"}, "COMMENT ON SEQUENCE us IS 'new'", "SELECT obj_description('us'::regclass,'pg_class') AS v", nil, "new", "TEXT"},
		{"alter_actions", []string{"CREATE TABLE u(i INT)", "INSERT INTO u VALUES(3)"}, "ALTER TABLE u ADD COLUMN j INT DEFAULT 4; ALTER TABLE u ALTER COLUMN j SET NOT NULL", "SELECT relnatts::int4 AS v FROM pg_class WHERE relname='u'", int64(1), int64(2), "INT4"},
		{"temp_create", nil, "CREATE TEMP TABLE u(i INT); INSERT INTO u VALUES(3)", "SELECT count(*) AS v FROM pg_tables WHERE tablename='u' AND schemaname LIKE 'pg_temp_%'", int64(0), int64(1), "INT8"},
		{"temp_drop", []string{"CREATE TEMP TABLE u(i INT)"}, "DROP TABLE u", "SELECT count(*) AS v FROM pg_tables WHERE tablename='u' AND schemaname LIKE 'pg_temp_%'", int64(1), int64(0), "INT8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				tc := tc
				if _, err := a.ExecContext(p.Ctx, "CREATE TABLE public.batch_catalog_anchor(i INT)"); err != nil {
					t.Fatal(err)
				}
				var publicNamespace uint64
				if err := a.QueryRowContext(p.Ctx, "SELECT relnamespace FROM pg_class WHERE relname='batch_catalog_anchor' AND relkind='r'").Scan(&publicNamespace); err != nil || publicNamespace == 0 {
					t.Fatal("independent public namespace", publicNamespace, err)
				}
				seedQuery := func(q string) string { return q }
				switch tc.name {
				case "schema", "schema_alter", "schema_drop", "comment_schema", "set_schema":
					schema := "batch_" + tc.name + "_schema"
					renamed := schema + "_renamed"
					replace := func(q string) string {
						return strings.ReplaceAll(strings.ReplaceAll(q, "us", schema), "renamed", renamed)
					}
					seedQuery = replace
					tc.change = replace(tc.change)
					tc.probe = replace(tc.probe)
					t.Cleanup(func() {
						ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
						defer cancel()
						_, _ = a.ExecContext(ctx, "ROLLBACK")
						for _, name := range []string{schema, renamed} {
							if _, err := peer.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+name+" CASCADE"); err != nil {
								t.Error("utility schema cleanup", err)
							}
						}
					})
				}
				if strings.Contains(tc.probe, "FROM pg_class WHERE relname=") {
					if tc.name != "comment_schema" {
						tc.probe += " AND relnamespace=" + strconv.FormatUint(publicNamespace, 10)
					}
					kind := "r"
					switch tc.name {
					case "index_create", "index_drop", "index_alter", "constraint_rename":
						kind = "i"
					case "materialized_view", "mv_drop":
						kind = "m"
					case "view_create", "view_alter", "view_drop":
						kind = "v"
					case "sequence_create", "sequence_alter", "sequence_drop":
						kind = "S"
					}
					tc.probe += " AND relkind='" + kind + "'"
				}
				for _, q := range tc.seed {
					if _, err := a.ExecContext(p.Ctx, seedQuery(q)); err != nil {
						t.Fatal(err)
					}
				}
				probe := func(x fixture.Executor, w any) {
					fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(tc.probe, []string{"v"}, []string{tc.typ}, [][]any{{w}}))
				}
				consumer := ""
				switch tc.name {
				case "enum":
					consumer = "SELECT v='a' AS typed FROM u"
				case "composite":
					consumer = "SELECT v IS NOT NULL AS typed FROM u"
				case "domain":
					consumer = "SELECT v=3 AS typed FROM u"
				case "domain_drop":
					consumer = "SELECT * FROM u"
				case "column_rename":
					consumer = "SELECT i=4 AS typed FROM column_anchor"
				case "collation":
					consumer = "SELECT upper('é' COLLATE uc)='é' AS typed"
				case "cast":
					consumer = "SELECT ('red'::ub_src)::ub_dst::text='red' AS typed"
				}
				privateSets := []fixture.BatchSet{{[]string{"v"}, []string{tc.typ}, [][]any{{tc.after}}, false}}
				if consumer != "" {
					tc.change += "; " + consumer
					typed := fixture.BatchSet{[]string{"typed"}, []string{"BOOL"}, [][]any{{true}}, false}
					if tc.name == "domain_drop" {
						typed = fixture.BatchSet{[]string{"i"}, []string{"INT4"}, [][]any{{int64(4)}}, false}
					}
					privateSets = append([]fixture.BatchSet{typed}, privateSets...)
				}
				readPrivate := func(rows *sql.Rows) {
					for i, set := range privateSets {
						if i > 0 && !rows.NextResultSet() {
							t.Fatal("private typed/catalog consumer boundary", rows.Err())
						}
						fixture.BatchReadSet(t, rows, set)
					}
				}
				suffix := "SELECT missing"
				column := "missing"
				if tc.name == "domain_drop" {
					suffix = "SELECT v FROM u"
					column = "v"
				}
				q := tc.change + "; " + tc.probe + "; " + suffix
				w := batchDiag("42703", `column "`+column+`" does not exist`)
				w.Position = batchPosition(q, column)
				if tc.name == "domain_drop" {
					w.Position = batchPosition(q, "v FROM u")
				}
				rows, err := a.QueryContext(p.Ctx, q)
				if err != nil {
					t.Fatal("private utility consumer", err)
				}
				readPrivate(rows)
				fixture.AssertDiagnostic(t, rows.Err(), &w)
				rows.Close()
				probe(a, tc.before)
				if tc.name == "domain_drop" {
					fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT * FROM u", []string{"v", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(4)}}))
				}
				if tc.name == "column_rename" {
					fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT i FROM column_anchor", []string{"i"}, []string{"INT4"}, [][]any{{int64(3)}}))
				}
				typeName := ""
				switch tc.name {
				case "enum":
					typeName = "ue"
				case "composite":
					typeName = "uc"
				case "domain":
					typeName = "ud"
				}
				typeAbsent := func(x fixture.Executor) {
					q := "CREATE TABLE type_peer_probe(v " + typeName + ")"
					w := batchDiag("42704", `type "`+typeName+`" does not exist`)
					w.Position = batchPosition(q, typeName)
					batchError(t, p, x, q, w)
				}
				if typeName != "" {
					typeAbsent(a)
				}
				// Promotion makes candidate effects externally observable only after COMMIT.
				rows = batchQuery(t, p, a, tc.change+"; "+tc.probe+"; BEGIN")
				fixture.BatchSets(t, rows, privateSets...)
				if typeName != "" {
					typeAbsent(peer)
				}
				switch tc.name {
				case "reindex_index", "reindex_table":
					if b.PG {
						batchUtilityBlockedPeer(t, p, b, a, peer, tc.probe, tc.typ, tc.after)
					} else {
						probe(peer, tc.before)
						batchExec(t, p, b, a, "COMMIT", 0)
						probe(peer, tc.after)
					}
				case "default", "view_replace":
					batchUtilityBlockedPeer(t, p, b, a, peer, tc.probe, tc.typ, tc.after)
				case "column_rename":
					if !b.PG {
						probe(peer, tc.before)
					}
					batchUtilityBlockedPeer(t, p, b, a, peer, "SELECT i AS v FROM column_anchor", "INT4", int64(4))
				default:
					if tc.name != "temp_create" && tc.name != "temp_drop" {
						probe(peer, tc.before)
					}
					batchExec(t, p, b, a, "COMMIT", 0)
				}
				probe(a, tc.after)
				if tc.name == "column_rename" {
					fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT j AS i FROM u", []string{"i"}, []string{"INT4"}, [][]any{{int64(4)}}))
					fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT i FROM column_anchor", []string{"i"}, []string{"INT4"}, [][]any{{int64(4)}}))
				}
				if tc.name == "enum" {
					fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT v::text AS v FROM u", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}}))
				}
				if tc.name == "composite" {
					fixture.CompositeReferenceRows(t, p, b, a, "SELECT v FROM u", [][]any{{"(3)"}}, false)
				}
				if tc.name == "domain" {
					fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT v FROM u", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}))
				}
				if typeName != "" {
					batchExec(t, p, b, peer, "CREATE TABLE type_peer_probe(v "+typeName+")", 0)
					batchExec(t, p, b, peer, "DROP TABLE type_peer_probe", 0)
				}
				if tc.name == "domain_drop" {
					for _, x := range []fixture.Executor{a, peer} {
						fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select("SELECT * FROM u", []string{"i"}, []string{"INT4"}, [][]any{{int64(4)}}))
					}
					q := "SELECT v FROM u"
					denied := batchDiag("42703", `column "v" does not exist`)
					denied.Position = batchPosition(q, "v")
					batchError(t, p, a, q, denied)
					batchExec(t, p, b, a, "CREATE DOMAIN ud AS INT", 0)
				}
				if tc.name == "alter_actions" {
					denied := batchDiag("23502", `null value in column "j" of relation "u" violates not-null constraint`)
					denied.Detail = "Failing row contains (9, null)."
					denied.Schema = "public"
					denied.Table = "u"
					denied.Column = "j"
					batchError(t, p, a, "INSERT INTO u VALUES(9,NULL)", denied)
					fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT i,j FROM u", []string{"i", "j"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(4)}}))
				}
			})
		})
	}
	batchUtilityRoles(t)
	batchUtilityNonrollback(t)
	batchUtilityComposition(t)
	batchSessionEffects(t)
	batchRoleComposition(t)
	batchIdentityPromotion(t)
}

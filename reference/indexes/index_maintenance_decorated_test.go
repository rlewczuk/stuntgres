package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func indexMaintenanceDecoratedSetup(run func(fixture.Step)) {
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE t(unused INT,n INT,s TEXT,v VARCHAR(8),b CHAR(8),p TEXT,j JSON)", 0),
		fixture.Exec(`INSERT INTO t VALUES(0,-1,'a ','x','x','tail','{"k":1}'),(0,2,'a','y','y',NULL,NULL),(0,2,'a','y','y',NULL,NULL),(0,NULL,NULL,NULL,NULL,NULL,NULL)`, 4),
		fixture.Exec("CREATE INDEX unsigned ON t(n oid_ops DESC NULLS LAST) INCLUDE(p,n,p,j)", 0),
		fixture.Exec(`CREATE INDEX padded ON t(s COLLATE "C" bpchar_ops ASC NULLS FIRST) INCLUDE(p,j)`, 0),
		fixture.Exec(`CREATE INDEX mixed ON t(lower(s) COLLATE "POSIX" text_pattern_ops,v varchar_ops,n int4_ops DESC NULLS FIRST) INCLUDE(p,p)`, 0),
		fixture.Exec(`CREATE INDEX patterns ON t(v COLLATE "C" varchar_pattern_ops,b COLLATE "POSIX" bpchar_pattern_ops)`, 0),
		fixture.Exec("ALTER TABLE t DROP COLUMN unused", 0),
	} {
		run(s)
	}
}

func indexMaintenanceDecoratedRows(run func(fixture.Step), changed bool) {
	rows := [][]any{{int64(-1), "a ", "x", "tail", `{"k":1}`}, {int64(2), "a", "y", nil, nil}, {int64(2), "a", "y", nil, nil}, {nil, nil, nil, nil, nil}}
	if changed {
		rows = [][]any{{int64(1), "b", "z", "new", `{"k":2}`}, {int64(2), "a", "y", "changed", nil}, {int64(2), "a", "y", "changed", nil}, {nil, nil, nil, nil, nil}}
	}
	column := "p"
	if changed {
		column = "payload"
	}
	step := fixture.Select("SELECT n,s,v,"+column+",j::text AS json_text FROM t ORDER BY n NULLS LAST", []string{"n", "s", "v", column, "json_text"}, []string{"INT4", "TEXT", "VARCHAR", "TEXT", "TEXT"}, rows)
	run(step)
	run(fixture.Prepared(step))
}

// All decorations are already admitted by CREATE INDEX. Maintenance must not
// narrow that implemented surface or reinterpret binary-compatible providers.
func TestReferenceIndexMaintenanceDecoratedLifecycle(t *testing.T) {
	indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		indexMaintenanceDecoratedSetup(run)
		indexMaintenanceDecoratedRows(run, false)
		definitions := []struct{ name, want string }{
			{"unsigned", "CREATE INDEX unsigned ON public.t USING btree (n oid_ops DESC NULLS LAST) INCLUDE (p, n, p, j)"},
			{"padded", `CREATE INDEX padded ON public.t USING btree (s COLLATE "C" bpchar_ops NULLS FIRST) INCLUDE (p, j)`},
			{"mixed", `CREATE INDEX mixed ON public.t USING btree (lower(s) COLLATE "POSIX" text_pattern_ops, v varchar_ops, n DESC) INCLUDE (p, p)`},
			{"patterns", `CREATE INDEX patterns ON public.t USING btree (v COLLATE "C" varchar_pattern_ops, b COLLATE "POSIX" bpchar_pattern_ops)`},
		}
		for _, c := range definitions {
			for _, prepared := range []bool{false, true} {
				route := "/direct"
				if prepared {
					route = "/prepared"
				}
				t.Run(c.name+route, func(t *testing.T) {
					step := fixture.Select("SELECT pg_get_indexdef($1::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{c.want}}, c.name)
					if prepared {
						step = fixture.Prepared(step)
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
				})
			}
		}
		for _, c := range []struct{ call, want string }{
			{"pg_get_indexdef('mixed'::regclass,4,false)", "p"},
			{"pg_get_indexdef('mixed'::regclass,5,true)", "p"},
			{"pg_get_indexdef('unsigned'::regclass,5,false)", "j"},
			{"pg_get_indexdef('unsigned'::regclass,6,false)", ""},
		} {
			t.Run(c.call, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT "+c.call+" AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{c.want}}))
			})
		}
		var oldUnsigned, oldMixed int64
		if b.PG {
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='unsigned'").Scan(&oldUnsigned); err != nil {
				t.Fatal(err)
			}
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='mixed'").Scan(&oldMixed); err != nil {
				t.Fatal(err)
			}
		}
		run(fixture.Exec("ALTER INDEX unsigned RENAME TO renamed_unsigned", 0))
		run(fixture.Prepared(fixture.Exec("REINDEX INDEX renamed_unsigned", 0)))
		run(fixture.Exec("ALTER TABLE t RENAME COLUMN p TO payload", 0))
		run(fixture.Exec("ALTER INDEX mixed RENAME TO renamed_mixed", 0))
		run(fixture.Prepared(fixture.Exec("REINDEX TABLE t", 0)))
		if b.PG {
			run(fixture.Select("SELECT oid=$1::oid AS same FROM pg_class WHERE relname='renamed_unsigned'", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, oldUnsigned))
			run(fixture.Select("SELECT oid=$1::oid AS same FROM pg_class WHERE relname='renamed_mixed'", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, oldMixed))
		}
		for _, c := range []struct {
			name  string
			natts int64
		}{{"padded", 3}, {"patterns", 2}, {"renamed_mixed", 5}, {"renamed_unsigned", 5}} {
			run(fixture.Select("SELECT relname,relnatts FROM pg_class WHERE relname=$1", []string{"relname", "relnatts"}, []string{"NAME", "INT2"}, [][]any{{c.name, c.natts}}, c.name))
		}
		run(fixture.Prepared(fixture.Exec("UPDATE t SET payload='changed' WHERE n=2", 2)))
		run(fixture.Exec("DELETE FROM t WHERE n=-1", 1))
		run(fixture.Exec(`INSERT INTO t(n,s,v,b,payload,j) VALUES(1,'b','z','z','new','{"k":2}')`, 1))
		indexMaintenanceDecoratedRows(run, true)
		run(fixture.Exec("REINDEX TABLE t", 0))
		for _, q := range []string{"ALTER INDEX renamed_unsigned SET(fillfactor=70,deduplicate_items=off)", "ALTER INDEX renamed_unsigned RESET(fillfactor,deduplicate_items)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.ImplicitError("DROP INDEX renamed_unsigned,missing", fixture.Diagnostic{Code: "42704", Message: `index "missing" does not exist`}))
		run(fixture.Prepared(fixture.ImplicitError("DROP INDEX renamed_mixed,missing", fixture.Diagnostic{Code: "42704", Message: `index "missing" does not exist`})))
		for _, name := range []string{"renamed_mixed", "renamed_unsigned"} {
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, [][]any{{name}}, name))
		}
		run(fixture.Exec("DROP INDEX renamed_unsigned,patterns,patterns CASCADE", 0))
		run(fixture.Prepared(fixture.Exec("REINDEX TABLE t", 0)))
		indexMaintenanceDecoratedRows(run, true)
		for _, c := range []struct {
			name    string
			present bool
		}{{"padded", true}, {"patterns", false}, {"renamed_mixed", true}, {"renamed_unsigned", false}} {
			var rows [][]any
			if c.present {
				rows = [][]any{{c.name}}
			}
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, rows, c.name))
		}
	})
}

func TestReferenceIndexMaintenanceDecoratedMetadataOracle(t *testing.T) {
	indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		indexMaintenanceDecoratedSetup(run)
		// Independently observed released identities from the real PG18 oracle.
		// Standalone class facts are shared; no pg_am source is needed.
		run(fixture.Select(`SELECT opcname,oid::bigint,opcintype::regtype::text FROM pg_opclass WHERE opcmethod=403 AND opcname IN ('oid_ops','bpchar_ops','int4_ops','varchar_ops','text_pattern_ops','varchar_pattern_ops','bpchar_pattern_ops') ORDER BY opcname`, []string{"opcname", "oid", "opcintype"}, []string{"NAME", "INT8", "TEXT"}, [][]any{
			{"bpchar_ops", int64(10004), "character"},
			{"bpchar_pattern_ops", int64(4219), "character"},
			{"int4_ops", int64(1978), "integer"},
			{"oid_ops", int64(1981), "oid"},
			{"text_pattern_ops", int64(4217), "text"},
			{"varchar_ops", int64(10044), "text"},
			{"varchar_pattern_ops", int64(4218), "text"},
		}))
		// Complete vectors and expression/predicate fields remain PG-only:
		// native pg_index does not expose those fields or indclass.
		metadata := func(label string) {
			if !b.PG {
				return
			}
			t.Run(label, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(`SELECT c.relname,i.indnatts,i.indnkeyatts,i.indisunique,i.indnullsnotdistinct,i.indisprimary,i.indisexclusion,i.indimmediate,i.indisclustered,i.indisvalid,i.indcheckxmin,i.indisready,i.indislive,i.indisreplident,i.indkey::text,i.indcollation::text,i.indoption::text,i.indexprs IS NULL AS no_expr,i.indpred IS NULL AS no_pred,i.indexrelid=c.oid AS same_index,i.indrelid=p.oid AS same_parent FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_class p ON p.oid=i.indrelid WHERE p.relname='t' ORDER BY c.relname`, []string{"relname", "indnatts", "indnkeyatts", "indisunique", "indnullsnotdistinct", "indisprimary", "indisexclusion", "indimmediate", "indisclustered", "indisvalid", "indcheckxmin", "indisready", "indislive", "indisreplident", "indkey", "indcollation", "indoption", "no_expr", "no_pred", "same_index", "same_parent"}, []string{"NAME", "INT2", "INT2", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{
					{"mixed", int64(5), int64(3), false, false, false, false, true, false, true, false, true, true, false, "0 4 2 6 6", "951 100 0", "0 0 3", false, true, true, true},
					{"padded", int64(3), int64(1), false, false, false, false, true, false, true, false, true, true, false, "3 6 7", "950", "2", true, true, true, true},
					{"patterns", int64(2), int64(2), false, false, false, false, true, false, true, false, true, true, false, "4 5", "950 951", "0 0", true, true, true, true},
					{"unsigned", int64(5), int64(1), false, false, false, false, true, false, true, false, true, true, false, "2 6 2 6 7", "0", "1", true, true, true, true},
				}))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(`SELECT c.relname,i.indclass::text FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_class p ON p.oid=i.indrelid WHERE p.relname='t' ORDER BY c.relname`, []string{"relname", "indclass"}, []string{"NAME", "TEXT"}, [][]any{
					{"mixed", "4217 10044 1978"}, {"padded", "10004"}, {"patterns", "4218 4219"}, {"unsigned", "1981"},
				}))
			})
		}
		metadata("initial_complete_vectors")
		if b.PG {
			t.Run("actual_key_classes", func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(`SELECT c.relname,k.ordinality::bigint AS key,o.opcname,o.opcintype::regtype::text AS input,o.opcdefault FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_class p ON p.oid=i.indrelid CROSS JOIN LATERAL unnest(i.indclass) WITH ORDINALITY AS k(oid,ordinality) JOIN pg_opclass o ON o.oid=k.oid WHERE p.relname='t' ORDER BY c.relname,k.ordinality`, []string{"relname", "key", "opcname", "input", "opcdefault"}, []string{"NAME", "INT8", "NAME", "TEXT", "BOOL"}, [][]any{
					{"mixed", int64(1), "text_pattern_ops", "text", false}, {"mixed", int64(2), "varchar_ops", "text", false}, {"mixed", int64(3), "int4_ops", "integer", true},
					{"padded", int64(1), "bpchar_ops", "character", true}, {"patterns", int64(1), "varchar_pattern_ops", "text", false}, {"patterns", int64(2), "bpchar_pattern_ops", "character", false}, {"unsigned", int64(1), "oid_ops", "oid", true},
				}))
			})
			t.Run("original_expression_only", func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(`SELECT c.relname,pg_get_expr(i.indexprs,i.indrelid) AS expression FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_class p ON p.oid=i.indrelid WHERE p.relname='t' ORDER BY c.relname`, []string{"relname", "expression"}, []string{"NAME", "TEXT"}, [][]any{{"mixed", "lower(s)"}, {"padded", nil}, {"patterns", nil}, {"unsigned", nil}}))
			})
		}
		run(fixture.Exec("REINDEX INDEX unsigned", 0))
		run(fixture.Prepared(fixture.Exec("REINDEX TABLE t", 0)))
		run(fixture.Exec("ALTER TABLE t RENAME COLUMN p TO payload", 0))
		metadata("after_rebuild_and_include_rename")
	})
}

func TestReferenceIndexMaintenanceEnumProviders(t *testing.T) {
	indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TYPE mood AS ENUM('z','a')", 0), fixture.Exec("CREATE DOMAIN dm AS mood", 0), fixture.Exec("CREATE DOMAIN nested AS dm", 0),
			fixture.Exec("CREATE TABLE t(m mood,d nested,p TEXT)", 0), fixture.Exec("INSERT INTO t VALUES('a','a','last'),('z','z','first'),(NULL,NULL,NULL)", 3),
			fixture.Exec("CREATE INDEX direct_enum ON t(m) INCLUDE(p)", 0), fixture.Exec("CREATE INDEX domain_enum ON t(d enum_ops) INCLUDE(p,p)", 0),
		} {
			run(s)
		}
		expected := fixture.Select("SELECT m::text AS m,d::text AS d,p FROM t ORDER BY t.m NULLS LAST", []string{"m", "d", "p"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"z", "z", "first"}, {"a", "a", "last"}, {nil, nil, nil}})
		run(expected)
		run(fixture.Prepared(expected))
		run(fixture.Exec("ALTER INDEX domain_enum RENAME TO renamed_enum", 0))
		run(fixture.Prepared(fixture.Exec("REINDEX INDEX renamed_enum", 0)))
		run(fixture.Exec("REINDEX TABLE t", 0))
		run(expected)
		if b.PG {
			run(fixture.Select(`SELECT c.relname,i.indnatts,i.indnkeyatts,i.indkey::text,i.indclass::text,i.indcollation::text,i.indoption::text,i.indexprs IS NULL AS no_expr FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE c.relname IN ('direct_enum','renamed_enum') ORDER BY c.relname`, []string{"relname", "indnatts", "indnkeyatts", "indkey", "indclass", "indcollation", "indoption", "no_expr"}, []string{"NAME", "INT2", "INT2", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{"direct_enum", int64(2), int64(1), "1 3", "10069", "0", "0", true}, {"renamed_enum", int64(3), int64(1), "2 3 3", "10069", "0", "0", true}}))
		}
		run(fixture.Exec("DROP INDEX direct_enum,renamed_enum", 0))
		run(expected)
	})
}

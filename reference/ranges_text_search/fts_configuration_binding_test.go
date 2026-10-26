package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func configurationVectorMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 1 || types[0].Name() != "normalized" || types[0].DatabaseTypeName() != "TSVECTOR" {
		t.Fatal("typed conversion metadata", types)
	}
	scan := reflect.TypeFor[string]()
	if pg {
		scan = reflect.TypeFor[any]()
	}
	if types[0].ScanType() != scan {
		t.Fatal("conversion ScanType", types[0].ScanType())
	}
	if n, known := types[0].Nullable(); n || known {
		t.Fatal("computed conversion nullability", n, known)
	}
	if n, known := types[0].Length(); n != 0 || known {
		t.Fatal("conversion length", n, known)
	}
	if p, s, known := types[0].DecimalSize(); p != 0 || s != 0 || known {
		t.Fatal("conversion decimal size", p, s, known)
	}
}
func TestReferenceFTSConfigurationBinding(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"config_binding_actor"})
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.config_binding (COPY=pg_catalog.simple)", 0))
		query := "SELECT to_tsvector($1::regconfig,$2::text) AS normalized"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		for _, tc := range []struct {
			config, text any
			want         any
		}{{"public.config_binding", "The Fat Rats", "'fat':2 'rats':3 'the':1"}, {[]byte("public.config_binding"), []byte("Rats rats"), "'rats':1,2"}, {"public.config_binding", "", ""}, {nil, "Words", nil}, {"public.config_binding", nil, nil}, {"public.config_binding", "YeS", "'yes':1"}} {
			s := fixture.Select(query, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{tc.want}}, tc.config, tc.text)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
			run(s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.Exec("PREPARE config_sql(regconfig,text) AS SELECT to_tsvector($1,$2) AS normalized", 0))
		run(ftsConfigurationVector("EXECUTE config_sql('public.config_binding','The Fat Rats')", "'fat':2 'rats':3 'the':1"))
		run(fixture.Exec("DEALLOCATE config_sql", 0))
		for _, prepared := range []bool{false, true} {
			s := ftsConfigurationError("SELECT to_tsvector(0::regconfig,'word')", "XX000", "cache lookup failed for text search configuration 0")
			s.Prepared = prepared
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
		}
		for _, tc := range []struct{ query, message string }{
			{"SELECT to_tsvector(true,'words'::text)", "function to_tsvector(boolean, text) does not exist"},
			{"SELECT to_tsvector('simple'::regconfig,'words'::text,'extra'::text)", "function to_tsvector(regconfig, text, text) does not exist"},
		} {
			d := fixture.ControlsReferenceDiagnostic("42883", tc.message)
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			d.Position = "8"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.query, d)
				s.Prepared = prepared
				s.SelectRows = true
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
		}
		run(fixture.Exec("CREATE ROLE config_binding_actor", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO config_binding_actor", 0))
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, sql := range []string{"RESET ROLE", "GRANT EXECUTE ON FUNCTION pg_catalog.to_tsvector(regconfig,text) TO PUBLIC"} {
				if _, err := c.ExecContext(cleanup, sql); err != nil {
					t.Errorf("routine authority cleanup: %s: %v", sql, err)
				}
			}
		}()
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.to_tsvector(regconfig,text) FROM PUBLIC", 0))
		run(fixture.Exec("SET ROLE config_binding_actor", 0))
		for _, prepared := range []bool{false, true} {
			s := ftsConfigurationError("SELECT to_tsvector('public.config_binding'::regconfig,$1::text) AS normalized", "42501", "permission denied for function to_tsvector")
			s.Args = []any{"Words"}
			s.Prepared = prepared
			s.ErrorStage = "execute"
			s.SelectRows = true
			run(s)
		}
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.to_tsvector(regconfig,text) TO PUBLIC", 0))
	})
}
func TestReferenceFTSConfigurationMetadata(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"config_metadata_reader"})
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(ftsConfigurationMaps("pg_catalog.simple", ftsConfigurationSimpleMaps()))
		run(fixture.Select("SELECT oid::bigint,prorettype::bigint,proisstrict,provolatile,proparallel,proargtypes::text,proargnames::text FROM pg_proc WHERE oid=3745", []string{"oid", "prorettype", "proisstrict", "provolatile", "proparallel", "proargtypes", "proargnames"}, []string{"INT8", "INT8", "BOOL", "CHAR", "CHAR", "TEXT", "TEXT"}, [][]any{{int64(3745), int64(3614), true, "i", "s", "3734 25", nil}}))
		run(fixture.Select("SELECT attname,atttypid::bigint,attnotnull FROM pg_attribute WHERE attrelid=3603 AND attnum>0 ORDER BY attnum", []string{"attname", "atttypid", "attnotnull"}, []string{"NAME", "INT8", "BOOL"}, [][]any{{"mapcfg", int64(26), true}, {"maptokentype", int64(23), true}, {"mapseqno", int64(23), true}, {"mapdict", int64(26), true}}))
		run(fixture.Select("SELECT relname,relkind,relnatts,relhasindex,reltype='pg_catalog.pg_ts_config_map'::regtype::oid AS linked FROM pg_class WHERE oid=3603", []string{"relname", "relkind", "relnatts", "relhasindex", "linked"}, []string{"NAME", "CHAR", "INT2", "BOOL", "BOOL"}, [][]any{{"pg_ts_config_map", "r", int64(4), true, true}}))
		for _, tc := range []struct {
			sql  string
			rows [][]any
		}{{"SELECT to_tsvector('simple'::regconfig,'Words') AS normalized", [][]any{{"'words':1"}}}, {"SELECT to_tsvector('simple'::regconfig,'') AS normalized", [][]any{{""}}}, {"SELECT to_tsvector('simple'::regconfig,NULL::text) AS normalized", [][]any{{nil}}}, {"SELECT to_tsvector('simple'::regconfig,'Words') AS normalized WHERE false", nil}} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(tc.sql, []string{"normalized"}, []string{"TSVECTOR"}, tc.rows)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
				run(s)
			}
		}
		links := fixture.Select("SELECT fkcols,pktable::oid::bigint,pkcols,is_array,is_opt FROM pg_get_catalog_foreign_keys() WHERE fktable::oid=3603", []string{"fkcols", "pktable", "pkcols", "is_array", "is_opt"}, []string{"_TEXT", "INT8", "_TEXT", "BOOL", "BOOL"}, [][]any{{"{mapcfg}", int64(3602), "{oid}", false, false}, {"{mapdict}", int64(3600), "{oid}", false, false}})
		links.Bag = true
		run(links)
		run(fixture.Prepared(links))
		run(fixture.Exec("CREATE ROLE config_metadata_reader", 0))
		run(fixture.Exec("SET ROLE config_metadata_reader", 0))
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := c.ExecContext(cleanup, "RESET ROLE"); err != nil {
				t.Error(err)
			}
		}()
		for _, query := range []string{"INSERT INTO pg_catalog.pg_ts_config_map SELECT * FROM pg_catalog.pg_ts_config_map WHERE false", "UPDATE pg_catalog.pg_ts_config_map SET mapdict=3765 WHERE false", "DELETE FROM pg_catalog.pg_ts_config_map WHERE false"} {
			for _, prepared := range []bool{false, true} {
				s := ftsConfigurationError(query, "42501", "permission denied for table pg_ts_config_map")
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
			}
		}
		run(ftsConfigurationMaps("pg_catalog.simple", ftsConfigurationSimpleMaps()))
		run(fixture.Exec("RESET ROLE", 0))
	})
}
func TestReferenceFTSConfigurationAtomicity(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.config_atomic (COPY=pg_catalog.simple)", 0))
		var oid, owner, namespace, parser int64
		identitySQL := "SELECT oid::bigint,cfgowner::bigint,cfgnamespace::bigint,cfgparser::bigint FROM pg_ts_config WHERE cfgname='config_atomic'"
		if err := c.QueryRowContext(p.Ctx, identitySQL).Scan(&oid, &owner, &namespace, &parser); err != nil || oid <= 0 || owner <= 0 || namespace <= 0 || parser != 3722 {
			t.Fatal("configuration identity", oid, owner, namespace, parser, err)
		}
		unchanged := func() {
			run(fixture.Select(identitySQL, []string{"oid", "cfgowner", "cfgnamespace", "cfgparser"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{oid, owner, namespace, parser}}))
			run(fixture.Select("SELECT refclassid::bigint,refobjid::bigint,deptype FROM pg_depend WHERE classid=3602 AND objid=$1::oid ORDER BY refclassid,refobjid", []string{"refclassid", "refobjid", "deptype"}, []string{"INT8", "INT8", "CHAR"}, [][]any{{int64(2615), namespace, "n"}}, oid))
		}
		unchanged()
		duplicate := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "pg_ts_config_map_index"`)
		duplicate.Detail = fmt.Sprintf("Key (mapcfg, maptokentype, mapseqno)=(%d, 1, 1) already exists.", oid)
		duplicate.Schema = "pg_catalog"
		duplicate.Table = "pg_ts_config_map"
		duplicate.Constraint = "pg_ts_config_map_index"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("ALTER TEXT SEARCH CONFIGURATION public.config_atomic ADD MAPPING FOR blank,asciiword WITH simple", duplicate)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			unchanged()
			run(ftsConfigurationMaps("public.config_atomic", ftsConfigurationSimpleMaps()))
		}
		for _, sql := range []string{"ALTER TEXT SEARCH CONFIGURATION public.config_atomic ALTER MAPPING FOR asciiword,not_a_token WITH simple", "ALTER TEXT SEARCH CONFIGURATION public.config_atomic DROP MAPPING FOR asciiword,blank", "ALTER TEXT SEARCH CONFIGURATION public.config_atomic ALTER MAPPING FOR asciiword WITH simple,config_absent_dict"} {
			code, message := "22023", `token type "not_a_token" does not exist`
			if sql == "ALTER TEXT SEARCH CONFIGURATION public.config_atomic DROP MAPPING FOR asciiword,blank" {
				code, message = "42704", `mapping for token type "blank" does not exist`
			}
			if sql == "ALTER TEXT SEARCH CONFIGURATION public.config_atomic ALTER MAPPING FOR asciiword WITH simple,config_absent_dict" {
				code, message = "42704", `text search dictionary "config_absent_dict" does not exist`
			}
			for _, prepared := range []bool{false, true} {
				s := ftsConfigurationError(sql, code, message)
				s.Prepared = prepared
				run(s)
				unchanged()
				run(ftsConfigurationMaps("public.config_atomic", ftsConfigurationSimpleMaps()))
				run(ftsConfigurationVector("SELECT to_tsvector('public.config_atomic'::regconfig,$1::text) AS normalized", "'fat':2 'rats':3 'the':1", "The Fat Rats"))
			}
		}
		for _, sql := range []string{"BEGIN", "SAVEPOINT config_save", "ALTER TEXT SEARCH CONFIGURATION public.config_atomic DROP MAPPING FOR asciiword"} {
			run(fixture.Exec(sql, 0))
		}
		run(ftsConfigurationVector("SELECT to_tsvector('public.config_atomic'::regconfig,$1::text) AS normalized", "", "The Fat Rats"))
		run(fixture.Exec("ROLLBACK TO config_save", 0))
		run(ftsConfigurationMaps("public.config_atomic", ftsConfigurationSimpleMaps()))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_atomic DROP MAPPING FOR asciiword", 0))
		run(fixture.Exec("ROLLBACK", 0))
		run(ftsConfigurationMaps("public.config_atomic", ftsConfigurationSimpleMaps()))
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err := c.ExecContext(ctx, "ALTER TEXT SEARCH CONFIGURATION public.config_atomic DROP MAPPING FOR asciiword")
			if !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary canceled context", err)
			}
			run(ftsConfigurationMaps("public.config_atomic", ftsConfigurationSimpleMaps()))
		}
		query := "SELECT to_tsvector('public.config_atomic'::regconfig,$1::text) AS normalized"
		held, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		input := strings.Repeat("word ", 2_000_000)
		fixture.BinaryConversionActiveCancel(t, p, b, c, query, "ConvertTSVector", func(ctx context.Context) fixture.RelationOutcome {
			rows, err := held.QueryContext(ctx, input)
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		run(ftsConfigurationMaps("public.config_atomic", ftsConfigurationSimpleMaps()))
		unchanged()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}, b.Recorder, ftsConfigurationVector(query, "'rats':1", "Rats"))
	})
}

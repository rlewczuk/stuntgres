package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGiSTOptions(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE gist_options(id integer,v tsvector,w tsvector,p point)", 0))
		run(fixture.Exec("INSERT INTO gist_options VALUES(1,'a:1','b:2','(0,0)'),(2,NULL,NULL,NULL)", 2))
		for _, q := range []string{
			"CREATE INDEX gi_opt32 ON gist_options USING gist(v tsvector_ops(siglen=32)) WITH(buffering=ON,fillfactor=80)",
			"CREATE INDEX gi_opt124 ON gist_options USING gist(v tsvector_ops(siglen=124))",
			"CREATE INDEX gi_opt_minmax ON gist_options USING gist(v tsvector_ops(siglen=1),w tsvector_ops(siglen=2024)) INCLUDE(id)",
			"CREATE INDEX gi_opt_absent ON gist_options USING gist(v)",
			"CREATE TABLE gist_option_exclusion(p point,CONSTRAINT gist_option_ex EXCLUDE USING gist(p WITH ~=) WITH(fillfactor=75,buffering=off))",
		} {
			run(fixture.Exec(q, 0))
		}
		check := func(options any, definition string) {
			classRoutes(t, p, b, conn, fixture.Select("SELECT reloptions::text AS options FROM pg_class WHERE relname='gi_opt32'", []string{"options"}, []string{"TEXT"}, [][]any{{options}}))
			classRoutes(t, p, b, conn, fixture.Select("SELECT pg_get_indexdef('gi_opt32'::regclass) AS definition,pg_get_indexdef('gi_opt32'::regclass,1,true) AS key", []string{"definition", "key"}, []string{"TEXT", "TEXT"}, [][]any{{definition, "v"}}))
		}
		check("{buffering=on,fillfactor=80}", "CREATE INDEX gi_opt32 ON public.gist_options USING gist (v tsvector_ops (siglen='32')) WITH (buffering='on', fillfactor='80')")
		classRoutes(t, p, b, conn, fixture.Select("SELECT c.relname,a.attnum,a.attoptions::text AS options FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid WHERE c.relname IN ('gi_opt32','gi_opt124','gi_opt_minmax','gi_opt_absent') AND a.attnum>0 ORDER BY c.relname,a.attnum", []string{"relname", "attnum", "options"}, []string{"NAME", "INT2", "TEXT"}, [][]any{{"gi_opt124", int64(1), "{siglen=124}"}, {"gi_opt32", int64(1), "{siglen=32}"}, {"gi_opt_absent", int64(1), nil}, {"gi_opt_minmax", int64(1), "{siglen=1}"}, {"gi_opt_minmax", int64(2), "{siglen=2024}"}, {"gi_opt_minmax", int64(3), nil}}))
		for _, tc := range []struct{ name, value string }{{"gi_opt_fraction", "1.5"}, {"gi_opt_zeros", "0032"}} {
			run(fixture.Prepared(fixture.Exec("CREATE INDEX "+tc.name+" ON gist_options USING gist(v tsvector_ops(siglen='"+tc.value+"'))", 0)))
			classRoutes(t, p, b, conn, fixture.Select("SELECT attoptions::text AS options FROM pg_attribute WHERE attrelid='"+tc.name+"'::regclass AND attnum=1", []string{"options"}, []string{"TEXT"}, [][]any{{"{siglen=" + tc.value + "}"}}))
			classRoutes(t, p, b, conn, fixture.Select("SELECT pg_get_indexdef('"+tc.name+"'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE INDEX " + tc.name + " ON public.gist_options USING gist (v tsvector_ops (siglen='" + tc.value + "'))"}}))
		}
		run(fixture.Exec("BEGIN", 0))
		defer conn.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("SAVEPOINT option_prefix", 0))
		run(fixture.Prepared(fixture.Exec("ALTER INDEX gi_opt32 SET(buffering=auto)", 0)))
		check("{fillfactor=80,buffering=auto}", "CREATE INDEX gi_opt32 ON public.gist_options USING gist (v tsvector_ops (siglen='32')) WITH (fillfactor='80', buffering=auto)")
		run(fixture.Exec("ROLLBACK TO option_prefix", 0))
		check("{buffering=on,fillfactor=80}", "CREATE INDEX gi_opt32 ON public.gist_options USING gist (v tsvector_ops (siglen='32')) WITH (buffering='on', fillfactor='80')")
		run(fixture.Exec("ALTER INDEX gi_opt32 RESET(buffering,fillfactor)", 0))
		run(fixture.Exec("COMMIT", 0))
		check(nil, "CREATE INDEX gi_opt32 ON public.gist_options USING gist (v tsvector_ops (siglen='32'))")
		run(fixture.Exec("REINDEX INDEX gi_opt32", 0))
		check(nil, "CREATE INDEX gi_opt32 ON public.gist_options USING gist (v tsvector_ops (siglen='32'))")
		run(fixture.Exec("ALTER INDEX gi_opt124 SET(buffering=off,fillfactor=80)", 0))
		run(fixture.Exec("CREATE TABLE gist_options_copy(LIKE gist_options INCLUDING INDEXES)", 0))
		classRoutes(t, p, b, conn, fixture.Select("SELECT count(*) AS n FROM pg_attribute a JOIN pg_index i ON i.indexrelid=a.attrelid WHERE i.indrelid='gist_options_copy'::regclass AND a.attoptions IS NOT NULL", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}))
		copied := fixture.Select("SELECT a.attoptions::text AS options FROM pg_attribute a JOIN pg_index i ON i.indexrelid=a.attrelid WHERE i.indrelid='gist_options_copy'::regclass AND a.attoptions IS NOT NULL", []string{"options"}, []string{"TEXT"}, [][]any{{"{siglen=32}"}, {"{siglen=124}"}, {"{siglen=1}"}, {"{siglen=2024}"}, {"{siglen=1.5}"}, {"{siglen=0032}"}})
		copied.Bag = true
		classRoutes(t, p, b, conn, copied)
		run(fixture.Exec("INSERT INTO gist_options_copy VALUES(10,'copy:1','other:2','(2,0)'),(11,NULL,NULL,NULL),(12,'copy:1','other:2','(2,0)')", 3))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id,v::text AS v,w::text AS w,p::text AS p FROM gist_options_copy ORDER BY id", []string{"id", "v", "w", "p"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(10), "'copy':1", "'other':2", "(2,0)"}, {int64(11), nil, nil, nil}, {int64(12), "'copy':1", "'other':2", "(2,0)"}}))
		run(fixture.Prepared(fixture.Exec("UPDATE gist_options_copy SET v='changed:3',w='updated:4',p='(3,4)' WHERE id=10", 1)))
		run(fixture.Exec("DELETE FROM gist_options_copy WHERE id=12", 1))
		run(fixture.Exec("INSERT INTO gist_options_copy VALUES(13,'new:5','copy:6','(1,0)')", 1))
		copyRows := fixture.Select("SELECT id,v::text AS v,w::text AS w,p::text AS p FROM gist_options_copy ORDER BY id", []string{"id", "v", "w", "p"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(10), "'changed':3", "'updated':4", "(3,4)"}, {int64(11), nil, nil, nil}, {int64(13), "'new':5", "'copy':6", "(1,0)"}})
		classRoutes(t, p, b, conn, copyRows)
		run(fixture.Exec("ALTER INDEX gi_opt124 SET(buffering=on)", 0))
		classRoutes(t, p, b, conn, fixture.Select("SELECT c.reloptions::text AS options FROM pg_class c JOIN pg_index i ON i.indexrelid=c.oid WHERE i.indrelid='gist_options_copy'::regclass AND c.reloptions IS NOT NULL", []string{"options"}, []string{"TEXT"}, [][]any{{"{buffering=off,fillfactor=80}"}}))
		run(fixture.Exec("CREATE TABLE gist_options_part(id integer,v tsvector) PARTITION BY RANGE(id)", 0))
		run(fixture.Exec("CREATE TABLE gist_options_leaf PARTITION OF gist_options_part FOR VALUES FROM(0) TO(10)", 0))
		run(fixture.Exec("CREATE INDEX gist_options_part_i ON gist_options_part USING gist(v tsvector_ops(siglen=32)) WITH(buffering=off,fillfactor=80)", 0))
		classRoutes(t, p, b, conn, fixture.Select("SELECT i.indrelid='gist_options_part'::regclass AS parent,c.reloptions::text AS options,a.attoptions::text AS key_options FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum=1 WHERE i.indrelid IN ('gist_options_part'::regclass,'gist_options_leaf'::regclass) ORDER BY parent", []string{"parent", "options", "key_options"}, []string{"BOOL", "TEXT", "TEXT"}, [][]any{{false, "{buffering=off,fillfactor=80}", "{siglen=32}"}, {true, "{buffering=off,fillfactor=80}", "{siglen=32}"}}))
		run(fixture.Exec("INSERT INTO gist_options_part VALUES(1,'part:1')", 1))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id,v::text AS v FROM gist_options_part ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "'part':1"}}))
		run(fixture.Prepared(fixture.Exec("UPDATE gist_options_part SET v='changed:2' WHERE id=1", 1)))
		run(fixture.Exec("INSERT INTO gist_options_part VALUES(2,NULL),(3,'part:3')", 2))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id,v::text AS v FROM gist_options_leaf ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "'changed':2"}, {int64(2), nil}, {int64(3), "'part':3"}}))
		run(fixture.Exec("INSERT INTO gist_options VALUES(3,'c:3','d:4','(1,0)')", 1))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id,v::text AS v FROM gist_options ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "'a':1"}, {int64(2), nil}, {int64(3), "'c':3"}}))
		classRoutes(t, p, b, conn, copyRows)
	})
}

func TestReferenceGiSTDiagnostics(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE gist_diag(n integer,i inet,p point,v tsvector,t text,l line,s lseg,h path,c cidr)", 0))
		hint := "You must specify an operator class for the index or define a default operator class for the data type."
		for _, tc := range []struct {
			suffix, code, message, detail, hint string
			unique                              bool
		}{
			{"(n)", "42704", `data type integer has no default operator class for access method "gist"`, "", hint, false},
			{"(i)", "42704", `data type inet has no default operator class for access method "gist"`, "", hint, false},
			{"(t)", "42704", `data type text has no default operator class for access method "gist"`, "", hint, false},
			{"(l)", "42704", `data type line has no default operator class for access method "gist"`, "", hint, false},
			{"(s)", "42704", `data type lseg has no default operator class for access method "gist"`, "", hint, false},
			{"(h)", "42704", `data type path has no default operator class for access method "gist"`, "", hint, false},
			{"(c)", "42704", `data type cidr has no default operator class for access method "gist"`, "", hint, false},
			{"(p missing_ops)", "42704", `operator class "missing_ops" does not exist for access method "gist"`, "", "", false},
			{"(n int4_ops)", "42704", `operator class "int4_ops" does not exist for access method "gist"`, "", "", false},
			{"(n point_ops)", "42804", `operator class "point_ops" does not accept data type integer`, "", "", false},
			{"(p)", "0A000", `access method "gist" does not support unique indexes`, "", "", true},
			{"(p DESC)", "0A000", `access method "gist" does not support ASC/DESC options`, "", "", false},
			{"(p ASC)", "0A000", `access method "gist" does not support ASC/DESC options`, "", "", false},
			{"(p NULLS FIRST)", "0A000", `access method "gist" does not support NULLS FIRST/LAST options`, "", "", false},
			{"(p NULLS LAST)", "0A000", `access method "gist" does not support NULLS FIRST/LAST options`, "", "", false},
			{"(p) WITH(buffering=wrong)", "22023", `invalid value for enum option "buffering": wrong`, `Valid values are "on", "off", and "auto".`, "", false},
			{"(p) WITH(buffering=true)", "22023", `invalid value for enum option "buffering": true`, `Valid values are "on", "off", and "auto".`, "", false},
			{"(p) WITH(buffering=1)", "22023", `invalid value for enum option "buffering": 1`, `Valid values are "on", "off", and "auto".`, "", false},
			{"(p) WITH(fillfactor=9)", "22023", `value 9 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`, "", false},
			{"(p) WITH(fillfactor=101)", "22023", `value 101 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`, "", false},
			{"(p) WITH(fillfactor='bad')", "22023", `invalid value for integer option "fillfactor": bad`, "", "", false},
			{"(p) WITH(unknown=1)", "22023", `unrecognized parameter "unknown"`, "", "", false},
			{"(p point_ops(siglen=32))", "22023", `operator class point_ops has no options`, "", "", false},
			{"(v tsvector_ops(siglen=0))", "22023", `value 0 out of bounds for option "siglen"`, `Valid values are between "1" and "2024".`, "", false},
			{"(v tsvector_ops(siglen=2025))", "22023", `value 2025 out of bounds for option "siglen"`, `Valid values are between "1" and "2024".`, "", false},
			{"(v tsvector_ops(siglen=abc))", "22023", `invalid value for integer option "siglen": abc`, "", "", false},
			{"(v tsvector_ops(siglen=true))", "22023", `invalid value for integer option "siglen": true`, "", "", false},
			{"(v tsvector_ops(siglen=1,siglen=2))", "22023", `parameter "siglen" specified more than once`, "", "", false},
			{"(p) WITH(buffering=on,buffering=off)", "22023", `parameter "buffering" specified more than once`, "", "", false},
			{"(v tsvector_ops(unknown=1))", "22023", `unrecognized parameter "unknown"`, "", "", false},
			{"(p) WITH(siglen=32)", "22023", `unrecognized parameter "siglen"`, "", "", false},
			{"(p) WITH(deduplicate_items=on)", "22023", `unrecognized parameter "deduplicate_items"`, "", "", false},
		} {
			prefix := "CREATE INDEX gd ON gist_diag USING gist"
			if tc.unique {
				prefix = "CREATE UNIQUE INDEX gd ON gist_diag USING gist"
			}
			for _, unicode := range []string{"", "/*é*/ "} {
				step := fixture.ImplicitError(unicode+prefix+tc.suffix, fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail, Hint: tc.hint})
				step.ErrorStage = "execute"
				classRoutes(t, p, b, conn, step)
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='gd'", []string{"relname"}, []string{"NAME"}, nil))
			}
			run(fixture.Exec("CREATE INDEX gd ON gist_diag USING gist(p)", 0))
			run(fixture.Exec("DROP INDEX gd", 0))
		}
		run(fixture.Exec("CREATE INDEX gd ON gist_diag USING gist(p) WITH(buffering=off)", 0))
		classRoutes(t, p, b, conn, fixture.ImplicitError("ALTER INDEX gd SET(buffering=wrong)", fixture.Diagnostic{Code: "22023", Message: `invalid value for enum option "buffering": wrong`, Detail: `Valid values are "on", "off", and "auto".`}))
		run(fixture.Select("SELECT reloptions::text AS options FROM pg_class WHERE relname='gd'", []string{"options"}, []string{"TEXT"}, [][]any{{"{buffering=off}"}}))
		classRoutes(t, p, b, conn, fixture.ImplicitError("ALTER INDEX gd RESET(buffering=on)", fixture.Diagnostic{Code: "42601", Message: "RESET must not include values for parameters"}))
		for _, tc := range []struct{ typ, operator, code, message, detail, hint string }{
			{"point", "=", "42883", "operator does not exist: point = point", "", "No operator matches the given name and argument types. You might need to add explicit type casts."},
			{"box", "=", "42809", `operator =(box,box) is not a member of operator family "box_ops"`, "The exclusion operator must be related to the index operator class for the constraint.", ""},
			{"point", "<->", "42809", `operator <->(point,point) is not a member of operator family "point_ops"`, "The exclusion operator must be related to the index operator class for the constraint.", ""},
			{"tsquery", "@>", "42809", "operator @>(tsquery,tsquery) is not commutative", "Only commutative operators can be used in exclusion constraints.", ""},
		} {
			classRoutes(t, p, b, conn, fixture.ImplicitError("CREATE TABLE gist_bad_ex(v "+tc.typ+",EXCLUDE USING gist(v WITH "+tc.operator+"))", fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail, Hint: tc.hint}))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='gist_bad_ex'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Exec("CREATE TABLE gist_bad_ex(v point,EXCLUDE USING gist(v WITH ~=))", 0))
			run(fixture.Exec("DROP TABLE gist_bad_ex", 0))
		}
	})
}

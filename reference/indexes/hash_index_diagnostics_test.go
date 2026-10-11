package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// Semantic DDL must remain quiet at Prepare and revalidate on every Execute.
// referenceImplicitError supplies ERROR/ERROR; every unlisted logical field is
// independently required to be absent, including position and object fields.
func hashIndexSemanticFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step), q string, want fixture.Diagnostic) {
	t.Helper()
	s := fixture.ImplicitError(q, want)
	s.ErrorStage = "execute"
	run(s)
	run(fixture.Prepared(s))
	for _, nonContext := range []bool{false, true} {
		var stmt *sql.Stmt
		var err error
		if nonContext {
			stmt, err = b.DB.Prepare(q)
		} else {
			stmt, err = b.DB.PrepareContext(p.Ctx, q)
		}
		if err != nil {
			t.Fatal("semantic failure at Prepare", err)
		}
		for range 2 {
			s.NonContext = nonContext
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceHashIndexDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, definition, code, message, hint string
	}{
		{"method", "USING missing(n)", "42704", `access method "missing" does not exist`, ""},
		{"include-before-multi", "USING hash(n,s) INCLUDE(n)", "0A000", `access method "hash" does not support included columns`, ""},
		{"multi", "USING hash(n,s)", "0A000", `access method "hash" does not support multicolumn indexes`, ""},
		{"asc", "USING hash(n ASC)", "0A000", `access method "hash" does not support ASC/DESC options`, ""},
		{"desc", "USING hash(n DESC)", "0A000", `access method "hash" does not support ASC/DESC options`, ""},
		{"first", "USING hash(n NULLS FIRST)", "0A000", `access method "hash" does not support NULLS FIRST/LAST options`, ""},
		{"last", "USING hash(n NULLS LAST)", "0A000", `access method "hash" does not support NULLS FIRST/LAST options`, ""},
		{"missing-class", "USING hash(n missing_ops)", "42704", `operator class "missing_ops" does not exist for access method "hash"`, ""},
		{"qualified-missing", "USING hash(n public.missing_ops)", "42704", `operator class "public.missing_ops" does not exist for access method "hash"`, ""},
		{"missing-namespace", "USING hash(n absent_hash_schema.int4_ops)", "3F000", `schema "absent_hash_schema" does not exist`, ""},
		{"unicode", `USING hash(n "不存在")`, "42704", `operator class "不存在" does not exist for access method "hash"`, ""},
		{"incompatible", "USING hash(n text_ops)", "42804", `operator class "text_ops" does not accept data type integer`, ""},
		{"qualified-incompatible", "USING hash(n pg_catalog.text_ops)", "42804", `operator class "pg_catalog.text_ops" does not accept data type integer`, ""},
		{"widening-not-binary", "USING hash(n int8_ops)", "42804", `operator class "int8_ops" does not accept data type integer`, ""},
		{"name-not-text", "USING hash(nm text_ops)", "42804", `operator class "text_ops" does not accept data type name`, ""},
		{"bpchar-not-text", "USING hash(c text_ops)", "42804", `operator class "text_ops" does not accept data type character`, ""},
		{"json-default", "USING hash(j)", "42704", `data type json has no default operator class for access method "hash"`, "You must specify an operator class for the index or define a default operator class for the data type."},
		{"json-explicit", "USING hash(j text_ops)", "42804", `operator class "text_ops" does not accept data type json`, ""},
		{"parameters", "USING hash(n int4_ops(foo=1))", "22023", "operator class int4_ops has no options", ""},
		{"class-before-direction", "USING hash(n missing_ops DESC)", "42704", `operator class "missing_ops" does not exist for access method "hash"`, ""},
		{"type-before-direction", "USING hash(n text_ops DESC)", "42804", `operator class "text_ops" does not accept data type integer`, ""},
		{"class-before-nulls", "USING hash(n missing_ops NULLS FIRST)", "42704", `operator class "missing_ops" does not exist for access method "hash"`, ""},
		{"missing-collation", "USING hash(s COLLATE missing)", "42704", `collation "missing" for encoding "UTF8" does not exist`, ""},
		{"type-before-class", `USING hash(n COLLATE "C" missing_ops)`, "42804", "collations are not supported by type integer", ""},
		{"system-xmin", "USING hash(xmin)", "0A000", "index creation on system columns is not supported", ""},
		{"system-cmin", "USING hash(cmin)", "0A000", "index creation on system columns is not supported", ""},
		{"system-tableoid", "USING hash(tableoid)", "0A000", "index creation on system columns is not supported", ""},
		{"system-ctid", "USING hash(ctid)", "0A000", "index creation on system columns is not supported", ""},
		{"missing-key", "USING hash(missing)", "42703", `column "missing" does not exist`, ""},
		{"unknown-before-options", "USING missing(n) WITH(fillfactor=9)", "42704", `access method "missing" does not exist`, ""},
		{"multi-before-options", "USING hash(n,s) WITH(fillfactor=9)", "0A000", `access method "hash" does not support multicolumn indexes`, ""},
		{"include-before-options", "USING hash(n) INCLUDE(s) WITH(fillfactor=9)", "0A000", `access method "hash" does not support included columns`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE t(n INT,s TEXT,c CHAR(4),nm NAME,j JSON)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,'one','one','one','{}')", 1))
				want := fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint}
				hashIndexSemanticFailure(t, p, b, run, "CREATE INDEX retry ON t "+tc.definition, want)
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
				run(fixture.Select("SELECT n,s FROM t", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
				run(fixture.Exec("CREATE INDEX retry ON t USING hash(n)", 0))
			})
		})
	}
	for _, tail := range []string{"", " INCLUDE(s) WITH(fillfactor=9)"} {
		t.Run("unique"+tail, func(t *testing.T) {
			hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE t(n INT,s TEXT)", 0))
				hashIndexSemanticFailure(t, p, b, run, "CREATE UNIQUE INDEX retry ON t USING hash(n)"+tail, fixture.Diagnostic{Code: "0A000", Message: `access method "hash" does not support unique indexes`})
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
			})
		})
	}
	t.Run("collision-precedence", func(t *testing.T) {
		hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE t(n INT)", 0))
			run(fixture.Exec("CREATE INDEX existing ON t USING hash(n)", 0))
			hashIndexSemanticFailure(t, p, b, run, "CREATE INDEX existing ON t USING hash(n int4_ops(foo=1))", fixture.Diagnostic{Code: "42P07", Message: `relation "existing" already exists`})
			hashIndexSemanticFailure(t, p, b, run, "CREATE INDEX IF NOT EXISTS existing ON t USING hash(n missing_ops)", fixture.Diagnostic{Code: "42704", Message: `operator class "missing_ops" does not exist for access method "hash"`})
			var notices []fixture.Diagnostic
			db, _, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("CREATE INDEX IF NOT EXISTS existing ON t USING hash(n int4_ops(foo=1))", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, db, recorder, s)
				want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "existing" already exists, skipping`}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("notices got %#v want %#v", notices, want)
				}
				notices = nil
			}
		})
	})
	t.Run("array-boundary", func(t *testing.T) {
		hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE t(a INT[])", 0))
			run(fixture.Exec("INSERT INTO t VALUES('{1,NULL}'),(NULL)", 2))
			for _, key := range []string{"a", "a array_ops"} {
				q := "CREATE INDEX boundary ON t USING hash(" + key + ")"
				for _, prepared := range []bool{false, true} {
					s := fixture.Exec(q, 0)
					if !b.PG {
						s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "index operator class is unsupported"})
						s.ErrorStage = "execute"
					}
					s.Prepared = prepared
					run(s)
					if b.PG {
						run(fixture.Exec("DROP INDEX boundary", 0))
					} else {
						run(fixture.Select("SELECT relname FROM pg_class WHERE relname='boundary'", []string{"relname"}, []string{"NAME"}, nil))
					}
				}
			}
			run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"_INT4"}, [][]any{{"{1,NULL}"}, {nil}}))
		})
	})
}

func TestReferenceHashIndexOptions(t *testing.T) {
	for _, tc := range []struct{ options, code, message, detail string }{
		{"fillfactor=9", "22023", `value 9 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`},
		{"fillfactor=101", "22023", `value 101 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`},
		{"fillfactor=-1", "22023", `value -1 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`},
		{"fillfactor='bad'", "22023", `invalid value for integer option "fillfactor": bad`, ""},
		{"fillfactor", "22023", `invalid value for integer option "fillfactor": true`, ""},
		{"fillfactor=80,fillfactor=90", "22023", `parameter "fillfactor" specified more than once`, ""},
		{"fillfactor='bad',fillfactor=90", "22023", `invalid value for integer option "fillfactor": bad`, ""},
		{"deduplicate_items=on", "22023", `unrecognized parameter "deduplicate_items"`, ""},
		{"fastupdate=on", "22023", `unrecognized parameter "fastupdate"`, ""},
		{"unknown=1", "22023", `unrecognized parameter "unknown"`, ""},
		{"fillfactor=80,unknown=1", "22023", `unrecognized parameter "unknown"`, ""},
		{"toast.fillfactor=80", "22023", `unrecognized parameter namespace "toast"`, ""},
		{`"a=b"=1`, "22023", `invalid option name "a=b": must not contain "="`, ""},
	} {
		t.Run(tc.options, func(t *testing.T) {
			hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE t(n INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(2),(2),(NULL)", 3))
				want := fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail}
				hashIndexSemanticFailure(t, p, b, run, "CREATE INDEX retry ON t USING hash(n) WITH("+tc.options+")", want)
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
				run(fixture.Exec("CREATE INDEX retry ON t USING hash(n)", 0))
				run(fixture.Select("SELECT n FROM t WHERE n=2", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}}))
			})
		})
	}
	for _, value := range []string{"10", "75", "100", "80.0"} {
		t.Run("valid-physical-"+value, func(t *testing.T) {
			hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE t(n INT)", 0))
				q := "CREATE INDEX retry ON t USING hash(n) WITH(fillfactor=" + value + ")"
				if !b.PG {
					hashIndexSemanticFailure(t, p, b, run, q, fixture.Diagnostic{Code: "0A000", Message: "index storage parameters are unsupported", Position: "39"})
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
				} else {
					for _, prepared := range []bool{false, true} {
						s := fixture.Exec(q, 0)
						s.Prepared = prepared
						run(s)
						run(fixture.Exec("DROP INDEX retry", 0))
					}
				}
				run(fixture.Exec("CREATE INDEX retry ON t USING hash(n)", 0))
			})
		})
	}
	t.Run("option-before-class-direction", func(t *testing.T) {
		hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE t(n INT)", 0))
			hashIndexSemanticFailure(t, p, b, run, "CREATE INDEX retry ON t USING hash(n missing_ops DESC) WITH(fillfactor=9)", fixture.Diagnostic{Code: "22023", Message: `value 9 out of bounds for option "fillfactor"`, Detail: `Valid values are between "10" and "100".`})
		})
	})
	// A valid physical request is rejected only after PostgreSQL's remaining
	// semantic/collision/parameter checks; its WITH exclusion must not mask them.
	for _, tc := range []struct{ name, key, code, message string }{
		{"valid-option-bad-key", "missing", "42703", `column "missing" does not exist`},
		{"valid-option-bad-class", "n missing_ops", "42704", `operator class "missing_ops" does not exist for access method "hash"`},
		{"valid-option-bad-type", "n text_ops", "42804", `operator class "text_ops" does not accept data type integer`},
		{"valid-option-direction", "n ASC", "0A000", `access method "hash" does not support ASC/DESC options`},
		{"valid-option-nulls", "n NULLS LAST", "0A000", `access method "hash" does not support NULLS FIRST/LAST options`},
		{"valid-option-class-params", "n int4_ops(foo=1)", "22023", "operator class int4_ops has no options"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE t(n INT)", 0))
				hashIndexSemanticFailure(t, p, b, run, "CREATE INDEX retry ON t USING hash("+tc.key+") WITH(fillfactor=75)", fixture.Diagnostic{Code: tc.code, Message: tc.message})
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
				run(fixture.Exec("CREATE INDEX retry ON t USING hash(n)", 0))
			})
		})
	}
	t.Run("valid-option-collision-before-physical", func(t *testing.T) {
		hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE t(n INT)", 0))
			run(fixture.Exec("CREATE INDEX existing ON t USING hash(n)", 0))
			hashIndexSemanticFailure(t, p, b, run, "CREATE INDEX existing ON t USING hash(n int4_ops(foo=1)) WITH(fillfactor=75)", fixture.Diagnostic{Code: "42P07", Message: `relation "existing" already exists`})
			var notices []fixture.Diagnostic
			db, _, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("CREATE INDEX IF NOT EXISTS existing ON t USING hash(n int4_ops(foo=1)) WITH(fillfactor=75)", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, db, recorder, s)
				want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "existing" already exists, skipping`}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("notices got %#v want %#v", notices, want)
				}
				notices = nil
			}
			run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('existing'::regclass) AS definition", "CREATE INDEX existing ON public.t USING hash (n)"))
		})
	})
	t.Run("alter-method-options", func(t *testing.T) {
		hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE t(n INT)", 0))
			run(fixture.Exec("CREATE INDEX ix ON t USING hash(n)", 0))
			hashIndexSemanticFailure(t, p, b, run, "ALTER INDEX ix SET(deduplicate_items=on)", fixture.Diagnostic{Code: "22023", Message: `unrecognized parameter "deduplicate_items"`})
			hashIndexSemanticFailure(t, p, b, run, "ALTER INDEX ix SET(fillfactor=9)", fixture.Diagnostic{Code: "22023", Message: `value 9 out of bounds for option "fillfactor"`, Detail: `Valid values are between "10" and "100".`})
			hashIndexSemanticFailure(t, p, b, run, "ALTER INDEX ix RESET(fillfactor=80)", fixture.Diagnostic{Code: "42601", Message: "RESET must not include values for parameters"})
			for _, q := range []string{"ALTER INDEX ix SET(fillfactor=80)", "ALTER INDEX ix RESET(fillfactor)", "ALTER INDEX ix SET(toast.fillfactor=80)", "ALTER INDEX ix RESET(toast.fillfactor)", "ALTER INDEX ix RESET(deduplicate_items)", "ALTER INDEX ix RESET(unknown)", "ALTER INDEX ix RESET(fillfactor,fillfactor)"} {
				if b.PG {
					run(fixture.Exec(q, 0))
					run(fixture.Prepared(fixture.Exec(q, 0)))
				} else {
					hashIndexSemanticFailure(t, p, b, run, q, fixture.Diagnostic{Code: "0A000", Message: "index storage parameters are unsupported"})
				}
			}
			run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('ix'::regclass) AS definition", "CREATE INDEX ix ON public.t USING hash (n)"))
		})
	})
}

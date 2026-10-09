package reference_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
)

func TestReferenceIndexClausesDiagnostics(t *testing.T) {
	cases := []struct{ name, elem, tail, method, code, message, hint string }{
		{name: "missing-key", elem: "missing missing_ops", code: "42703", message: `column "missing" does not exist`},
		{name: "missing-include", elem: "n", tail: " INCLUDE(missing DESC)", code: "42703", message: `column "missing" does not exist`},
		{name: "method", elem: "n", method: "missing", code: "42704", message: `access method "missing" does not exist`},
		{name: "hash-include", elem: "n,s", tail: " INCLUDE(n)", method: "hash", code: "0A000", message: `access method "hash" does not support included columns`},
		{name: "hash-multi", elem: "n,s", method: "hash", code: "0A000", message: `access method "hash" does not support multicolumn indexes`},
		{name: "hash-asc", elem: "n ASC", method: "hash", code: "0A000", message: `access method "hash" does not support ASC/DESC options`},
		{name: "hash-desc", elem: "n DESC", method: "hash", code: "0A000", message: `access method "hash" does not support ASC/DESC options`},
		{name: "hash-nulls", elem: "n NULLS LAST", method: "hash", code: "0A000", message: `access method "hash" does not support NULLS FIRST/LAST options`},
		{name: "parenthesized-include", elem: "n", tail: " INCLUDE((n))", code: "0A000", message: "expressions are not supported in included columns"},
		{name: "unbound-expression", elem: "n", tail: " INCLUDE((missing+1))", code: "0A000", message: "expressions are not supported in included columns"},
		{name: "function-include", elem: "n", tail: " INCLUDE(lower(s))", code: "0A000", message: "expressions are not supported in included columns"},
		{name: "qualified-function-include", elem: "n", tail: " INCLUDE(pg_catalog.lower(s))", code: "0A000", message: "expressions are not supported in included columns"},
		{name: "quoted-qualified-function-include", elem: "n", tail: ` INCLUDE("pg_catalog"."lower"(s))`, code: "0A000", message: "expressions are not supported in included columns"},
		{name: "coalesce-special-include", elem: "n", tail: " INCLUDE(coalesce(missing,missing))", code: "0A000", message: "expressions are not supported in included columns"},
		{name: "nullif-special-include", elem: "n", tail: " INCLUDE(nullif(missing,missing))", code: "0A000", message: "expressions are not supported in included columns"},
		{name: "greatest-special-include", elem: "n", tail: " INCLUDE(greatest(missing,missing))", code: "0A000", message: "expressions are not supported in included columns"},
		{name: "least-special-include", elem: "n", tail: " INCLUDE(least(missing,missing))", code: "0A000", message: "expressions are not supported in included columns"},
		{name: "include-collation", elem: "n", tail: " INCLUDE(s COLLATE missing)", code: "42P17", message: "including column does not support a collation"},
		{name: "include-class", elem: "n", tail: " INCLUDE(n missing_ops)", code: "42P17", message: "including column does not support an operator class"},
		{name: "include-asc", elem: "n", tail: " INCLUDE(n ASC)", code: "42P17", message: "including column does not support ASC/DESC options"},
		{name: "include-desc", elem: "n", tail: " INCLUDE(n DESC)", code: "42P17", message: "including column does not support ASC/DESC options"},
		{name: "include-first", elem: "n", tail: " INCLUDE(n NULLS FIRST)", code: "42P17", message: "including column does not support NULLS FIRST/LAST options"},
		{name: "include-last", elem: "n", tail: " INCLUDE(n NULLS LAST)", code: "42P17", message: "including column does not support NULLS FIRST/LAST options"},
		{name: "missing-text-collation", elem: "s COLLATE missing", code: "42704", message: `collation "missing" for encoding "UTF8" does not exist`},
		{name: "lookup-before-type", elem: "n COLLATE missing", code: "42704", message: `collation "missing" for encoding "UTF8" does not exist`},
		{name: "type-before-class", elem: `n COLLATE "C" missing_ops`, code: "42804", message: "collations are not supported by type integer"},
		{name: "missing-class", elem: "n missing_ops", code: "42704", message: `operator class "missing_ops" does not exist for access method "btree"`},
		{name: "incompatible-class", elem: "n text_ops", code: "42804", message: `operator class "text_ops" does not accept data type integer`},
		{name: "qualified-class", elem: "n pg_catalog.text_ops", code: "42804", message: `operator class "pg_catalog.text_ops" does not accept data type integer`},
		{name: "widening-not-binary", elem: "n int8_ops", code: "42804", message: `operator class "int8_ops" does not accept data type integer`},
		{name: "bpchar-not-text", elem: "c text_ops", code: "42804", message: `operator class "text_ops" does not accept data type character`},
		{name: "name-not-text", elem: "nm text_ops", code: "42804", message: `operator class "text_ops" does not accept data type name`},
		{name: "parameters", elem: "n int4_ops(foo=1)", code: "22023", message: "operator class int4_ops has no options"},
		{name: "parameters-negative", elem: "n int4_ops(foo=-1)", code: "22023", message: "operator class int4_ops has no options"},
		{name: "parameters-positive", elem: "n int4_ops(foo=+1)", code: "22023", message: "operator class int4_ops has no options"},
		{name: "parameters-reserved", elem: "n int4_ops(ASC=1)", code: "22023", message: "operator class int4_ops has no options"},
		{name: "system-key", elem: "ctid", code: "0A000", message: "index creation on system columns is not supported"},
		{name: "system-include", elem: "n", tail: " INCLUDE(ctid)", code: "0A000", message: "index creation on system columns is not supported"},
		{name: "system-collation-missing", elem: "ctid COLLATE missing", code: "42704", message: `collation "missing" for encoding "UTF8" does not exist`},
		{name: "system-collation-type", elem: `ctid COLLATE "C"`, code: "42804", message: "collations are not supported by type tid"},
		{name: "system-class-type", elem: "ctid int4_ops", code: "42804", message: `operator class "int4_ops" does not accept data type tid`},
		{name: "system-class-missing", elem: "ctid missing_ops", code: "42704", message: `operator class "missing_ops" does not exist for access method "btree"`},
		{name: "system-before-later-missing", elem: "ctid,missing", code: "42703", message: `column "missing" does not exist`},
		{name: "system-include-desc", elem: "n", tail: " INCLUDE(ctid DESC)", code: "42P17", message: "including column does not support ASC/DESC options"},
		{name: "system-xmin-default", elem: "xmin", code: "42704", message: `data type xid has no default operator class for access method "btree"`, hint: "You must specify an operator class for the index or define a default operator class for the data type."},
		{name: "system-cmin-default", elem: "cmin", code: "42704", message: `data type cid has no default operator class for access method "btree"`, hint: "You must specify an operator class for the index or define a default operator class for the data type."},
		{name: "key-before-include", elem: "missing", tail: " INCLUDE((missing+1))", code: "42703", message: `column "missing" does not exist`},
		{name: "wrong-schema-class", elem: "n public.int4_ops", code: "42704", message: `operator class "public.int4_ops" does not exist for access method "btree"`},
		{name: "wrong-schema-collation", elem: `s COLLATE public."C"`, code: "42704", message: `collation "public.C" for encoding "UTF8" does not exist`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			indexClausesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
				run(referenceExec("CREATE TABLE t(n INT,s TEXT,c CHAR(4),nm NAME)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,'one','one','one')", 1))
				run(referenceExec("CREATE INDEX existing ON t(n)", 0))
				var before bytes.Buffer
				if !b.pg {
					if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
						t.Fatal(err)
					}
				}
				method := ""
				if tc.method != "" {
					method = " USING " + tc.method
				}
				for _, prefix := range []string{"CREATE INDEX retry", "CREATE INDEX IF NOT EXISTS retry", "CREATE INDEX existing", "CREATE INDEX IF NOT EXISTS existing"} {
					q := prefix + " ON t" + method + "(" + tc.elem + ")" + tc.tail
					want := controlsReferenceDiagnostic(tc.code, tc.message)
					want.Hint = tc.hint
					if tc.code == "22023" && prefix == "CREATE INDEX existing" {
						want = controlsReferenceDiagnostic("42P07", `relation "existing" already exists`)
					}
					if tc.code == "22023" && prefix == "CREATE INDEX IF NOT EXISTS existing" {
						var notices []referenceDiagnostic
						db, _, recorder := noticeReferencePool(t, p, b.pg, &notices)
						for _, prepared := range []bool{false, true} {
							s := referenceExec(q, 0)
							s.prepared = prepared
							referenceRunStep(t, p.ctx, db, recorder, s)
							expected := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "existing" already exists, skipping`}}
							if !reflect.DeepEqual(notices, expected) {
								t.Fatalf("notice got %#v want %#v", notices, expected)
							}
							notices = nil
						}
						continue
					}
					s := referenceErrorDiagnostic(q, want)
					s.errorStage = "execute"
					run(s)
					run(referencePrepared(s))
					stmt, err := b.db.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal("semantic Prepare", err)
					}
					defer stmt.Close()
					for range 2 {
						result, err := stmt.ExecContext(p.ctx)
						if result != nil {
							t.Fatal("published error result")
						}
						referenceAssertDiagnostic(t, err, &want)
					}
					stmt.Close()
				}
				if !b.pg {
					var after bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || before.String() != after.String() {
						t.Fatal("failed definition mutated dump", err)
					}
				}
				run(referenceSelect("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
				run(referenceSelect("SELECT n,s FROM t", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
				run(referenceExec("CREATE INDEX retry ON t(n) INCLUDE(s)", 0))
			})
		})
	}
	for _, tc := range []struct{ q, token string }{
		{"CREATE INDEX retry ON t()", ")"},
		{"CREATE INDEX retry ON t(n) INCLUDE()", ")"},
		{"CREATE INDEX retry ON t(n) INCLUDE(s) INCLUDE(n)", "INCLUDE"},
		{"CREATE INDEX retry ON t(n DESC COLLATE \"C\")", "COLLATE"},
		{"CREATE INDEX retry ON t(n NULLS MIDDLE)", "MIDDLE"},
		{"CREATE INDEX retry ON t(n ASC DESC)", "DESC"},
		{"/*é😀*/ CREATE INDEX retry ON t(n) INCLUDE(s) junk", "junk"},
		{"CREATE INDEX retry ON t(n int4_ops(foo=))", ")"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			indexClausesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
				run(referenceExec("CREATE TABLE t(n INT,s TEXT)", 0))
				pos := utf8.RuneCountInString(tc.q[:strings.LastIndex(tc.q, tc.token)]) + 1
				if strings.HasSuffix(tc.q, "foo=))") {
					pos--
				}
				s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42601", Message: `syntax error at or near "` + tc.token + `"`, Position: referencePosition(pos)})
				s.errorStage = "execute"
				run(s)
				s = referencePrepared(s)
				s.errorStage = "prepare"
				run(s)
				run(referenceSelect("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
			})
		})
	}
	for _, tc := range []struct {
		name, create, seed, index, alter, query string
		diagnostic                              referenceDiagnostic
		columns, types                          []string
		rows                                    [][]any
	}{
		{
			name:       "derived-retained-class",
			create:     "CREATE TABLE t(n INT,p TEXT)",
			seed:       "INSERT INTO t VALUES(-1,'space'),(1,'plain')",
			index:      "CREATE INDEX ix ON t((coalesce(n,n)) oid_ops) INCLUDE(p)",
			alter:      "ALTER TABLE t ALTER n TYPE TEXT USING n::text",
			query:      "SELECT n,p FROM t ORDER BY n",
			diagnostic: referenceDiagnostic{Code: "42804", Message: `operator class "oid_ops" does not accept data type text`},
			columns:    []string{"n", "p"}, types: []string{"INT4", "TEXT"},
			rows: [][]any{{int64(-1), "space"}, {int64(1), "plain"}},
		},
		{
			name:       "decorated-type-position",
			create:     "CREATE TABLE t(c INT,v TEXT,p TEXT)",
			seed:       "INSERT INTO t VALUES(1,'Alpha','payload')",
			index:      `CREATE INDEX ix ON t(lower(v) COLLATE "POSIX" text_pattern_ops DESC NULLS LAST,(c+1)) INCLUDE(p)`,
			alter:      "ALTER TABLE t ALTER c TYPE TEXT USING c::text",
			query:      "SELECT c,v,p FROM t",
			diagnostic: referenceDiagnostic{Code: "42883", Message: "operator does not exist: text + integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: referencePosition(105)},
			columns:    []string{"c", "v", "p"}, types: []string{"INT4", "TEXT", "TEXT"},
			rows: [][]any{{int64(1), "Alpha", "payload"}},
		},
		{
			name:       "special-type-position-coalesce",
			create:     "CREATE TABLE t(n INT,c INT,p TEXT)",
			seed:       "INSERT INTO t VALUES(1,1,'payload')",
			index:      "CREATE INDEX ix ON t((coalesce(n,n)) oid_ops DESC NULLS LAST,(c+1)) INCLUDE(p)",
			alter:      "ALTER TABLE t ALTER c TYPE TEXT USING c::text",
			query:      "SELECT n,c,p FROM t",
			diagnostic: referenceDiagnostic{Code: "42883", Message: "operator does not exist: text + integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: referencePosition(86)},
			columns:    []string{"n", "c", "p"}, types: []string{"INT4", "INT4", "TEXT"},
			rows: [][]any{{int64(1), int64(1), "payload"}},
		},
		{
			name:       "special-type-position-nullif",
			create:     "CREATE TABLE t(n INT,c INT,p TEXT)",
			seed:       "INSERT INTO t VALUES(1,1,'payload')",
			index:      "CREATE INDEX ix ON t((nullif(n,0)) oid_ops DESC NULLS LAST,(c+1)) INCLUDE(p)",
			alter:      "ALTER TABLE t ALTER c TYPE TEXT USING c::text",
			query:      "SELECT n,c,p FROM t",
			diagnostic: referenceDiagnostic{Code: "42883", Message: "operator does not exist: text + integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: referencePosition(84)},
			columns:    []string{"n", "c", "p"}, types: []string{"INT4", "INT4", "TEXT"},
			rows: [][]any{{int64(1), int64(1), "payload"}},
		},
		{
			name:       "special-type-position-greatest",
			create:     "CREATE TABLE t(n INT,c INT,p TEXT)",
			seed:       "INSERT INTO t VALUES(1,1,'payload')",
			index:      "CREATE INDEX ix ON t((greatest(n,0)) oid_ops DESC NULLS LAST,(c+1)) INCLUDE(p)",
			alter:      "ALTER TABLE t ALTER c TYPE TEXT USING c::text",
			query:      "SELECT n,c,p FROM t",
			diagnostic: referenceDiagnostic{Code: "42883", Message: "operator does not exist: text + integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: referencePosition(86)},
			columns:    []string{"n", "c", "p"}, types: []string{"INT4", "INT4", "TEXT"},
			rows: [][]any{{int64(1), int64(1), "payload"}},
		},
		{
			name:       "special-type-position-least",
			create:     "CREATE TABLE t(n INT,c INT,p TEXT)",
			seed:       "INSERT INTO t VALUES(1,1,'payload')",
			index:      "CREATE INDEX ix ON t((least(n,0)) oid_ops DESC NULLS LAST,(c+1)) INCLUDE(p)",
			alter:      "ALTER TABLE t ALTER c TYPE TEXT USING c::text",
			query:      "SELECT n,c,p FROM t",
			diagnostic: referenceDiagnostic{Code: "42883", Message: "operator does not exist: text + integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: referencePosition(83)},
			columns:    []string{"n", "c", "p"}, types: []string{"INT4", "INT4", "TEXT"},
			rows: [][]any{{int64(1), int64(1), "payload"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			indexClausesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
				run(referenceExec(tc.create, 0))
				run(referenceExec(tc.seed, int64(len(tc.rows))))
				run(referenceExec(tc.index, 0))
				inquiry := func() {
					if tc.name != "derived-retained-class" {
						return
					}
					s := deparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('ix'::regclass) AS definition", "CREATE INDEX ix ON public.t USING btree (COALESCE(n, n) oid_ops) INCLUDE (p)")
					run(s)
					run(referencePrepared(s))
				}
				inquiry()
				var before bytes.Buffer
				if !b.pg {
					if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
						t.Fatal(err)
					}
				}
				s := referenceImplicitError(tc.alter, tc.diagnostic)
				s.errorStage = "execute"
				run(s)
				run(referencePrepared(s))
				held, err := b.db.PrepareContext(p.ctx, tc.alter)
				if err != nil {
					t.Fatal("semantic TYPE failed at Prepare", err)
				}
				defer held.Close()
				result, err := held.ExecContext(p.ctx)
				if result != nil {
					t.Fatal("failed held TYPE published result")
				}
				want := controlsReferenceDiagnostic(tc.diagnostic.Code, tc.diagnostic.Message)
				want.Hint, want.Position = tc.diagnostic.Hint, tc.diagnostic.Position
				referenceAssertDiagnostic(t, err, &want)
				run(referenceSelect(tc.query, tc.columns, tc.types, tc.rows))
				run(referencePrepared(referenceSelect(tc.query, tc.columns, tc.types, tc.rows)))
				if b.pg {
					class := "oid_ops"
					if tc.name == "decorated-type-position" {
						class = "text_pattern_ops"
					}
					run(referenceSelect("SELECT o.opcname FROM pg_index i JOIN pg_class c ON i.indexrelid=c.oid JOIN pg_opclass o ON i.indclass[0]=o.oid WHERE c.relname='ix'", []string{"opcname"}, []string{"NAME"}, [][]any{{class}}))
				} else {
					var after bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || after.String() != before.String() {
						t.Fatal("failed derived TYPE mutated definition/data", err)
					}
				}
				inquiry()
			})
		})
	}
}

func TestReferenceIndexClausesUnownedBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, q, message string }{
		{"array-default", "CREATE INDEX boundary ON t(a)", "index operator class is unsupported"},
		{"array-explicit", "CREATE INDEX boundary ON t(a array_ops)", "index operator class is unsupported"},
		{"ucs-basic", `CREATE INDEX boundary ON t(s COLLATE ucs_basic)`, "collation is unsupported"},
		{"hash", "CREATE INDEX boundary ON t USING hash(n)", "non-btree indexes are unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			indexClausesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
				run(referenceExec("CREATE DOMAIN payload AS name[]", 0))
				run(referenceExec("CREATE TABLE t(n INT,s TEXT,a payload)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,'one','{alpha,NULL}')", 1))
				for _, prepared := range []bool{false, true} {
					s := referenceExec(tc.q, 0)
					if !b.pg {
						s = referenceImplicitError(tc.q, referenceDiagnostic{Code: "0A000", Message: tc.message})
						s.errorStage = "execute"
					}
					if prepared {
						s = referencePrepared(s)
					}
					run(s)
					if b.pg {
						run(referenceSelect("SELECT relkind FROM pg_class WHERE relname='boundary'", []string{"relkind"}, []string{"CHAR"}, [][]any{{"i"}}))
						run(referenceExec("DROP INDEX boundary", 0))
					} else {
						run(referenceSelect("SELECT relname FROM pg_class WHERE relname='boundary'", []string{"relname"}, []string{"NAME"}, nil))
					}
				}
				run(referenceExec("CREATE INDEX boundary ON t(n) INCLUDE(a)", 0))
				run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"_NAME"}, [][]any{{"{alpha,NULL}"}}))
			})
		})
	}
}

package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringVariadicConsumers(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec(`CREATE TABLE sv_consumer(id integer,v text)`, 0),
			fixture.Exec(`INSERT INTO sv_consumer VALUES(1,'a'),(2,'a'),(3,'b')`, 3),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, s := range []fixture.Step{
			fixture.Select(`SELECT concat(v,'!') AS value,count(*) AS n FROM sv_consumer GROUP BY concat(v,'!') HAVING concat(concat(v,'!'),'ok')<>'bad' ORDER BY value`, []string{"value", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a!", int64(2)}, {"b!", int64(1)}}),
			fixture.Select(`SELECT min(quote_ident(v)) AS value FROM sv_consumer WHERE concat(v,'!')='a!'`, []string{"value"}, []string{"TEXT"}, [][]any{{"a"}}),
			fixture.Select(`SELECT concat_ws(':',l.v,r.v) AS value FROM sv_consumer l JOIN sv_consumer r ON concat(l.id)=concat(r.id) WHERE l.id=3`, []string{"value"}, []string{"TEXT"}, [][]any{{"b:b"}}),
		} {
			formatReferenceBoth(t, p, b, c, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Select(`UPDATE sv_consumer SET v=concat(v,'x') WHERE id=1 RETURNING quote_nullable(v) AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{`'ax'`}})))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`DELETE FROM sv_consumer WHERE concat(id)='2' RETURNING concat_ws('|',v,id) AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{"a|2"}}))
		formatReferenceBoth(t, p, b, c, fixture.Select(`SELECT concat(id,v) AS value FROM sv_consumer ORDER BY id`, []string{"value"}, []string{"TEXT"}, [][]any{{"1ax"}, {"3b"}}))
	})
}

func stringVariadicStoredSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec(`CREATE TABLE sv_stored(id integer,v text DEFAULT concat('d',1),a integer[] DEFAULT '{1,NULL,2}',CONSTRAINT sv_stored_valid CHECK(quote_nullable(v)<>'NULL'))`, 0),
		fixture.Exec(`INSERT INTO sv_stored(id) VALUES(1)`, 1),
		fixture.Prepared(fixture.Exec(`INSERT INTO sv_stored VALUES(2,concat('x',true),'{3,4}')`, 1)),
		fixture.Exec(`CREATE VIEW sv_stored_view AS SELECT id,quote_literal(v) AS quoted,concat_ws('|',VARIADIC a) AS joined FROM sv_stored`, 0),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
	return fixture.Select(`SELECT id,quoted,joined FROM sv_stored_view ORDER BY id`, []string{"id", "quoted", "joined"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), `'d1'`, "1|2"}, {int64(2), `'xt'`, "3|4"}})
}

func TestReferenceStringVariadicStored(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		want := stringVariadicStoredSetup(t, p, b, c)
		stmt, err := c.PrepareContext(p.Ctx, want.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: want.Query}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`ALTER TABLE sv_stored RENAME COLUMN v TO renamed`, 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		formatReferenceBoth(t, p, b, c, want)
		stringVariadicImmutableStored(t, p, b, c)
		for _, q := range []string{
			`CREATE TABLE sv_gen_concat(v text,q text GENERATED ALWAYS AS (concat(v)) STORED)`,
			`CREATE TABLE sv_gen_encoding(v text,q name GENERATED ALWAYS AS (pg_client_encoding()) STORED)`,
			`CREATE INDEX sv_idx_concat ON sv_stored ((concat(id)))`,
		} {
			stringVariadicContract(t, p, b, c, q)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE sv_cast_volatility(d date,z timestamptz)`, 0))
		for _, tc := range []struct{ typ, column string }{{"date", "d"}, {"timestamptz", "z"}} {
			for _, fn := range []string{"quote_literal", "quote_nullable"} {
				formatReferenceFailure(t, p, b, c, "CREATE TABLE sv_gen_"+tc.typ+"_"+fn+"(v "+tc.typ+",q text GENERATED ALWAYS AS ("+fn+"(v)) STORED)", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P17", Message: "generation expression is not immutable"}, false)
				formatReferenceFailure(t, p, b, c, "CREATE INDEX sv_idx_"+tc.typ+"_"+fn+" ON sv_cast_volatility (("+fn+"("+tc.column+")))", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P17", Message: "functions in index expression must be marked IMMUTABLE"}, false)
			}
		}
	})
}

// Successful DDL must support real stored values, expression-index consumers,
// and subsequent writes. Each route owns a separate persistent table.
func stringVariadicImmutableStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET bytea_output=hex`, 0))
	for _, tc := range []struct{ typ, input, updated, quoted, next string }{
		{"text", `'O''Reilly'`, `'changed'`, `'O''Reilly'`, `'changed'`},
		{"integer", "42", "43", `'42'`, `'43'`},
		{"bytea", `'\x00015c'::bytea`, `'\x01'::bytea`, `E'\\x00015c'`, `E'\\x01'`},
	} {
		for _, fn := range []string{"quote_literal", "quote_nullable", "quote_ident"} {
			if fn == "quote_ident" && tc.typ != "text" {
				continue
			}
			for route, prepared := range []bool{false, true} {
				table := fmt.Sprintf("sv_immutable_%s_%s_%d", tc.typ, fn, route)
				t.Run(table, func(t *testing.T) {
					run := func(s fixture.Step) {
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
					run(fixture.Exec("CREATE TABLE "+table+"(id integer,v "+tc.typ+",q text GENERATED ALWAYS AS ("+fn+"(v)) STORED)", 0))
					defer fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE "+table, 0))
					run(fixture.Exec("INSERT INTO "+table+"(id,v) VALUES(1,"+tc.input+"),(2,NULL)", 2))
					run(fixture.Exec("CREATE INDEX "+table+"_idx ON "+table+" (("+fn+"(v)))", 0))
					quoted, next, null := tc.quoted, tc.next, any(nil)
					if fn == "quote_nullable" {
						null = "NULL"
					}
					if fn == "quote_ident" {
						quoted = `"O'Reilly"`
						next = "changed"
					}
					run(fixture.Select("SELECT id,q FROM "+table+" ORDER BY id", []string{"id", "q"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), quoted}, {int64(2), null}}))
					// The same expression as the stored index participates in an actual lookup.
					run(fixture.Select("SELECT q FROM "+table+" WHERE "+fn+"(v)=$1::text", []string{"q"}, []string{"TEXT"}, [][]any{{quoted}}, quoted))
					run(fixture.Exec("UPDATE "+table+" SET v="+tc.updated+" WHERE id=1", 1))
					run(fixture.Select("SELECT q FROM "+table+" WHERE "+fn+"(v)=$1::text", []string{"q"}, []string{"TEXT"}, [][]any{{next}}, next))
				})
			}
		}
	}
}

func TestReferenceStringVariadicEvaluation(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE SEQUENCE sv_eval_seq`, 0))
		q := `SELECT concat_ws(NULL,nextval('sv_eval_seq')) AS value`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT is_called FROM sv_eval_seq`, []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
		want := fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{nil}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, want)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, want)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT last_value,is_called FROM sv_eval_seq`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
		stringVariadicContract(t, p, b, c, `SELECT concat_ws(NULL,format('%I',NULL))`)
	})
}

func TestReferenceStringVariadicSettings(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET quote_all_identifiers=off`, 0))
		for _, s := range []fixture.Step{
			fixture.Exec(`CREATE TABLE sv_deparse(v text DEFAULT quote_ident('foo'),CONSTRAINT sv_deparse_check CHECK(quote_nullable(v)<>'NULL'))`, 0),
			fixture.Exec(`CREATE VIEW sv_deparse_view AS SELECT quote_ident(v) AS quoted FROM sv_deparse`, 0),
			fixture.Exec(`CREATE INDEX sv_deparse_idx ON sv_deparse ((quote_literal(v)))`, 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		literal := `SELECT quote_ident('foo'),format('%I','foo')`
		typed := `SELECT quote_ident($1::text),format('%I',$1::text)`
		ls, err := c.PrepareContext(p.Ctx, literal)
		if err != nil {
			t.Fatal(err)
		}
		defer ls.Close()
		ts, err := c.PrepareContext(p.Ctx, typed)
		if err != nil {
			t.Fatal(err)
		}
		defer ts.Close()
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		var canonicalDump string
		for i, setting := range []string{"off", "on", "off"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET quote_all_identifiers="+setting, 0))
			t.Run(fmt.Sprintf("setting_%d_%s", i, setting), func(t *testing.T) {
				quoted := "foo"
				if setting == "on" {
					quoted = `"foo"`
				}
				want := fixture.Select(literal, []string{"quote_ident", "format"}, []string{"TEXT", "TEXT"}, [][]any{{quoted, quoted}})
				formatReferenceBoth(t, p, b, c, want)
				formatReferenceBoth(t, p, b, c, fixture.Select(typed, want.Columns, want.Types, want.Rows, "foo"))
				formatReferenceBoth(t, p, b, peer, fixture.Select(literal, want.Columns, want.Types, [][]any{{"foo", "foo"}}))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: ls, SQL: literal}, b.Recorder, fixture.Select(literal, want.Columns, want.Types, [][]any{{"foo", quoted}}))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: ts, SQL: typed}, b.Recorder, fixture.Select(typed, want.Columns, want.Types, want.Rows, "foo"))
				for _, tc := range []struct{ q, off, on, label string }{
					{`SELECT pg_get_viewdef('sv_deparse_view'::regclass)`, " SELECT quote_ident(v) AS quoted\n   FROM sv_deparse;", " SELECT \"quote_ident\"(\"v\") AS \"quoted\"\n   FROM \"sv_deparse\";", "pg_get_viewdef"},
					{`SELECT pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE adrelid='sv_deparse'::regclass::oid`, `quote_ident('foo'::text)`, `"quote_ident"('foo'::"text")`, "pg_get_expr"},
					{`SELECT pg_get_indexdef('sv_deparse_idx'::regclass)`, `CREATE INDEX sv_deparse_idx ON public.sv_deparse USING btree (quote_literal(v))`, `CREATE INDEX "sv_deparse_idx" ON "public"."sv_deparse" USING "btree" ("quote_literal"("v"))`, "pg_get_indexdef"},
					{`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='sv_deparse_check'`, `CHECK ((quote_nullable(v) <> 'NULL'::text))`, `CHECK (("quote_nullable"("v") <> 'NULL'::"text"))`, "pg_get_constraintdef"},
				} {
					value := tc.off
					if setting == "on" {
						value = tc.on
					}
					formatReferenceBoth(t, p, b, c, fixture.Select(tc.q, []string{tc.label}, []string{"TEXT"}, [][]any{{value}}))
				}
				if !b.PG {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(dump.String(), `CREATE INDEX "sv_deparse_idx" ON "public"."sv_deparse" USING btree (`) {
						t.Fatal("canonical Dump changed access-method spelling", dump.String())
					}
					if i == 0 {
						canonicalDump = dump.String()
					} else if dump.String() != canonicalDump {
						t.Fatal("session quoting changed canonical Dump", dump.String())
					}
				}
			})
		}
		for _, s := range []string{"on", "off"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET standard_conforming_strings="+s, 0))
			formatReferenceBoth(t, p, b, c, fixture.Select(`SELECT quote_literal($1::text) AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{`E'a\\b'`}}, `a\b`))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`RESET standard_conforming_strings`, 0))
	})
}

func TestReferenceStringVariadicAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sv_reader"})
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec(`CREATE ROLE sv_reader`, 0), fixture.Exec(`CREATE TABLE sv_private(v text)`, 0), fixture.Exec(`INSERT INTO sv_private VALUES('secret')`, 1),
			fixture.Exec(`CREATE SEQUENCE sv_private_seq`, 0), fixture.Exec(`CREATE TABLE sv_auth_target(v text)`, 0), fixture.Exec(`GRANT INSERT ON sv_auth_target TO sv_reader`, 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		if b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`GRANT USAGE ON SCHEMA public TO sv_reader`, 0))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET ROLE sv_reader`, 0))
		formatReferenceBoth(t, p, b, c, fixture.Select(`SELECT concat('public',1) AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{"public1"}}))
		for _, tc := range []struct{ q, message string }{
			{`SELECT quote_literal(v) FROM sv_private`, "permission denied for table sv_private"},
			{`SELECT concat(nextval('sv_private_seq'))`, "permission denied for sequence sv_private_seq"},
			{`INSERT INTO sv_auth_target SELECT concat(v) FROM sv_private`, "permission denied for table sv_private"},
		} {
			formatReferenceFailure(t, p, b, c, tc.q, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: tc.message}, false)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`RESET ROLE`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT v FROM sv_auth_target`, []string{"v"}, []string{"TEXT"}, [][]any{}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT last_value,is_called FROM sv_private_seq`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
	})
}

func TestReferenceStringVariadicControls(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		want := stringVariadicStoredSetup(t, p, b, c)
		formatReferenceBoth(t, p, b, c, want)
		if b.PG {
			return
		} // Named public native-control exception; PG has no these APIs.
		t.Run("native_precanceled", func(t *testing.T) {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := c.QueryContext(ctx, `SELECT concat('canceled',1)`)
			if rows != nil {
				rows.Close()
				t.Fatal("precanceled query returned cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("precanceled query: %v", err)
			}
			formatReferenceBoth(t, p, b, c, want)
		})
		t.Run("native_dump_restore", func(t *testing.T) {
			var before bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
			old, err := c.QueryContext(p.Ctx, want.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			metadata, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			bad := `CREATE TABLE sv_bad(v text); INSERT INTO sv_bad VALUES(concat('valid',1)); INSERT INTO sv_bad VALUES(quote_ident(42));`
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
			if err == nil {
				t.Fatal("invalid Restore succeeded")
			}
			// Native-only diagnostic contract: this Restore path is first reached after
			// production. Assert the native binder's complete positioned signature error,
			// not an invented PostgreSQL Restore parity record.
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function quote_ident(integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: fixture.Position(len([]rune(bad[:strings.Index(bad, "quote_ident")])) + 1)})
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if before.String() != after.String() {
				t.Fatal("failed Restore changed persistent state")
			}
			fixture.StringCoreRetainedRows(t, old, nil, want.Columns, want.Types, want.Rows, want.Bag)
			for i, col := range metadata {
				if col.DatabaseTypeName() != want.Types[i] {
					t.Fatal("retained metadata changed")
				}
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(before.Bytes())); err != nil {
				t.Fatal(err)
			}
			formatReferenceBoth(t, p, b, c, want)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO sv_stored(id) VALUES(3)`, 1))
			formatReferenceBoth(t, p, b, c, fixture.Select(`SELECT quoted,joined FROM sv_stored_view WHERE id=3`, []string{"quoted", "joined"}, []string{"TEXT", "TEXT"}, [][]any{{`'d1'`, "1|2"}}))
		})
	})
}

package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceOperatorClassDiagnostics(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec(`CREATE TABLE class_diag_rows(n integer)`, 0))
		run(fixture.Exec(`INSERT INTO class_diag_rows VALUES(7)`, 1))
		held := classHeld(t, p, conn, `SELECT n FROM class_diag_rows WHERE n=$1`)
		run(fixture.Exec(`CREATE OPERATOR CLASS public.class_diag_existing FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, 0))
		run(fixture.Exec(`CREATE OPERATOR FAMILY public.class_diag_collision USING btree`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.class_diag_collision USING btree ADD FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, 0))
		type bad struct{ name, suffix, code, message, detail, hint string }
		cases := []bad{
			{"result", `FUNCTION 1 pg_catalog.int4eq(integer,integer)`, "42P17", `ordering comparison functions must return integer`, "", ""},
			{"arity", `FUNCTION 1 pg_catalog.hashint4(integer)`, "42P17", `ordering comparison functions must have two arguments`, "", ""},
			{"strategy", `OPERATOR 6 pg_catalog.<`, "42P17", `invalid operator number 6, must be between 1 and 5`, "", ""},
			{"support", `FUNCTION 7 pg_catalog.btint4cmp(integer,integer)`, "42P17", `invalid function number 7, must be between 1 and 6`, "", ""},
			{"storage_twice", `STORAGE integer, STORAGE integer`, "42P17", `storage type specified more than once`, "", ""},
			{"storage", `STORAGE bigint`, "42P17", `storage type cannot be different from data type for access method "btree"`, "", ""},
			{"duplicate_item", `FUNCTION 1 pg_catalog.btint4cmp(integer,integer), FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, "42P17", `function number 1 for (integer,integer) appears more than once`, "", ""},
			{"sort_signature", `FUNCTION 2 pg_catalog.int4mi(integer,integer)`, "42P17", `ordering sort support functions must accept type "internal"`, "", ""},
			{"options", `FUNCTION 5 pg_catalog.int4mi(integer,integer)`, "42P17", `invalid operator class options parsing function`, "", `Valid signature of operator class options parsing function is (internal) RETURNS void.`},
			{"cross_options", `FUNCTION 5 (integer,bigint) pg_catalog.btint4sortsupport(internal)`, "42P17", `associated data types for operator class options parsing functions must match opclass input type`, "", ""},
			{"cross_skip", `FUNCTION 6 (integer,bigint) pg_catalog.btint4sortsupport(internal)`, "42P17", `btree skip support functions must not be cross-type`, "", ""},
			{"function", `FUNCTION 1 pg_catalog.class_diag_missing(integer,integer)`, "42883", `function pg_catalog.class_diag_missing(integer, integer) does not exist`, "", ""},
			{"operator", `OPERATOR 1 public.===`, "42883", `operator does not exist: integer public.=== integer`, "", ""},
			{"nonboolean", `OPERATOR 1 pg_catalog.+`, "42P17", `index search operators must return boolean`, "", ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				q := `CREATE OPERATOR CLASS public.class_diag_failed FOR TYPE integer USING btree AS ` + tc.suffix
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail, Hint: tc.hint})
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
					classDiagnosticAtomicity(t, p, b, conn)
				}
			})
		}
		queries := []struct{ q, code, message, detail string }{
			{`CREATE OPERATOR CLASS public.class_diag_failed DEFAULT FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, "42710", `could not make operator class "class_diag_failed" be default for type pg_catalog.int4`, `Operator class "int4_ops" already is the default.`},
			{`CREATE OPERATOR CLASS public.class_diag_existing FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, "42710", `operator class "class_diag_existing" for access method "btree" already exists`, ""},
			{`CREATE OPERATOR CLASS public.class_diag_existing DEFAULT FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.int4eq(integer,integer)`, "42P17", `ordering comparison functions must return integer`, ""},
			{`CREATE OPERATOR CLASS public.class_diag_failed FOR TYPE integer USING class_diag_missing AS STORAGE integer`, "42704", `access method "class_diag_missing" does not exist`, ""},
			{`CREATE OPERATOR CLASS class_diag_missing.class_diag_failed FOR TYPE integer USING btree AS STORAGE integer`, "3F000", `schema "class_diag_missing" does not exist`, ""},
			{`CREATE OPERATOR CLASS public.class_diag_failed FOR TYPE class_diag_missing USING btree AS STORAGE integer`, "42704", `type "class_diag_missing" does not exist`, ""},
			{`CREATE OPERATOR CLASS public.class_diag_failed FOR TYPE integer USING btree FAMILY public.class_diag_missing AS STORAGE integer`, "42704", `operator family "public.class_diag_missing" does not exist for access method "btree"`, ""},
			{`CREATE OPERATOR CLASS public.class_diag_failed FOR TYPE integer USING hash FAMILY public.class_diag_collision AS FUNCTION 1 pg_catalog.hashint4(integer)`, "42704", `operator family "public.class_diag_collision" does not exist for access method "hash"`, ""},
			{`DROP OPERATOR CLASS public.class_diag_missing USING btree`, "42704", `operator class "public.class_diag_missing" does not exist for access method "btree"`, ""},
			{`CREATE INDEX class_diag_failed_index ON class_diag_rows(n pg_catalog.text_ops)`, "42804", `operator class "pg_catalog.text_ops" does not accept data type integer`, ""},
			{`CREATE OPERATOR CLASS public.class_diag_failed FOR TYPE integer USING hash AS OPERATOR 2 pg_catalog.=`, "42P17", `invalid operator number 2, must be between 1 and 1`, ""},
			{`CREATE OPERATOR CLASS public.class_diag_failed FOR TYPE integer USING hash AS FUNCTION 4 pg_catalog.hashint4(integer)`, "42P17", `invalid function number 4, must be between 1 and 3`, ""},
			{`CREATE OPERATOR CLASS public.class_diag_failed FOR TYPE integer USING gist AS FUNCTION 13 pg_catalog.int4mi(integer,integer)`, "42P17", `invalid function number 13, must be between 1 and 12`, ""},
			{`CREATE OPERATOR CLASS public.class_diag_failed FOR TYPE integer USING gist AS OPERATOR 32768 pg_catalog.=`, "42P17", `invalid operator number 32768, must be between 1 and 32767`, ""},
		}
		for i, tc := range queries {
			t.Run(fmt.Sprintf("resolution_%02d", i), func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail})
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
					classDiagnosticAtomicity(t, p, b, conn)
				}
			})
		}
		t.Run("stored_procedure_collision", func(t *testing.T) {
			var familyOID int64
			if err := conn.QueryRowContext(p.Ctx, `SELECT f.oid::bigint FROM pg_opfamily f JOIN pg_namespace n ON n.oid=f.opfnamespace WHERE n.nspname='public' AND f.opfname='class_diag_collision' AND f.opfmethod=403`).Scan(&familyOID); err != nil {
				t.Fatal(err)
			}
			want := fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "pg_amproc_fam_proc_index"`, Detail: fmt.Sprintf(`Key (amprocfamily, amproclefttype, amprocrighttype, amprocnum)=(%d, 23, 23, 1) already exists.`, familyOID), Schema: "pg_catalog", Table: "pg_amproc", Constraint: "pg_amproc_fam_proc_index"}
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(`CREATE OPERATOR CLASS public.class_diag_failed FOR TYPE integer USING btree FAMILY public.class_diag_collision AS OPERATOR 1 pg_catalog.<, FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, want)
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
				classDiagnosticAtomicity(t, p, b, conn)
			}
		})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(held.SQL, []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}, 7))
		for _, tc := range []struct{ q, tag string }{
			{`CREATE OPERATOR CLASS public.class_diag_boundary FOR TYPE integer USING btree AS STORAGE integer`, "CREATE OPERATOR CLASS"},
			{`ALTER OPERATOR CLASS public.class_diag_existing USING btree RENAME TO class_diag_boundary`, "ALTER OPERATOR CLASS"},
			{`DROP OPERATOR CLASS public.class_diag_existing USING btree`, "DROP OPERATOR CLASS"},
		} {
			for _, prepared := range []bool{false, true} {
				run(fixture.Exec(`BEGIN READ ONLY`, 0))
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "25006", Message: `cannot execute ` + tc.tag + ` in a read-only transaction`})
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				run(fixture.Exec(`ROLLBACK`, 0))
				run(fixture.Exec(`BEGIN`, 0))
				s = fixture.Exec(tc.q, 0)
				s.Prepared = prepared
				run(s)
				run(fixture.Exec(`ROLLBACK`, 0))
			}
		}
		classSyntaxBoundaries(t, p, b, conn)
	})
}

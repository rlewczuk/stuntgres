package reference_test

import (
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAccessMethodDiagnostics(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"am_diag_reader"})
	amBackends(t, []string{"am_diag_existing", "am_diag_failed", "am_diag_table", "am_diag_gin", "am_diag_boundary"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec(`CREATE ACCESS METHOD am_diag_existing TYPE INDEX HANDLER bthandler`, 0))
		run(fixture.Exec(`CREATE ACCESS METHOD am_diag_table TYPE TABLE HANDLER heap_tableam_handler`, 0))
		run(fixture.Exec(`CREATE TABLE am_diag_rows(n integer)`, 0))
		run(fixture.Exec(`INSERT INTO am_diag_rows VALUES(7)`, 1))
		recovery := classHeld(t, p, conn, `SELECT n FROM am_diag_rows WHERE n=$1`)
		atomic := func() {
			run(amCount(`SELECT count(*) FROM pg_am WHERE amname='am_diag_failed'`, 0))
			run(amCount(`SELECT count(*) FROM pg_class WHERE relname='am_diag_failed_index'`, 0))
			run(fixture.Select(`SELECT 42 AS answer`, []string{"answer"}, []string{"INT4"}, [][]any{{int64(42)}}))
			fixture.RunStep(t, p.Ctx, recovery, b.Recorder, fixture.Select(recovery.SQL, []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}, 7))
		}
		for _, tc := range []struct{ q, code, message string }{
			{`CREATE ACCESS METHOD am_diag_existing TYPE INDEX HANDLER am_diag_missing`, "42710", `access method "am_diag_existing" already exists`},
			{`CREATE ACCESS METHOD am_diag_failed TYPE INDEX HANDLER am_diag_missing`, "42883", `function am_diag_missing(internal) does not exist`},
			{`CREATE ACCESS METHOD am_diag_failed TYPE INDEX HANDLER public.am_diag_missing`, "42883", `function public.am_diag_missing(internal) does not exist`},
			{`CREATE ACCESS METHOD am_diag_failed TYPE INDEX HANDLER int4in`, "42883", `function int4in(internal) does not exist`},
			{`CREATE ACCESS METHOD am_diag_failed TYPE INDEX HANDLER btint4cmp`, "42883", `function btint4cmp(internal) does not exist`},
			{`CREATE ACCESS METHOD am_diag_failed TYPE INDEX HANDLER version`, "42883", `function version(internal) does not exist`},
			{`CREATE ACCESS METHOD am_diag_failed TYPE INDEX HANDLER heap_tableam_handler`, "42809", `function heap_tableam_handler must return type index_am_handler`},
			{`CREATE ACCESS METHOD am_diag_failed TYPE INDEX HANDLER pg_catalog.heap_tableam_handler`, "42809", `function heap_tableam_handler must return type index_am_handler`},
			{`CREATE ACCESS METHOD am_diag_failed TYPE TABLE HANDLER pg_catalog.bthandler`, "42809", `function bthandler must return type table_am_handler`},
			{`CREATE ACCESS METHOD am_diag_failed TYPE TABLE HANDLER ginhandler`, "42809", `function ginhandler must return type table_am_handler`},
			{`CREATE INDEX am_diag_failed_index ON am_diag_rows USING am_diag_missing(n)`, "42704", `access method "am_diag_missing" does not exist`},
			{`DROP ACCESS METHOD am_diag_missing`, "42704", `access method "am_diag_missing" does not exist`},
			{`DROP ACCESS METHOD btree`, "2BP01", `cannot drop access method btree because it is required by the database system`},
		} {
			t.Run(tc.q, func(t *testing.T) {
				amError(t, p, b, conn, tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
				atomic()
			})
		}
		// PG18.6 invokes the heap handler before discovering the wrong routine
		// struct. Native rejects the method kind without invoking a heap pointer.
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(`CREATE INDEX am_diag_failed_index ON am_diag_rows USING am_diag_table(n)`, fixture.Diagnostic{Code: "55000", Message: `access method "am_diag_table" is not of type INDEX`})
			s.PostgresDiagnostic = &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: `index access method handler function 3 did not return an IndexAmRoutine struct`}
			if b.PG {
				s.State = s.PostgresDiagnostic.Code
			}
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			atomic()
		}
		q := `CREATE ACCESS METHOD am_diag_gin TYPE INDEX HANDLER pg_catalog.ginhandler`
		if b.PG {
			run(fixture.Prepared(fixture.Exec(q, 0)))
			run(fixture.Exec(`DROP ACCESS METHOD am_diag_gin`, 0))
		} else {
			amError(t, p, b, conn, q, fixture.Diagnostic{Code: "0A000", Message: `access method handler has no registered native implementation`})
			run(amCount(`SELECT count(*) FROM pg_am WHERE amname='am_diag_gin'`, 0))
		}
		for _, tc := range []struct{ q, token string }{
			{`CREATE ACCESS METHOD am_diag_failed TYPE BOGUS HANDLER bthandler`, "BOGUS"},
			{`CREATE ACCESS METHOD public.am_diag_failed TYPE INDEX HANDLER bthandler`, "."},
			{`CREATE ACCESS METHOD am_diag_failed TYPE INDEX HANDLER bthandler(internal)`, "("},
			{`CREATE ACCESS METHOD am_diag_failed TYPE INDEX HANDLER bthandler GARBAGE`, "GARBAGE"},
			{`DROP ACCESS METHOD am_diag_existing GARBAGE`, "GARBAGE"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "` + tc.token + `"`, Position: strconv.Itoa(strings.Index(tc.q, tc.token) + 1)})
				s.Prepared, s.ErrorStage = prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
				atomic()
			}
		}
		run(fixture.Exec(`CREATE ROLE am_diag_reader`, 0))
		// Prepare as superuser, execute after SET ROLE: authority is not cached.
		createQ := `CREATE ACCESS METHOD am_diag_failed TYPE INDEX HANDLER am_diag_missing`
		dropQ := `DROP ACCESS METHOD am_diag_existing`
		heldCreate := classHeld(t, p, conn, createQ)
		heldDrop := classHeld(t, p, conn, dropQ)
		run(fixture.Exec(`SET ROLE am_diag_reader`, 0))
		denied := fixture.Diagnostic{Code: "42501", Message: `permission denied to create access method "am_diag_failed"`, Hint: `Must be superuser to create an access method.`}
		amError(t, p, b, conn, createQ, denied)
		s := fixture.ImplicitError(createQ, denied)
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, heldCreate, b.Recorder, s)
		amError(t, p, b, conn, `CREATE ACCESS METHOD am_diag_existing TYPE INDEX HANDLER am_diag_missing`, fixture.Diagnostic{Code: "42501", Message: `permission denied to create access method "am_diag_existing"`, Hint: `Must be superuser to create an access method.`})
		amError(t, p, b, conn, dropQ, fixture.Diagnostic{Code: "42501", Message: `must be superuser`})
		s = fixture.ImplicitError(dropQ, fixture.Diagnostic{Code: "42501", Message: `must be superuser`})
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, heldDrop, b.Recorder, s)
		amError(t, p, b, conn, `DROP ACCESS METHOD btree`, fixture.Diagnostic{Code: "42501", Message: `must be superuser`})
		amError(t, p, b, conn, `DROP ACCESS METHOD am_diag_missing`, fixture.Diagnostic{Code: "42704", Message: `access method "am_diag_missing" does not exist`})
		run(fixture.Exec(`RESET ROLE`, 0))
		var notices []fixture.Diagnostic
		_, nc, nr := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		for _, prepared := range []bool{false, true} {
			fixture.RunStep(t, p.Ctx, nc, nr, fixture.Exec(`SET ROLE am_diag_reader`, 0))
			fixture.RoleGrantNoticeExec(t, p, nc, nr, prepared, &notices, `DROP ACCESS METHOD IF EXISTS am_diag_missing`, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `access method "am_diag_missing" does not exist, skipping`})
			fixture.RunStep(t, p.Ctx, nc, nr, fixture.Exec(`RESET ROLE`, 0))
		}
		for _, tc := range []struct{ q, tag string }{
			{`CREATE ACCESS METHOD am_diag_boundary TYPE INDEX HANDLER bthandler`, "CREATE ACCESS METHOD"},
			{`DROP ACCESS METHOD am_diag_existing`, "DROP ACCESS METHOD"},
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
		atomic()
	})
}

func TestReferenceAccessMethodQuotedDiagnostics(t *testing.T) {
	amBackends(t, []string{`am\quoted`, `am"quoted`}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec(`CREATE TABLE am_quoted_rows(n integer)`, 0))
		run(fixture.Exec(`INSERT INTO am_quoted_rows VALUES(7),(NULL)`, 2))
		for _, tc := range []struct{ name, token, message, missing string }{
			{`am\quoted`, `"am\quoted"`, `data type integer has no default operator class for access method "am\quoted"`, `access method "am\quoted" does not exist`},
			{`am"quoted`, `"am""quoted"`, `data type integer has no default operator class for access method "am"quoted"`, `access method "am"quoted" does not exist`},
		} {
			missingAddress := func() {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(`SELECT * FROM pg_get_object_address('access method',ARRAY[$1],ARRAY[]::text[])`, fixture.Diagnostic{Code: "42704", Message: tc.missing}, tc.name)
					s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
					run(s)
				}
			}
			missingAddress()
			run(fixture.Exec(`CREATE ACCESS METHOD `+tc.token+` TYPE INDEX HANDLER pg_catalog.bthandler`, 0))
			amError(t, p, b, conn, `CREATE INDEX am_quoted_i ON am_quoted_rows USING `+tc.token+`(n)`, fixture.Diagnostic{
				Code: "42704", Message: tc.message,
				Hint: `You must specify an operator class for the index or define a default operator class for the data type.`,
			})
			run(amCount(`SELECT count(*) FROM pg_class WHERE relname='am_quoted_i'`, 0))
			run(fixture.Select(`SELECT amhandler::oid::bigint,amtype FROM pg_am WHERE amname=$1`, []string{"amhandler", "amtype"}, []string{"INT8", "CHAR"}, [][]any{{int64(330), "i"}}, tc.name))
			run(fixture.Select(`SELECT n FROM am_quoted_rows ORDER BY n NULLS LAST`, []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}))
			run(fixture.Exec(`DROP ACCESS METHOD `+tc.token, 0))
			missingAddress()
		}
	})
}

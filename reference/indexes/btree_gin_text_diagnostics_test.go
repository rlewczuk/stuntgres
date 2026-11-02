package reference_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceBtreeGINTextDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, keys, code, message string }{
		{"missing", "t missing_ops", "42704", `operator class "missing_ops" does not exist for access method "gin"`},
		{"qualified_missing", "t public.missing_ops", "42704", `operator class "public.missing_ops" does not exist for access method "gin"`},
		{"bytea_text", "x text_ops", "42804", `operator class "text_ops" does not accept data type bytea`},
		{"qualified_bytea_text", "x public.text_ops", "42804", `operator class "public.text_ops" does not accept data type bytea`},
		{"text_bytea", "t bytea_ops", "42804", `operator class "bytea_ops" does not accept data type text`},
		{"bpchar_text", "b text_ops", "42804", `operator class "text_ops" does not accept data type character`},
		{"bpchar_varchar", "b varchar_ops", "42804", `operator class "varchar_ops" does not accept data type character`},
		{"name_text", "n text_ops", "42804", `operator class "text_ops" does not accept data type name`},
		{"char_bpchar", "c bpchar_ops", "42804", `operator class "bpchar_ops" does not accept data type "char"`},
		{"late_key", "t text_ops,x text_ops", "42804", `operator class "text_ops" does not accept data type bytea`},
		{"unique", "t", "0A000", `access method "gin" does not support unique indexes`},
		{"asc", "t ASC", "0A000", `access method "gin" does not support ASC/DESC options`},
		{"desc", "t DESC", "0A000", `access method "gin" does not support ASC/DESC options`},
		{"options", "t text_ops(siglen=1)", "22023", `operator class public.text_ops has no options`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
				run(fixture.Exec(`CREATE TABLE text_diag(id integer,t text,v varchar,b bpchar,c "char",n name,x bytea)`, 0))
				run(fixture.Exec(`INSERT INTO text_diag VALUES(1,'ab','ab','ab','b','ab','\x80'),(2,NULL,NULL,NULL,NULL,NULL,NULL)`, 2))
				run(fixture.Exec("CREATE INDEX text_good ON text_diag USING gin(t text_ops)", 0))
				unique := ""
				if tc.name == "unique" {
					unique = "UNIQUE "
				}
				for _, prefix := range []string{"", "/*é*/ "} {
					ginUtilityError(t, p, b, conn, prefix+"CREATE "+unique+"INDEX text_bad ON text_diag USING gin("+tc.keys+")", fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
					compoundIndexAbsent(run, "text_diag", "text_bad", 1)
					classRoutes(t, p, b, conn, ginIDs("SELECT id FROM text_diag ORDER BY id", []int64{1, 2}))
					classRoutes(t, p, b, conn, ginIDs("SELECT id FROM text_diag WHERE t='ab'::text ORDER BY id", []int64{1}))
					run(fixture.Exec("CREATE INDEX text_bad ON text_diag USING gin(t text_ops,x bytea_ops)", 0))
					run(fixture.Exec("DROP INDEX text_bad", 0))
				}
			})
		})
	}
	t.Run("distance", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			_, run := classConn(t, p, b)
			for _, tc := range textCases {
				for _, prefix := range []string{"", "/*é*/ "} {
					q := prefix + "SELECT " + networkLiteral(tc.boundary, textSQLType(tc)) + " <-> " + networkLiteral(tc.boundary, textSQLType(tc))
					label := map[string]string{"text": "text", "varchar": "character varying", "bpchar": "character", "char": `"char"`, "name": "name", "bytea": "bytea"}[tc.typ]
					d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+label+" <-> "+label)
					d.Hint = compoundOperatorHint
					d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "<->")]) + 1)
					s := fixture.ErrorDiagnostic(q, d)
					s.SelectRows = true
					s.ErrorStage = "execute"
					run(s)
					s.Prepared = true
					s.ErrorStage = "prepare"
					run(s)
				}
			}
		})
	})
	t.Run("savepoint_and_cancel", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			compoundCleanup(t, p, b, conn)
			run(fixture.Exec("CREATE EXTENSION btree_gin", 0))
			run(fixture.Exec("CREATE TABLE text_atomic(id integer,t text,x bytea)", 0))
			run(fixture.Exec(`INSERT INTO text_atomic VALUES(1,'ab','\x80'),(2,NULL,NULL)`, 2))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT text_before", 0))
			s := fixture.ErrorDiagnostic("CREATE INDEX text_atomic_i ON text_atomic USING gin(t text_ops,x text_ops)", fixture.ControlsReferenceDiagnostic("42804", `operator class "text_ops" does not accept data type bytea`))
			s.ErrorStage = "execute"
			run(s)
			run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
			run(fixture.Exec("ROLLBACK TO text_before", 0))
			compoundIndexAbsent(run, "text_atomic", "text_atomic_i", 0)
			const good = "CREATE INDEX text_atomic_i ON text_atomic USING gin(t text_ops,x bytea_ops)"
			run(fixture.Exec(good, 0))
			run(fixture.Exec("INSERT INTO text_atomic VALUES(3,'ab',NULL)", 1))
			run(fixture.Exec("ROLLBACK", 0))
			compoundIndexAbsent(run, "text_atomic", "text_atomic_i", 0)
			run(ginIDs("SELECT id FROM text_atomic ORDER BY id", []int64{1, 2}))
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				result, err := conn.ExecContext(ctx, good)
				var state interface{ SQLState() string }
				if !errors.Is(err, context.Canceled) || result != nil || errors.As(err, &state) {
					t.Fatalf("ordinary cancellation result=%v err=%v", result, err)
				}
				compoundIndexAbsent(run, "text_atomic", "text_atomic_i", 0)
			}
			run(fixture.Exec(good, 0))
		})
	})
}

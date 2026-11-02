package reference_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceBtreeGINCompoundDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, clause, code, message string }{
		{"missing", "INDEX compound_bad ON compound_diag USING gin(m missing_ops)", "42704", `operator class "missing_ops" does not exist for access method "gin"`},
		{"qualified_missing", "INDEX compound_bad ON compound_diag USING gin(m public.missing_ops)", "42704", `operator class "public.missing_ops" does not exist for access method "gin"`},
		{"bit_money", "INDEX compound_bad ON compound_diag USING gin(b money_ops)", "42804", `operator class "money_ops" does not accept data type bit`},
		{"money_bit", "INDEX compound_bad ON compound_diag USING gin(m bit_ops)", "42804", `operator class "bit_ops" does not accept data type money`},
		{"enum_integer", "INDEX compound_bad ON compound_diag USING gin(n enum_ops)", "42804", `operator class "enum_ops" does not accept data type integer`},
		{"enum_text", "INDEX compound_bad ON compound_diag USING gin(s enum_ops)", "42804", `operator class "enum_ops" does not accept data type text`},
		{"money_numeric", "INDEX compound_bad ON compound_diag USING gin(c money_ops)", "42804", `operator class "money_ops" does not accept data type numeric`},
		{"money_integer", "INDEX compound_bad ON compound_diag USING gin(n money_ops)", "42804", `operator class "money_ops" does not accept data type integer`},
		{"unique", "UNIQUE INDEX compound_bad ON compound_diag USING gin(m)", "0A000", `access method "gin" does not support unique indexes`},
		{"option", "INDEX compound_bad ON compound_diag USING gin(m money_ops(siglen=1))", "22023", `operator class public.money_ops has no options`},
		{"desc", "INDEX compound_bad ON compound_diag USING gin(m DESC)", "0A000", `access method "gin" does not support ASC/DESC options`},
		{"nulls", "INDEX compound_bad ON compound_diag USING gin(m NULLS FIRST)", "0A000", `access method "gin" does not support NULLS FIRST/LAST options`},
		{"late_key", "INDEX compound_bad ON compound_diag USING gin(m money_ops,b money_ops)", "42804", `operator class "money_ops" does not accept data type bit`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
				run(fixture.Exec("CREATE TABLE compound_diag(id integer,m money,b bit(3),n integer,s text,c numeric)", 0))
				run(fixture.Exec("INSERT INTO compound_diag VALUES(1,'0.01','100',1,'a',1),(2,NULL,NULL,NULL,NULL,NULL)", 2))
				run(fixture.Exec("CREATE INDEX compound_diag_good ON compound_diag USING gin(m money_ops)", 0))
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				for _, prefix := range []string{"", "/*é*/ "} {
					ginUtilityError(t, p, b, conn, prefix+"CREATE "+tc.clause, d)
					compoundIndexAbsent(run, "compound_diag", "compound_bad", 1)
					classRoutes(t, p, b, conn, ginIDs("SELECT id FROM compound_diag ORDER BY id", []int64{1, 2}))
					run(ginIDs("SELECT id FROM compound_diag WHERE m='0.01'::money AND b='100'::bit(3) ORDER BY id", []int64{1}))
					run(fixture.Exec("CREATE INDEX compound_bad ON compound_diag USING gin(m money_ops,b bit_ops)", 0))
					run(fixture.Exec("DROP INDEX compound_bad", 0))
				}
			})
		})
	}
	t.Run("operator_boundaries", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			compoundCleanup(t, p, b, conn)
			run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
			run(fixture.Exec("CREATE TYPE compound_diag_enum AS ENUM('z','a','m')", 0))
			run(fixture.Exec("CREATE TYPE compound_diag_other AS ENUM('z','a','m')", 0))
			for _, tc := range []struct{ query, operator, signature string }{
				{"SELECT '1'::money <-> '2'::money", "<->", "money <-> money"},
				{"SELECT '1'::bit <-> '0'::bit", "<->", "bit <-> bit"},
				{"SELECT '1'::varbit <-> '0'::varbit", "<->", "bit varying <-> bit varying"},
				{"SELECT 'a'::compound_diag_enum <-> 'z'::compound_diag_enum", "<->", "compound_diag_enum <-> compound_diag_enum"},
				{"SELECT 'a'::compound_diag_enum = 'a'::compound_diag_other", "=", "compound_diag_enum = compound_diag_other"},
			} {
				for _, prefix := range []string{"", "/*é*/ "} {
					q := prefix + tc.query
					d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+tc.signature)
					d.Hint = compoundOperatorHint
					d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, tc.operator)]) + 1)
					s := fixture.ErrorDiagnostic(q, d)
					s.SelectRows = true
					s.ErrorStage = "execute"
					run(s)
					s.Prepared = true
					s.ErrorStage = "prepare"
					run(s)
				}
			}
			// Cross-type bit classes are admitted, unlike unrelated money/text classes.
			run(fixture.Exec("CREATE TABLE compound_cross_diag(id integer,b bit(3),v varbit)", 0))
			run(fixture.Exec("CREATE INDEX compound_cross_diag_i ON compound_cross_diag USING gin(b varbit_ops,v bit_ops)", 0))
			run(fixture.Exec("INSERT INTO compound_cross_diag VALUES(1,'100','00'),(2,NULL,NULL)", 2))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM compound_cross_diag WHERE b='100'::bit(3) AND v>'0'::varbit ORDER BY id", []int64{1}))
		})
	})
	t.Run("savepoint_and_cancel", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			compoundCleanup(t, p, b, conn)
			run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
			run(fixture.Exec("CREATE TABLE compound_atomic(id integer,m money,b bit(3))", 0))
			run(fixture.Exec("INSERT INTO compound_atomic VALUES(1,'0.01','100'),(2,NULL,NULL)", 2))
			const bad = "CREATE INDEX compound_atomic_i ON compound_atomic USING gin(m money_ops,b money_ops)"
			d := fixture.ControlsReferenceDiagnostic("42804", `operator class "money_ops" does not accept data type bit`)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT compound_before", 0))
			s := fixture.ErrorDiagnostic(bad, d)
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Exec("ROLLBACK TO compound_before", 0))
			compoundIndexAbsent(run, "compound_atomic", "compound_atomic_i", 0)
			run(fixture.Exec("CREATE INDEX compound_atomic_i ON compound_atomic USING gin(m money_ops,b bit_ops)", 0))
			run(fixture.Exec("INSERT INTO compound_atomic VALUES(3,'0.01','100')", 1))
			run(ginIDs("SELECT id FROM compound_atomic WHERE m='0.01'::money ORDER BY id", []int64{1, 3}))
			run(fixture.Exec("ROLLBACK", 0))
			compoundIndexAbsent(run, "compound_atomic", "compound_atomic_i", 0)
			run(ginIDs("SELECT id FROM compound_atomic ORDER BY id", []int64{1, 2}))
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				result, err := conn.ExecContext(ctx, "CREATE INDEX compound_atomic_i ON compound_atomic USING gin(m money_ops,b bit_ops)")
				if !errors.Is(err, context.Canceled) || result != nil {
					t.Fatalf("pre-admission index cancellation: result=%v err=%v", result, err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatalf("context cancellation fabricated SQLSTATE: %v", err)
				}
				compoundIndexAbsent(run, "compound_atomic", "compound_atomic_i", 0)
			}
			run(fixture.Exec("CREATE INDEX compound_atomic_i ON compound_atomic USING gin(m money_ops,b bit_ops)", 0))
		})
	})
}

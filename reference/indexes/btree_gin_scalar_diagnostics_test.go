package reference_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceBtreeGINScalarDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, keys, code, message string }{
		{"missing", "i scalar_missing_ops", "42704", `operator class "scalar_missing_ops" does not exist for access method "gin"`},
		{"qualified_missing", "i public.scalar_missing_ops", "42704", `operator class "public.scalar_missing_ops" does not exist for access method "gin"`},
		{"integer_float4", "i float4_ops", "42804", `operator class "float4_ops" does not accept data type integer`},
		{"float4_float8", "r float8_ops", "42804", `operator class "float8_ops" does not accept data type real`},
		{"bool_int2", "b int2_ops", "42804", `operator class "int2_ops" does not accept data type boolean`},
		{"int4_int2", "i int2_ops", "42804", `operator class "int2_ops" does not accept data type integer`},
		{"int2_int8", "s int8_ops", "42804", `operator class "int8_ops" does not accept data type smallint`},
		{"late_key", "i int4_ops,r float8_ops", "42804", `operator class "float8_ops" does not accept data type real`},
		{"unique", "i", "0A000", `access method "gin" does not support unique indexes`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				run(fixture.Exec("CREATE EXTENSION btree_gin", 0))
				run(fixture.Exec("CREATE TABLE scalar_diag(id integer,i integer,r real,b boolean,s smallint)", 0))
				run(fixture.Exec("INSERT INTO scalar_diag VALUES(1,0,'-0',false,-32768),(2,NULL,NULL,NULL,NULL)", 2))
				run(fixture.Exec("CREATE INDEX scalar_good ON scalar_diag USING gin(i int4_ops)", 0))
				unique := ""
				if tc.name == "unique" {
					unique = "UNIQUE "
				}
				for _, prefix := range []string{"", "/*é*/ "} {
					ginUtilityError(t, p, b, conn, prefix+"CREATE "+unique+"INDEX scalar_bad ON scalar_diag USING gin("+tc.keys+")", fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
					compoundIndexAbsent(run, "scalar_diag", "scalar_bad", 1)
					classRoutes(t, p, b, conn, ginIDs("SELECT id FROM scalar_diag ORDER BY id", []int64{1, 2}))
					run(ginIDs("SELECT id FROM scalar_diag WHERE i=0 ORDER BY id", []int64{1}))
					run(fixture.Exec("CREATE INDEX scalar_bad ON scalar_diag USING gin(i int4_ops,r float4_ops,b bool_ops,s int2_ops)", 0))
					run(fixture.Exec("DROP INDEX scalar_bad", 0))
				}
			})
		})
	}
	t.Run("distance_boundaries", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			compoundCleanup(t, p, b, conn)
			run(fixture.Exec("CREATE EXTENSION btree_gin", 0))
			labels := []string{"smallint", "integer", "bigint", "real", "double precision", "boolean"}
			for i, tc := range scalarCases {
				for _, prefix := range []string{"", "/*é*/ "} {
					q := prefix + "SELECT " + networkLiteral(tc.lower, tc.typ) + " <-> " + networkLiteral(tc.upper, tc.typ)
					d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+labels[i]+" <-> "+labels[i])
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
			run(amCount("SELECT count(*) FROM pg_opclass c JOIN pg_depend d ON d.objid=c.oid AND d.classid='pg_opclass'::regclass WHERE c.opcmethod=783 AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='btree_gin')", 0))
		})
	})
	t.Run("savepoint_and_cancel", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			compoundCleanup(t, p, b, conn)
			run(fixture.Exec("CREATE EXTENSION btree_gin", 0))
			run(fixture.Exec("CREATE TABLE scalar_atomic(id integer,i integer,r real)", 0))
			run(fixture.Exec("INSERT INTO scalar_atomic VALUES(1,0,'-0'),(2,NULL,NULL)", 2))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT scalar_before", 0))
			bad := fixture.ErrorDiagnostic("CREATE INDEX scalar_atomic_i ON scalar_atomic USING gin(i int4_ops,r float8_ops)", fixture.ControlsReferenceDiagnostic("42804", `operator class "float8_ops" does not accept data type real`))
			bad.ErrorStage = "execute"
			run(bad)
			run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
			run(fixture.Exec("ROLLBACK TO scalar_before", 0))
			compoundIndexAbsent(run, "scalar_atomic", "scalar_atomic_i", 0)
			const good = "CREATE INDEX scalar_atomic_i ON scalar_atomic USING gin(i int4_ops,r float4_ops)"
			run(fixture.Exec(good, 0))
			run(fixture.Exec("INSERT INTO scalar_atomic VALUES(3,0,'NaN')", 1))
			run(fixture.Exec("ROLLBACK", 0))
			compoundIndexAbsent(run, "scalar_atomic", "scalar_atomic_i", 0)
			run(ginIDs("SELECT id FROM scalar_atomic ORDER BY id", []int64{1, 2}))
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				result, err := conn.ExecContext(ctx, good)
				var state interface{ SQLState() string }
				if !errors.Is(err, context.Canceled) || result != nil || errors.As(err, &state) {
					t.Fatalf("ordinary cancellation result=%v err=%v", result, err)
				}
				compoundIndexAbsent(run, "scalar_atomic", "scalar_atomic_i", 0)
			}
			run(fixture.Exec(good, 0))
		})
	})
}

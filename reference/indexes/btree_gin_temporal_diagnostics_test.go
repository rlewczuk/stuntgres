package reference_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceBtreeGINTemporalDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, keys, code, message string }{
		{"missing", "d temporal_missing_ops", "42704", `operator class "temporal_missing_ops" does not exist for access method "gin"`},
		{"qualified_missing", "d public.temporal_missing_ops", "42704", `operator class "public.temporal_missing_ops" does not exist for access method "gin"`},
		{"wrong_type", "d time_ops", "42804", `operator class "time_ops" does not accept data type date`},
		{"qualified_wrong", "d public.time_ops", "42804", `operator class "public.time_ops" does not accept data type date`},
		{"late_key", "d date_ops,t time_ops", "42804", `operator class "time_ops" does not accept data type timestamp without time zone`},
		{"timestamp_zone", "t timestamptz_ops", "42804", `operator class "timestamptz_ops" does not accept data type timestamp without time zone`},
		{"timetz_time", "z time_ops", "42804", `operator class "time_ops" does not accept data type time with time zone`},
		{"interval_date", "i date_ops", "42804", `operator class "date_ops" does not accept data type interval`},
		{"unique", "d", "0A000", `access method "gin" does not support unique indexes`},
		{"options", "d date_ops(siglen=1)", "22023", `operator class public.date_ops has no options`},
		{"asc", "d ASC", "0A000", `access method "gin" does not support ASC/DESC options`},
		{"desc", "d DESC", "0A000", `access method "gin" does not support ASC/DESC options`},
		{"nulls_first", "d NULLS FIRST", "0A000", `access method "gin" does not support NULLS FIRST/LAST options`},
		{"nulls_last", "d NULLS LAST", "0A000", `access method "gin" does not support NULLS FIRST/LAST options`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
				run(fixture.Exec("CREATE TABLE temporal_diag(id integer,d date,t timestamp,z timetz,i interval)", 0))
				run(fixture.Exec("INSERT INTO temporal_diag VALUES(1,'2000-01-01','2000-01-01','09:00+01','1 mon'),(2,NULL,NULL,NULL,NULL)", 2))
				run(fixture.Exec("CREATE INDEX temporal_good ON temporal_diag USING gin(d date_ops)", 0))
				unique := ""
				if tc.name == "unique" {
					unique = "UNIQUE "
				}
				for _, prefix := range []string{"", "/*é*/ "} {
					ginUtilityError(t, p, b, conn, prefix+"CREATE "+unique+"INDEX temporal_bad ON temporal_diag USING gin("+tc.keys+")", fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
					compoundIndexAbsent(run, "temporal_diag", "temporal_bad", 1)
					classRoutes(t, p, b, conn, ginIDs("SELECT id FROM temporal_diag ORDER BY id", []int64{1, 2}))
					run(ginIDs("SELECT id FROM temporal_diag WHERE d='2000-01-01'::date ORDER BY id", []int64{1}))
					run(fixture.Exec("CREATE INDEX temporal_bad ON temporal_diag USING gin(d date_ops,t timestamp_ops,z timetz_ops,i interval_ops)", 0))
					run(fixture.Exec("DROP INDEX temporal_bad", 0))
				}
			})
		})
	}
	t.Run("distance_boundary", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			compoundCleanup(t, p, b, conn)
			run(fixture.Exec("CREATE EXTENSION btree_gin", 0))
			for _, tc := range temporalCases {
				for _, prefix := range []string{"", "/*é*/ "} {
					q := prefix + "SELECT " + networkLiteral(tc.boundary, tc.typ) + " <-> " + networkLiteral(tc.boundary, tc.typ)
					d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+temporalTypeLabel(tc.typ)+" <-> "+temporalTypeLabel(tc.typ))
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
			run(fixture.Exec("CREATE TABLE temporal_atomic(id integer,d date,t timestamp)", 0))
			run(fixture.Exec("INSERT INTO temporal_atomic VALUES(1,'2000-01-01','2000-01-01'),(2,NULL,NULL)", 2))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT temporal_before", 0))
			s := fixture.ErrorDiagnostic("CREATE INDEX temporal_atomic_i ON temporal_atomic USING gin(d date_ops,t time_ops)", fixture.ControlsReferenceDiagnostic("42804", `operator class "time_ops" does not accept data type timestamp without time zone`))
			s.ErrorStage = "execute"
			run(s)
			run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
			run(fixture.Exec("ROLLBACK TO temporal_before", 0))
			compoundIndexAbsent(run, "temporal_atomic", "temporal_atomic_i", 0)
			const good = "CREATE INDEX temporal_atomic_i ON temporal_atomic USING gin(d date_ops,t timestamp_ops)"
			run(fixture.Exec(good, 0))
			run(fixture.Exec("INSERT INTO temporal_atomic VALUES(3,'2000-01-01','2000-01-01')", 1))
			run(fixture.Exec("ROLLBACK", 0))
			compoundIndexAbsent(run, "temporal_atomic", "temporal_atomic_i", 0)
			run(ginIDs("SELECT id FROM temporal_atomic ORDER BY id", []int64{1, 2}))
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				result, err := conn.ExecContext(ctx, good)
				var state interface{ SQLState() string }
				if !errors.Is(err, context.Canceled) || result != nil || errors.As(err, &state) {
					t.Fatalf("ordinary cancellation result=%v err=%v", result, err)
				}
				compoundIndexAbsent(run, "temporal_atomic", "temporal_atomic_i", 0)
			}
			run(fixture.Exec(good, 0))
		})
	})
}

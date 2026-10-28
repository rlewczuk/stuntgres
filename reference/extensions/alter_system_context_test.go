package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterSystemContexts(t *testing.T) {
	alterSystemScenario(t,func(t *testing.T,s *alterSystemState) {
		held := loadHeld(t,s.p,s.a,"ALTER SYSTEM RESET ALL")
		s.failure(t,s.a,"SELECT 1; ALTER SYSTEM RESET ALL",loadDiagnostic("25001","ALTER SYSTEM cannot run inside a transaction block"),false,false,false)
		s.unchanged(t)
		for _, begin := range []string{"BEGIN","BEGIN READ ONLY"} {
			for _, useHeld := range []bool{false,true} {
				for _, query := range []bool{false,true} {
					s.exec(t,begin)
					s.exec(t,"SAVEPOINT alter_system_recover")
					prepared := loadHeld(t,s.p,s.a,"ALTER SYSTEM RESET ALL")
					prepared.Statement.Close()
					var e fixture.Executor = s.a
					if useHeld { e = held }
					s.failure(t,e,held.SQL,loadDiagnostic("25001","ALTER SYSTEM cannot run inside a transaction block"),false,query,false)
					s.failure(t,s.a,"SELECT 1",loadDiagnostic("25P02","current transaction is aborted, commands ignored until end of transaction block"),false,true,false)
					s.exec(t,"ROLLBACK TO SAVEPOINT alter_system_recover")
					s.unchanged(t)
					s.exec(t,"ROLLBACK")
					s.unchanged(t)
				}
			}
		}
		for _, tc := range []struct{ sql, message string }{
			{"DO $$\nBEGIN\n ALTER SYSTEM RESET ALL;\nEND\n$$;","ALTER SYSTEM cannot be executed from a function"},
			{"DO $$\nBEGIN\n ALTER SYSTEM RESET ALL;\nEXCEPTION WHEN division_by_zero THEN PERFORM 1;\nEND\n$$;","ALTER SYSTEM cannot run inside a transaction block"},
		} {
			for _, block := range []bool{false,true} {
				if block { s.exec(t,"BEGIN") }
				message := tc.message
				if block { message = "ALTER SYSTEM cannot run inside a transaction block" }
				want := loadDiagnostic("25001",message)
				want.Context = "SQL statement \"ALTER SYSTEM RESET ALL\"\nPL/pgSQL function inline_code_block line 3 at SQL statement"
				s.failure(t,s.a,tc.sql,want,false,false,false)
				if block { s.exec(t,"ROLLBACK") }
				s.unchanged(t)
			}
		}
		if !s.b.PG {
			ctx,cancel := context.WithCancel(s.p.Ctx)
			cancel()
			stmt,err := s.a.PrepareContext(ctx,held.SQL)
			if stmt != nil { stmt.Close() }
			if stmt != nil || !errors.Is(err,context.Canceled) { t.Fatalf("canceled Prepare: %v %v",stmt,err) }
			for _, e := range []fixture.Executor{s.a,held} {
				result,err := e.ExecContext(ctx,held.SQL)
				if result != nil || !errors.Is(err,context.Canceled) { t.Fatalf("canceled Exec: %v %v",result,err) }
				rows,err := e.QueryContext(ctx,held.SQL)
				if rows != nil { rows.Close() }
				if rows != nil || !errors.Is(err,context.Canceled) { t.Fatalf("canceled Query: %v %v",rows,err) }
			}
			s.failure(t,held,held.SQL,loadDiagnostic("0A000",alterSystemMessage),false,true,false)
			s.unchanged(t)
		}
	})
}

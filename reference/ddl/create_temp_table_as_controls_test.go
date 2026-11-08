package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCreateTempTableAsAtomicity(t *testing.T) {
	tempAsScenario(t, func(s *fixture.TempReferenceSession) {
		tempAsExec(s, s.C, "CREATE TABLE ta_atomic_source(i INT)", 0)
		tempAsExec(s, s.C, "INSERT INTO ta_atomic_source VALUES(1),(0)", 2)
		q := "CREATE TEMP TABLE ta_atomic AS SELECT 1/i AS v FROM ta_atomic_source"
		tempAsError(s, s.C, q, "22012", "division by zero", "", false)
		tempAsAbsent(s, "ta_atomic")
		var namespace uint64
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT pg_my_temp_schema()").Scan(&namespace); err != nil || namespace != 0 {
			s.T.Fatal("failed first TEMP creation published namespace", namespace, err)
		}
		tempAsExec(s, s.C, "BEGIN", 0)
		tempAsError(s, s.C, q, "22012", "division by zero", "", false)
		tempAsError(s, s.C, "SELECT 1", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", true)
		tempAsExec(s, s.C, "ROLLBACK", 0)
		tempAsAbsent(s, "ta_atomic")
		tempAsRun(s, s.C, fixture.Select("SELECT i FROM ta_atomic_source ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}}))
		tempAsExec(s, s.C, "CREATE TEMP TABLE ta_atomic AS SELECT 9 AS v", 1)
		tempAsRows(s, s.C, "ta_atomic", 9)
	})
}
func TestReferenceCreateTempTableAsCancellation(t *testing.T) {
	tempAsScenario(t, func(s *fixture.TempReferenceSession) {
		tempAsExec(s, s.C, "CREATE TABLE ta_cancel_source(i INT)", 0)
		tempAsExec(s, s.C, "INSERT INTO ta_cancel_source VALUES(7)", 1)
		q := "CREATE TEMP TABLE ta_canceled AS SELECT i FROM ta_cancel_source FOR UPDATE"
		if !s.Postgres {
			ctx, cancel := context.WithCancel(s.P.Ctx)
			cancel()
			result, err := s.C.ExecContext(ctx, q)
			if result != nil || !errors.Is(err, context.Canceled) {
				s.T.Fatalf("result=%v error=%v", result, err)
			}
		} else {
			tx, err := s.Peer.BeginTx(s.P.Ctx, nil)
			if err != nil {
				s.T.Fatal(err)
			}
			defer tx.Rollback()
			tempAsRun(s, tx, fixture.Select("SELECT i FROM ta_cancel_source FOR UPDATE", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
			tempAsCancelAdmitted(s, q)
			if err := tx.Rollback(); err != nil {
				s.T.Fatal(err)
			}
		}
		tempAsAbsent(s, "ta_canceled")
		tempAsExec(s, s.C, q, 1)
		tempAsRun(s, s.C, fixture.Select("SELECT i FROM ta_canceled", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
	})
}
func tempAsCancelAdmitted(s *fixture.TempReferenceSession, q string) {
	s.T.Helper()
	ctx, cancel := context.WithCancel(s.P.Ctx)
	defer cancel()
	var pid int
	if err := s.C.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		s.T.Fatal(err)
	}
	tuple := fixture.QueryTableCreateTuple(s.T, ctx, s.C, true, "ta_cancel_source")
	var stmt *sql.Stmt
	if s.Prepared {
		var err error
		stmt, err = s.C.PrepareContext(ctx, q)
		if err != nil {
			s.T.Fatal(err)
		}
		defer stmt.Close()
	}
	done := make(chan error, 1)
	go func() {
		var result sql.Result
		var err error
		if stmt != nil {
			result, err = stmt.ExecContext(ctx)
		} else {
			result, err = s.C.ExecContext(ctx, q)
		}
		if result != nil && err != nil {
			done <- errors.New("canceled execution exposed result")
			return
		}
		done <- err
	}()
	joined := false
	defer func() {
		if !joined {
			cancel()
			<-done
		}
	}()
	for {
		var waiting bool
		if err := fixture.Admin.QueryRowContext(ctx, "SELECT COALESCE((SELECT state='active' AND query=$2 AND wait_event_type='Lock' FROM pg_stat_activity WHERE pid=$1),false)", pid, q).Scan(&waiting); err != nil {
			s.T.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			joined = true
			s.T.Fatalf("completed before lock admission: %v", err)
		default:
		}
	}
	var signaled bool
	if err := fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
		s.T.Fatalf("cancel=%v err=%v", signaled, err)
	}
	err := <-done
	joined = true
	fixture.QueryTableCreateLockDiagnostic(s.T, err, true, "57014", "canceling statement due to user request", "ta_cancel_source", tuple)
}
func TestReferenceCreateTempTableAsControls(t *testing.T) {
	tempAsScenario(t, func(s *fixture.TempReferenceSession) {
		tempAsExec(s, s.C, "CREATE TABLE public.ta_control(v INT)", 0)
		tempAsExec(s, s.C, "INSERT INTO public.ta_control VALUES(7)", 1)
		tempAsExec(s, s.C, "CREATE TEMP TABLE ta_control AS SELECT 11 AS v", 1)
		tempAsExec(s, s.Peer, "CREATE TEMP TABLE ta_control AS SELECT 22 AS v", 1)
		if err := s.C.Close(); err != nil {
			s.T.Fatal(err)
		}
		borrowed, err := s.DB.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		s.C = borrowed
		defer borrowed.Close()
		tempAsRows(s, s.C, "ta_control", 11)
		tempAsExec(s, s.C, "CREATE TEMP TABLE ta_control_delete ON COMMIT DELETE ROWS AS SELECT 1 AS v", 1)
		if !s.Postgres {
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
				s.T.Fatal(err)
			}
			for _, forbidden := range []string{"pg_temp_", "ta_control_delete", "VALUES (11)", "VALUES (22)"} {
				if strings.Contains(dump.String(), forbidden) {
					s.T.Fatal("temporary object in dump", dump.String())
				}
			}
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
				s.T.Fatal(err)
			}
			tempAsRows(s, s.C, "ta_control", 11)
			tempAsRows(s, s.Peer, "ta_control", 22)
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader("CREATE TABLE public.ta_control(v INT); INSERT INTO public.ta_control VALUES(9);")); err != nil {
				s.T.Fatal(err)
			}
			tempAsRows(s, s.C, "ta_control", 11)
			tempAsRows(s, s.Peer, "ta_control", 22)
			tempAsRows(s, s.C, "public.ta_control", 9)
			tempAsExec(s, s.C, "BEGIN", 0)
			tempAsExec(s, s.C, "INSERT INTO ta_control_delete VALUES(4)", 1)
			tempAsRows(s, s.C, "ta_control_delete", 4)
			tempAsExec(s, s.C, "COMMIT", 0)
			tempAsRows(s, s.C, "ta_control_delete")
		}
		s.Retire()
		tempAsRows(s, s.Peer, "ta_control", 22)
		c, err := s.DB.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer c.Close()
		if s.Postgres {
			tempAsRows(s, c, "ta_control", 7)
		} else {
			tempAsRows(s, c, "ta_control", 9)
		}
		s.Missing(c, "ta_control_delete", "v", "15")
	})
}

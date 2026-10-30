package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func checkpointErrorStep(q string, d fixture.Diagnostic) fixture.Step {
	d.Severity, d.SeverityUnlocalized = "ERROR", "ERROR"
	return fixture.ErrorDiagnostic(q, d)
}

func checkpointExpected(pg, allowed bool) fixture.Step {
	if !allowed { return checkpointErrorStep("CHECKPOINT", checkpointDenied) }
	if pg { return fixture.Exec("CHECKPOINT", 0) }
	return checkpointErrorStep("CHECKPOINT", checkpointPhysical)
}

func TestReferenceCheckpointContexts(t *testing.T) {
	for _, api := range []bool{false,true} { for _, readOnly := range []bool{false,true} { for _, allowed := range []bool{false,true} {
		t.Run(fmt.Sprintf("api=%v/readonly=%v/allowed=%v",api,readOnly,allowed), func(t *testing.T) {
			predefinedCases(t, []string{"cp_actor"}, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
				if allowed { s.Exec("GRANT pg_checkpoint TO cp_actor") }
				s.Actor("cp_actor")
				for _, aborted := range []bool{false,true} {
					var ex fixture.Executor = s.C
					var tx *sql.Tx
					if api {
						var err error
						tx,err=s.C.BeginTx(s.P.Ctx,&sql.TxOptions{ReadOnly:readOnly})
						if err!=nil { s.T.Fatal(err) }; ex=tx
						defer tx.Rollback()
					} else {
						q:="BEGIN READ WRITE"; if readOnly { q="BEGIN READ ONLY" }
						fixture.RunStep(s.T,s.P.Ctx,ex,s.Record,fixture.Exec(q,0))
						defer s.C.ExecContext(s.P.Ctx,"ROLLBACK")
					}
					stmt,err:=ex.PrepareContext(s.P.Ctx,"CHECKPOINT")
					if err!=nil || stmt==nil { s.T.Fatal("healthy Prepare",err) }
					defer stmt.Close()
					if aborted {
						fixture.RunStep(s.T,s.P.Ctx,ex,s.Record,checkpointErrorStep("SELECT 1/0",fixture.Diagnostic{Code:"22012",Message:"division by zero"}))
						for _,prepared:=range []bool{false,true} {
							step:=checkpointErrorStep("CHECKPOINT",checkpointAborted);step.Prepared=prepared
							if prepared { step.ErrorStage="prepare" }
							fixture.RunStep(s.T,s.P.Ctx,ex,s.Record,step)
						}
						fixture.RunStep(s.T,s.P.Ctx,fixture.HeldStatement{Executor:ex,Statement:stmt,SQL:"CHECKPOINT"},s.Record,checkpointErrorStep("CHECKPOINT",checkpointAborted))
						for _,tc:=range checkpointMalformed {
							for _,prepared:=range []bool{false,true} {
								step:=checkpointErrorStep(tc.sql,fixture.Diagnostic{Code:"42601",Message:`syntax error at or near "`+tc.token+`"`,Position:tc.pos});step.Prepared=prepared
								if prepared { step.ErrorStage="prepare" }
								fixture.RunStep(s.T,s.P.Ctx,ex,s.Record,step)
							}
						}
					} else {
						step:=checkpointExpected(s.Backend.PG,allowed)
						var target fixture.Executor=ex
						if s.Prepared { target=fixture.HeldStatement{Executor:ex,Statement:stmt,SQL:"CHECKPOINT"} }
						fixture.RunStep(s.T,s.P.Ctx,target,s.Record,step)
					}
					if err:=stmt.Close();err!=nil { s.T.Fatal(err) }
					if api { if err:=tx.Rollback();err!=nil { s.T.Fatal(err) } } else { fixture.RunStep(s.T,s.P.Ctx,s.C,s.Record,fixture.Exec("ROLLBACK",0)) }
					predefinedSelect(s,"SELECT 1 AS n",[]string{"n"},[]string{"INT4"},[][]any{{int64(1)}})
				}
			})
		})
	} } }
}

func TestReferenceCheckpointAtomicity(t *testing.T) {
	predefinedCases(t, []string{"cp_actor"}, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Exec("CREATE TABLE checkpoint_data(id INT)")
		s.Run(fixture.Exec("INSERT INTO checkpoint_data VALUES(7)",1))
		s.Exec("GRANT SELECT,INSERT ON checkpoint_data TO cp_actor")
		peer,err:=db.Conn(s.P.Ctx);if err!=nil { s.T.Fatal(err) };defer peer.Close()
		rows:=func(ex fixture.Executor, private bool) {
			s.T.Helper();want:=[][]any{{int64(7)}};if private { want=append(want,[]any{int64(8)}) }
			fixture.RunStep(s.T,s.P.Ctx,ex,s.Record,fixture.Select("SELECT id FROM checkpoint_data ORDER BY id",[]string{"id"},[]string{"INT4"},want))
		}
		dump:=func() []byte {
			s.T.Helper();var out bytes.Buffer
			if err:=stuntgres.Dump(s.P.Ctx,s.P.Native,&out);err!=nil { s.T.Fatal(err) }
			return append([]byte(nil),out.Bytes()...)
		}
		var baseline []byte
		if !s.Backend.PG { baseline=dump() }
		defer s.C.ExecContext(s.P.Ctx,"ROLLBACK")
		for _,mode:=range []string{"allowed","denied","syntax","savepoint"} {
			s.Actor("")
			if mode=="denied" { s.Exec("REVOKE pg_checkpoint FROM cp_actor") } else { s.Exec("GRANT pg_checkpoint TO cp_actor") }
			s.Actor("cp_actor")
			s.Exec("BEGIN")
			s.Run(fixture.Exec("INSERT INTO checkpoint_data VALUES(8)",1))
			rows(s.C,true);rows(peer,false)
			if !s.Backend.PG && !bytes.Equal(baseline,dump()) { s.T.Fatal("private row entered Dump") }
			if mode=="savepoint" { s.Exec("SAVEPOINT cp_keep") }
			if mode=="syntax" {
				predefinedFailure(s,"CHECKPOINT x",fixture.Diagnostic{Code:"42601",Message:`syntax error at or near "x"`,Position:"12"},false,"prepare")
			} else { checkpointRun(s,mode!="denied") }
			rows(peer,false)
			if mode=="savepoint" {
				s.Exec("ROLLBACK TO SAVEPOINT cp_keep");rows(s.C,true)
			} else if s.Backend.PG && mode=="allowed" { rows(s.C,true) } else {
				predefinedFailure(s,"SELECT id FROM checkpoint_data",checkpointAborted,true,"prepare")
			}
			if !s.Backend.PG && !bytes.Equal(baseline,dump()) { s.T.Fatal("rejected CHECKPOINT changed committed Dump") }
			s.Exec("ROLLBACK");rows(s.C,false);rows(peer,false)
		}
		if !s.Backend.PG {
			s.Actor("")
			s.Exec("BEGIN")
			s.Run(fixture.Exec("INSERT INTO checkpoint_data VALUES(8)",1))
			ctx,cancel:=context.WithCancel(s.P.Ctx);cancel()
			result,err:=s.C.ExecContext(ctx,"CHECKPOINT")
			if result!=nil || !errors.Is(err,context.Canceled) { s.T.Fatal("pre-admission cancellation",result,err) }
			rows(s.C,true);rows(peer,false);s.Exec("ROLLBACK")
			if !bytes.Equal(baseline,dump()) { s.T.Fatal("cancellation changed committed Dump") }
			if err:=stuntgres.Reset(s.P.Ctx,s.P.Native);err!=nil { s.T.Fatal(err) }
			if err:=stuntgres.Restore(s.P.Ctx,s.P.Native,bytes.NewReader(baseline));err!=nil { s.T.Fatal(err) }
			rows(s.C,false);rows(peer,false)
		}
	})
}

package stuntgres_test

import (
	"context"
	"stuntgres"
	"testing"
)

func TestSequenceOwnerFormsPublic(t *testing.T) {
	for _, prepared := range []bool{false,true} {
		for _, tc := range []struct{operand,name string}{{"CURRENT_ROLE","current_role"},{"CURRENT_USER","current_user"},{"SESSION_USER","session_user"}} {
			t.Run(tc.operand+map[bool]string{false:"/Direct",true:"/Prepared"}[prepared],func(t *testing.T) {
				c := sequenceConn(t,openLocal(t,stuntgres.NewDriver(),t.Name()))
				sequenceExec(t,c,`CREATE USER "`+tc.name+`"`)
				sequenceExec(t,c,"CREATE SEQUENCE s")
				owner := func(target string) {
					q := "ALTER SEQUENCE s OWNER TO "+target
					if !prepared { sequenceExec(t,c,q); return }
					s,err := c.PrepareContext(context.Background(),q)
					if err != nil { t.Fatal("Prepare",q,err) }
					defer s.Close()
					r,err := s.ExecContext(context.Background())
					if err != nil { t.Fatal(q,err) }
					if n,err := r.RowsAffected(); err != nil || n != 0 { t.Fatal("DDL count",n,err) }
				}
				owner(`"`+tc.name+`"`)
				sequenceExec(t,c,`SET SESSION AUTHORIZATION "`+tc.name+`"`)
				sequenceExec(t,c,"ALTER SEQUENCE s RESTART 14")
				sequenceExec(t,c,"RESET SESSION AUTHORIZATION")
				owner(tc.operand)
				sequenceExec(t,c,`SET SESSION AUTHORIZATION "`+tc.name+`"`)
				_,err := c.ExecContext(context.Background(),"ALTER SEQUENCE s RESTART 100")
				typeCoreDiagnostic(t,err,stuntgres.Error{Code:"42501",Message:"must be owner of sequence s"})
				sequenceExec(t,c,"RESET SESSION AUTHORIZATION")
				sequenceValue(t,c,"SELECT nextval('s')",14)
			})
		}
	}
}

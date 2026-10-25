package core_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestRowLockRelationWaitPosition(t *testing.T) {
	for _, clause := range []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE"} {
		for _, tc := range []struct{ query, position string }{{"SELECT v FROM t ", "15"}, {"/*é*/ SELECT v FROM public.t ", "21"}, {"INSERT INTO destination SELECT v FROM public.t ", "39"}} {
			for _, phase := range []string{"direct", "prepare", "cached"} {
				t.Run(clause+"/"+tc.query+"/"+phase, func(t *testing.T) {
					e := engine.New()
					a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
					enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
					enginetest.SessionSQL(t, a, "CREATE TABLE destination(v INT)")
					enginetest.SessionSQL(t, b, "SET lock_timeout='100ms'")
					ast := enginetest.Statement(t, tc.query+clause)
					op := enginetest.TransactionOperation(t, b)
					desc, err := e.Describe(op, ast)
					op.Done()
					if err != nil {
						t.Fatal(err)
					}
					ar := enginetest.TransactionBegin(t, a)
					defer enginetest.TransactionEnd(t, a, ar, false)
					enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					op, _, err = b.AcquireCommand(ctx)
					if err != nil {
						t.Fatal(err)
					}
					switch phase {
					case "prepare":
						_, err = e.Describe(op, ast)
					case "cached":
						var scope *engine.StatementScope
						scope, err = e.StartStatement(op)
						if err == nil {
							scope.Expected = &desc
							_, err = scope.Execute(ast, 0, nil)
							err = scope.Finish(err)
						}
					default:
						_, err = e.Execute(op, ast, 0, nil)
					}
					err = op.CommandError(err)
					op.Done()
					position := tc.position
					if phase == "cached" {
						position = ""
					}
					waitsDiagnostic(t, err, "55P03", "canceling statement due to lock timeout", position)
				})
			}
		}
	}
}

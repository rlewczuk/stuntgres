package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestRowLockRelationWaitPosition(t *testing.T) {
	for _, clause := range rowLockPublicClauses {
		for _, tc := range []struct{ query, position string }{{"SELECT v FROM t ", "15"}, {"/*é*/ SELECT v FROM public.t ", "21"}, {"INSERT INTO destination SELECT v FROM public.t ", "39"}} {
			for _, phase := range []string{"direct", "prepare", "cached"} {
				t.Run(clause+"/"+tc.query+"/"+phase, func(t *testing.T) {
					_, a, b, ctx := rowLockPublicViewFixture(t)
					transactionAffected(t, ctx, a, "CREATE TABLE destination(v INT)", false, 0)
					query := tc.query + clause
					cached, err := b.PrepareContext(ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer cached.Close()
					transactionAffected(t, ctx, b, "SET lock_timeout='100ms'", false, 0)
					holder, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					transactionAffected(t, ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0)
					switch phase {
					case "prepare":
						stmt, prepareErr := b.PrepareContext(ctx, query)
						err = prepareErr
						if stmt != nil {
							stmt.Close()
							t.Fatal("failed Prepare returned handle")
						}
					case "cached":
						_, err = cached.ExecContext(ctx)
					default:
						_, err = b.ExecContext(ctx, query)
					}
					position := tc.position
					if phase == "cached" {
						position = ""
					}
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "55P03", Message: "canceling statement due to lock timeout", Position: position})
				})
			}
		}
	}
}

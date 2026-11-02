package reference_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func TestReferencePasswordCheckCancellation(t *testing.T) {
	// Genuine parser-valid secret without spending billions of iterations on
	// setup. The hook's username guess, before the ordinary empty-hash check,
	// must honor interruption rather than imposing an invented iteration cap.
	salt := base64.StdEncoding.EncodeToString([]byte("cancel-salt-1234"))
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	verifier := "SCRAM-SHA-256$2147483647:"+salt+"$"+key+":"+key
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			passwordCheckScenario(t, []string{"pc_cancel"}, func(p *fixture.Pair, b fixture.Backend, admin *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				ra := passwordCheckRun(t, p, admin, r, prepared)
				stored := passwordCheckSCRAM(t, "role-attributes-fixture")
				ra(fixture.Exec("CREATE ROLE pc_cancel LOGIN CONNECTION LIMIT 1 PASSWORD "+pq.QuoteLiteral(stored), 0))
				oid := passwordCheckOID(t, p, admin, "pc_cancel")
				db := fixture.RoleAttributesPool(t, p, b, "pc_cancel")
				db.SetMaxOpenConns(1)
				db.SetMaxIdleConns(0)
				c, err := db.Conn(p.Ctx)
				if err != nil { t.Fatal(err) }
				defer c.Close()
				run := passwordCheckRun(t, p, c, b.Recorder, prepared)
				query := "ALTER ROLE CURRENT_USER PASSWORD "+pq.QuoteLiteral(verifier)
				var stmt *sql.Stmt
				if prepared {
					stmt, err = c.PrepareContext(p.Ctx, query)
					if err != nil { t.Fatal("cancellation must reach Execute", err) }
					defer stmt.Close()
				}
				if !b.PG {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					var result sql.Result
					if stmt != nil { result, err = stmt.ExecContext(ctx) } else { result, err = c.ExecContext(ctx, query) }
					if result != nil || !errors.Is(err, context.Canceled) { t.Fatal("native pre-canceled password", result, err) }
				}
				budget := 40*time.Millisecond
				if b.PG {
					run(fixture.Exec("SET statement_timeout='100ms'", 0))
					budget = fixture.Liveness
				}
				ctx, cancel := context.WithTimeout(p.Ctx, budget)
				defer cancel()
				started := time.Now()
				var result sql.Result
				if stmt != nil { result, err = stmt.ExecContext(ctx) } else { result, err = c.ExecContext(ctx, query) }
				if result != nil { t.Fatal("canceled hook returned a result") }
				if time.Since(started) > fixture.Liveness { t.Fatal("password hook failed bounded cancellation") }
				if b.PG {
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to statement timeout"})
					cancel()
					run(fixture.Exec("RESET statement_timeout", 0))
				} else {
					if !errors.Is(err, context.DeadlineExceeded) { t.Fatal("native lost caller deadline", err) }
					<-ctx.Done()
					if !errors.Is(ctx.Err(), context.DeadlineExceeded) { t.Fatal("source context terminal error", ctx.Err()) }
					cancel()
				}
				if stmt != nil { if err := stmt.Close(); err != nil { t.Fatal(err) } }
				passwordCheckCredential(t, p, b, "pc_cancel", sql.NullString{String: stored, Valid: true})
				if after := passwordCheckOID(t, p, admin, "pc_cancel"); after != oid { t.Fatal("cancellation changed role identity", oid, after) }
				if b.PG {
					ra(fixture.Select("SELECT rolcanlogin,rolconnlimit FROM pg_authid WHERE rolname='pc_cancel'", []string{"rolcanlogin", "rolconnlimit"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(1)}}))
				}
				run(fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{"pc_cancel", "pc_cancel"}}))
				run(fixture.Exec("ALTER ROLE CURRENT_USER PASSWORD 'role-attributes-fixture'", 0))
				passwordCheckPlainCredential(t, p, b, "pc_cancel", "role-attributes-fixture")
				closed := time.Now()
				if err := c.Close(); err != nil { t.Fatal(err) }
				if time.Since(closed) > fixture.Liveness { t.Fatal("physical close retained canceled work") }
				replacement, err := fixture.RoleAttributesPhysical(t, p, b, "pc_cancel")
				if err != nil { t.Fatal("sole physical slot was not released", err) }
				if err := replacement.Close(); err != nil { t.Fatal(err) }
			})
		})
	}
}

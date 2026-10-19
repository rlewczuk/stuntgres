package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExtensionVersionTransactionsAndAuthority(t *testing.T) {
	t.Run("Transactions", func(t *testing.T) {
		extensionVersionCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE EXTENSION fuzzystrmatch VERSION '1.1'", 0))
			before := extensionVersionInventory(t, p, b, c, "1.1")
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT ev_before", 0))
			run(fixture.Exec("ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'", 0))
			extensionVersionPreserved(t, before, extensionVersionInventory(t, p, b, c, "1.2"))
			extensionVersionDM(t, p, b, c)
			extensionVersionPreserved(t, before, extensionVersionInventory(t, p, b, peer, "1.1"))
			extensionVersionMissing(t, p, b, peer)
			extensionVersionAvailability(t, p, b, c, "1.2")
			extensionVersionAvailability(t, p, b, peer, "1.1")
			run(fixture.Exec("ROLLBACK TO ev_before", 0))
			extensionVersionPreserved(t, before, extensionVersionInventory(t, p, b, c, "1.1"))
			// A deliberate undefined-function error would abort this transaction;
			// inspect absence here and exercise 42883 after full rollback below.
			run(fixture.Select("SELECT count(*) FROM pg_proc WHERE proname='daitch_mokotoff'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Exec("ALTER EXTENSION fuzzystrmatch UPDATE", 0))
			run(fixture.Exec("ROLLBACK", 0))
			extensionVersionPreserved(t, before, extensionVersionInventory(t, p, b, c, "1.1"))
			extensionVersionMissing(t, p, b, c)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'", 0))
			run(extensionError("ALTER EXTENSION fuzzystrmatch UPDATE TO 'missing'", "22023", `extension "fuzzystrmatch" has no update path from version "1.2" to version "missing"`))
			run(extensionError("ALTER EXTENSION fuzzystrmatch UPDATE", "25P02", "current transaction is aborted, commands ignored until end of transaction block"))
			run(fixture.Exec("ROLLBACK", 0))
			extensionVersionPreserved(t, before, extensionVersionInventory(t, p, b, c, "1.1"))
			extensionVersionMissing(t, p, b, c)
			extensionVersionBoth(t, p, b, c, extensionVersionBase())
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("ALTER EXTENSION fuzzystrmatch UPDATE", 0))
			run(fixture.Exec("COMMIT", 0))
			extensionVersionPreserved(t, before, extensionVersionInventory(t, p, b, peer, "1.2"))
			extensionVersionDM(t, p, b, peer)
		})
	})
	t.Run("ReadOnlyPrepare", func(t *testing.T) {
		extensionVersionCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{"ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'", "ALTER EXTENSION fuzzystrmatch UPDATE TO ''"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN READ ONLY", 0))
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("read-only utility Prepare must not execute", err)
				}
				stmt.Close()
				daitchCount(t, p, b, c, 0)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, extensionError(q, "25006", "cannot execute ALTER EXTENSION in a read-only transaction"))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN READ ONLY", 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(extensionError(q, "25006", "cannot execute ALTER EXTENSION in a read-only transaction")))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
			}
		})
	})
	for _, trustedInstall := range []bool{false, true} {
		t.Run(map[bool]string{false: "SuperuserThenOrdinaryUpdater", true: "TrustedOwner"}[trustedInstall], func(t *testing.T) {
			extensionVersionCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				attribute := "SUPERUSER"
				if trustedInstall {
					attribute = "NOSUPERUSER"
				}
				run(fixture.Exec("CREATE USER ev_owner "+attribute, 0))
				run(fixture.Exec("CREATE USER ev_other NOSUPERUSER", 0))
				// The oracle resets public under the fixture owner. Grant actual
				// schema visibility; database CREATE alone does not select it.
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO ev_owner,ev_other", 0))
				var database string
				if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
					t.Fatal(err)
				}
				quoted := `"` + strings.ReplaceAll(database, `"`, `""`) + `"`
				run(fixture.Exec("GRANT CREATE ON DATABASE "+quoted+" TO ev_owner", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION ev_owner", 0))
				run(fixture.Exec("CREATE EXTENSION fuzzystrmatch VERSION '1.1'", 0))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				before := extensionVersionInventory(t, p, b, c, "1.1")
				run(fixture.Exec("SET SESSION AUTHORIZATION ev_other", 0))
				for _, target := range []string{"'1.2'", "''", "'1.1'"} {
					extensionVersionBoth(t, p, b, c, extensionError("ALTER EXTENSION fuzzystrmatch UPDATE TO "+target, "42501", "must be owner of extension fuzzystrmatch"))
				}
				run(fixture.Exec("BEGIN READ ONLY", 0))
				run(extensionError("ALTER EXTENSION fuzzystrmatch UPDATE", "25006", "cannot execute ALTER EXTENSION in a read-only transaction"))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("ALTER USER ev_owner NOSUPERUSER", 0))
				run(fixture.Exec("REVOKE CREATE ON DATABASE "+quoted+" FROM ev_owner", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION ev_owner", 0))
				denied := fixture.ImplicitError("ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'", fixture.Diagnostic{Code: "42501", Message: `permission denied to update extension "fuzzystrmatch"`, Hint: "Must have CREATE privilege on current database to update this extension."})
				extensionVersionBoth(t, p, b, c, denied)
				extensionVersionPreserved(t, before, extensionVersionInventory(t, p, b, c, "1.1"))
				extensionVersionMissing(t, p, b, c)
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("GRANT CREATE ON DATABASE "+quoted+" TO ev_owner", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION ev_owner", 0))
				run(fixture.Exec("ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'", 0))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				after := extensionVersionInventory(t, p, b, c, "1.2")
				extensionVersionPreserved(t, before, after)
				for id, properties := range after.Members {
					if _, old := before.Members[id]; !old && properties[2] != int64(10) {
						t.Fatal("trusted update new member must belong to bootstrap", properties)
					}
				}
				run(fixture.Exec("REVOKE CREATE ON DATABASE "+quoted+" FROM ev_owner", 0))
				var notices []fixture.Diagnostic
				_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("SET SESSION AUTHORIZATION ev_owner", 0))
				for _, q := range []string{"ALTER EXTENSION fuzzystrmatch UPDATE", "ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'"} {
					fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Prepared(fixture.Exec(q, 0)))
				}
				fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `version "1.2" of extension "fuzzystrmatch" is already installed`}
				if !reflect.DeepEqual(notices, []fixture.Diagnostic{want, want}) {
					t.Fatal("same-version must not require database CREATE", notices)
				}
				extensionVersionPreserved(t, after, extensionVersionInventory(t, p, b, c, "1.2"))
				extensionVersionDM(t, p, b, c)
			})
		})
	}
	t.Run("CanceledUpdate", func(t *testing.T) {
		extensionVersionCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION fuzzystrmatch VERSION '1.1'", 0))
			before := extensionVersionInventory(t, p, b, c, "1.1")
			q := "ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'"
			held := extensionVersionHeld(t, p, c, q)
			if !b.PG {
				// pq does not promise native precanceled admission; no fake lock
				// or PostgreSQL SQLSTATE is invented for an ordinary Go context.
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				for _, executor := range []fixture.Executor{c, held} {
					result, err := executor.ExecContext(ctx, q)
					var state interface{ SQLState() string }
					if result != nil || !errors.Is(err, context.Canceled) || errors.As(err, &state) {
						t.Fatal("ordinary canceled update", result, err)
					}
					extensionVersionPreserved(t, before, extensionVersionInventory(t, p, b, c, "1.1"))
					extensionVersionMissing(t, p, b, c)
				}
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
			extensionVersionPreserved(t, before, extensionVersionInventory(t, p, b, c, "1.2"))
			extensionVersionDM(t, p, b, c)
		})
	})
}

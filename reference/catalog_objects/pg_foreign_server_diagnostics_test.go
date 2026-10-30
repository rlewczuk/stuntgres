package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGForeignServerDiagnostics(t *testing.T) {
	pgfsScenario(t, func(c *pgfsCase) {
		c.wrapper()
		c.exec(pgfsSeed, "CREATE SERVER pgfs_other FOREIGN DATA WRAPPER pgfs_wrapper", "CREATE TABLE public.pgfs_sentinel(v integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO public.pgfs_sentinel VALUES(91)", 1))
		oid, wrapper := c.oid("pgfs_server"), c.wrapperOID()
		otherOID := c.oid("pgfs_other")
		before := [][]any{c.row("pgfs_server", oid, c.owner, wrapper, "", "18", nil, "{password=private=value,empty=,remote=é=🙂}")}
		held := c.hold(c.conn, pgfsQuery)
		unchanged := func() {
			c.raw(held, "pgfs_server", before)
			c.raw(held, "pgfs_bad", nil)
			c.raw(held, "pgfs_other", [][]any{c.row("pgfs_other", otherOID, c.owner, wrapper, nil, nil, nil, nil)})
			c.both(c.conn, fixture.Select("SELECT v FROM public.pgfs_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
		}
		for _, vector := range []struct{ query, code, message string }{
			{"CREATE SERVER pgfs_server FOREIGN DATA WRAPPER pgfs_wrapper", "42710", `server "pgfs_server" already exists`},
			{"ALTER SERVER pgfs_other RENAME TO pgfs_server", "42710", `server "pgfs_server" already exists`},
			{"ALTER SERVER pgfs_missing VERSION 'missing'", "42704", `server "pgfs_missing" does not exist`},
			{"CREATE SERVER pgfs_bad FOREIGN DATA WRAPPER pgfs_missing", "42704", `foreign-data wrapper "pgfs_missing" does not exist`},
			{"CREATE SERVER pgfs_bad FOREIGN DATA WRAPPER pgfs_wrapper OPTIONS(password 'one',password 'two')", "42710", `option "password" provided more than once`},
			{"ALTER SERVER pgfs_server VERSION 'wrong' OPTIONS(SET password 'wrong',ADD password 'duplicate')", "42710", `option "password" provided more than once`},
			{"ALTER SERVER pgfs_server VERSION 'wrong' OPTIONS(SET password 'wrong',SET absent 'missing')", "42704", `option "absent" not found`},
			{"ALTER SERVER pgfs_server VERSION 'wrong' OPTIONS(SET password 'wrong',DROP absent)", "42704", `option "absent" not found`},
			{`ALTER SERVER pgfs_server VERSION 'wrong' OPTIONS(SET password 'wrong',ADD "x=y" 'invalid')`, "22023", `invalid option name "x=y": must not contain "="`},
		} {
			for _, prepared := range []bool{false, true} {
				c.error(c.conn, vector.query, vector.code, vector.message, prepared)
				unchanged()
			}
			mutation := c.hold(c.conn, vector.query)
			c.error(mutation, vector.query, vector.code, vector.message, false)
			unchanged()
		}
		c.exec("SET search_path TO public", "CREATE FOREIGN TABLE public.pgfs_table(id integer) SERVER pgfs_server")
		const restrict = "DROP SERVER pgfs_server RESTRICT"
		step := fixture.ErrorDiagnostic(restrict, fixture.PgDependRestrict("server pgfs_server", "foreign table pgfs_table depends on server pgfs_server"))
		step.ErrorStage = "execute"
		c.both(c.conn, step)
		c.run(c.hold(c.conn, restrict), step)
		unchanged()
		c.both(c.conn, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='pgfs_table'", []string{"relname"}, []string{"NAME"}, [][]any{{"pgfs_table"}}))
		c.exec("DROP SERVER pgfs_server CASCADE")
		c.raw(held, "pgfs_server", nil)
		c.both(c.conn, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='pgfs_table'", []string{"relname"}, []string{"NAME"}, nil))
		c.exec(pgfsSeed)
		oid = c.oid("pgfs_server")
		before[0][0] = oid
		if !c.b.PG {
			ctx, cancel := context.WithCancel(c.p.Ctx)
			cancel()
			for _, executor := range []fixture.Executor{c.conn, held} {
				rows, err := executor.QueryContext(ctx, pgfsQuery, "pgfs_server")
				if rows != nil { rows.Close(); c.t.Fatal("precanceled rows") }
				var state interface{ SQLState() string }
				if !errors.Is(err, context.Canceled) || errors.As(err, &state) { c.t.Fatal("ordinary cancellation", err) }
			}
			unchanged()
		}
		pgfsOwnerDiagnostics(c, held, oid, wrapper)
		pgfsNotices(c)
		c.both(c.conn, fixture.Select("SELECT v FROM public.pgfs_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
	})
}

func pgfsOwnerDiagnostics(c *pgfsCase, held fixture.HeldStatement, oid, wrapper int64) {
	c.t.Helper()
	c.exec("CREATE ROLE pgfs_owner NOSUPERUSER", "CREATE ROLE pgfs_newowner NOSUPERUSER", "GRANT USAGE ON FOREIGN DATA WRAPPER pgfs_wrapper TO pgfs_owner", "ALTER SERVER pgfs_server OWNER TO pgfs_owner")
	var owner, recipient int64
	if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='pgfs_owner'").Scan(&owner); err != nil { c.t.Fatal(err) }
	if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='pgfs_newowner'").Scan(&recipient); err != nil { c.t.Fatal(err) }
	before := [][]any{c.row("pgfs_server", oid, owner, wrapper, "", "18", nil, "{password=private=value,empty=,remote=é=🙂}")}
	const transfer = "ALTER SERVER pgfs_server OWNER TO pgfs_newowner"
	ownerHeld := c.hold(c.conn, transfer)
	c.exec("SET ROLE pgfs_owner")
	for _, prepared := range []bool{false, true} {
		c.error(c.conn, transfer, "42501", `must be able to SET ROLE "pgfs_newowner"`, prepared)
		c.raw(held, "pgfs_server", before)
	}
	c.error(ownerHeld, transfer, "42501", `must be able to SET ROLE "pgfs_newowner"`, false)
	c.exec("RESET ROLE", "GRANT pgfs_newowner TO pgfs_owner WITH SET TRUE", "SET ROLE pgfs_owner")
	for _, prepared := range []bool{false, true} {
		c.error(c.conn, transfer, "42501", "permission denied for foreign-data wrapper pgfs_wrapper", prepared)
		c.raw(held, "pgfs_server", before)
	}
	c.error(ownerHeld, transfer, "42501", "permission denied for foreign-data wrapper pgfs_wrapper", false)
	c.raw(held, "pgfs_server", before)
	c.exec("RESET ROLE", "GRANT USAGE ON FOREIGN DATA WRAPPER pgfs_wrapper TO pgfs_newowner", "SET ROLE pgfs_owner")
	c.run(ownerHeld, fixture.Exec(transfer, 0))
	c.exec("RESET ROLE")
	c.identity("pgfs_server", oid, recipient, wrapper)
	c.raw(held, "pgfs_server", [][]any{c.row("pgfs_server", oid, recipient, wrapper, "", "18", nil, "{password=private=value,empty=,remote=é=🙂}")})
}

func pgfsNotices(c *pgfsCase) {
	c.t.Helper()
	var notices []fixture.Diagnostic
	_, conn, recorder := fixture.NoticeReferencePool(c.t, c.p, c.b.PG, &notices)
	defer conn.Close()
	for _, vector := range []struct{ query, code, message string }{
		{"CREATE SERVER IF NOT EXISTS pgfs_server FOREIGN DATA WRAPPER pgfs_wrapper OPTIONS(password 'must-not-replace')", "42710", `server "pgfs_server" already exists, skipping`},
		{"DROP SERVER IF EXISTS pgfs_missing", "00000", `server "pgfs_missing" does not exist, skipping`},
	} {
		notices = nil
		fixture.RunStep(c.t, c.p.Ctx, conn, recorder, fixture.Exec(vector.query, 0))
		if len(notices) != 1 { c.t.Fatal("notice count", notices) }
		want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: vector.code, Message: vector.message}
		if notices[0] != want { c.t.Fatalf("notice = %#v, want %#v", notices[0], want) }
	}
	c.options(c.conn, "pgfs_server", []string{"password=private=value", "empty=", "remote=é=🙂"})
}

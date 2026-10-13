package reference_test

import (
	"database/sql"
	"fmt"
	"path"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTablespacePathNamePrecedence(t *testing.T) {
	tablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, location := range []string{"", " ", "relative", "a/../b", "\\absolute"} {
			for _, name := range []string{"ts_contract_path", "pg_default"} {
				tablespaceError(t, p, b, c, tablespaceSQL(name, "", location), tablespaceDiagnostic("42P17", "tablespace location must be an absolute path"), false)
			}
		}
		for _, location := range []string{"/bad'quote", "bad'quote"} {
			tablespaceError(t, p, b, c, tablespaceSQL("pg_default", "", location), tablespaceDiagnostic("42602", "tablespace location cannot contain single quotes"), false)
		}
		for _, name := range []string{"pg_default", "pg_global", "pg_custom"} {
			want := tablespaceDiagnostic("42939", fmt.Sprintf("unacceptable tablespace name %q", name))
			want.Detail = "The prefix \"pg_\" is reserved for system tablespaces."
			tablespaceError(t, p, b, c, tablespaceSQL(name, "", tablespaceAbsent), want, false)
		}
		for _, location := range []string{tablespaceAbsent, "/tmp//stuntgres_tablespace_contract_absent/./other/../location/", "/tmp/bad'quote/../stuntgres_tablespace_contract_absent/location", tablespaceAbsent + "\\leaf"} {
			tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_path", "", location), tablespacePhysical(b, path.Clean(location)), false)
		}
		for _, name := range []string{"\"PG_custom\"", "\"ts_contract_line\nname\"", "\"ts_contract_line\rname\""} {
			tablespaceError(t, p, b, c, tablespaceSQL(name, "", tablespaceAbsent), tablespacePhysical(b, tablespaceAbsent), false)
		}
		// REL_18_0 reserves 54 bytes: version-directory length 15, five
		// separators, three 10-byte OIDs and a four-byte fork name.
		// Short components avoid ENAMETOOLONG before the guaranteed absent root.
		for _, size := range []int{960, 961, 970, 971} {
			t.Run(fmt.Sprintf("canonical-bytes-%d", size), func(t *testing.T) {
				location := "/tmp/stuntgres_tablespace_contract_absent/" + strings.Repeat("abcdefgh/", 100)
				location += strings.Repeat("z", size-len(location))
				want := tablespacePhysical(b, location)
				if size > 970 {
					want = tablespaceDiagnostic("42P17", "tablespace location \""+location+"\" is too long")
				}
				tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_length", "", location), want, false)
			})
		}
		for _, size := range []int{970, 971} {
			t.Run(fmt.Sprintf("canonical-utf8-bytes-%d", size), func(t *testing.T) {
				location := "/tmp/stuntgres_tablespace_contract_absent/" + strings.Repeat("é/", 300)
				remaining := size - len(location)
				location += strings.Repeat("é", remaining/2) + strings.Repeat("z", remaining%2)
				want := tablespacePhysical(b, location)
				if size > 970 {
					want = tablespaceDiagnostic("42P17", "tablespace location \""+location+"\" is too long")
				}
				tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_bytes", "", location), want, false)
			})
		}
		for _, suffix := range []string{" WITH (seq_page_cost=1)", " WITH (unknown_option=1)"} {
			tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_options", "ts_contract_missing", "relative")+suffix, tablespaceDiagnostic("42704", "role \"ts_contract_missing\" does not exist"), false)
			tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_options", "", "relative")+suffix, tablespaceDiagnostic("42P17", "tablespace location must be an absolute path"), false)
		}
		tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_options", "", tablespaceAbsent)+" WITH (seq_page_cost=1)", tablespacePhysical(b, tablespaceAbsent), false)
		tablespaceNoUserRows(t, p, b, c)
	})
}

func TestReferenceTablespaceOwnerAuthority(t *testing.T) {
	tablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		tablespaceRole(t, p, b, c, "ts_contract_reader", "LOGIN CREATEDB CREATEROLE")
		tablespaceRole(t, p, b, c, "ts_contract_super", "SUPERUSER")
		tablespaceExec(t, p, b, c, "GRANT ts_contract_super TO ts_contract_reader WITH INHERIT TRUE, SET TRUE")
		for _, owner := range []string{"ts_contract_missing", "public", "\"current_user\"", "\"current_role\"", "\"session_user\""} {
			name := strings.Trim(owner, "\"")
			tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_owner", owner, "relative"), tablespaceDiagnostic("42704", fmt.Sprintf("role %q does not exist", name)), false)
		}
		for _, owner := range []string{"", "CURRENT_USER", "CURRENT_ROLE", "SESSION_USER", "ts_contract_reader"} {
			tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_owner", owner, tablespaceAbsent), tablespacePhysical(b, tablespaceAbsent), false)
		}
		for _, owner := range []string{"none", "\"none\""} {
			q := tablespaceSQL("ts_contract_owner", owner, "relative")
			want := tablespaceDiagnostic("42939", "role name \"none\" is reserved")
			want.Position = fixture.Position(strings.Index(q, owner) + 1)
			tablespaceError(t, p, b, c, q, want, true)
		}
		q := tablespaceSQL("ts_contract_owner", "ts_contract_missing", "relative")
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal("utility prepare must defer authority:", err)
		}
		defer stmt.Close()
		tablespaceExec(t, p, b, c, "SET ROLE ts_contract_reader")
		want := tablespaceDiagnostic("42501", "permission denied to create tablespace \"ts_contract_owner\"")
		want.Hint = "Must be superuser to create a tablespace."
		tablespaceError(t, p, b, c, q, want, false)
		_, err = stmt.ExecContext(p.Ctx)
		fixture.AssertDiagnostic(t, err, &want)
		tablespaceBuiltins(t, p, b, c)
		tablespaceExec(t, p, b, c, "RESET ROLE")
		want = tablespaceDiagnostic("42704", "role \"ts_contract_missing\" does not exist")
		_, err = stmt.ExecContext(p.Ctx)
		fixture.AssertDiagnostic(t, err, &want)
		stmt.Close()
		// SESSION_USER remains the ordinary authenticated identity while CURRENT_USER is superuser.
		tablespaceExec(t, p, b, c, "SET SESSION AUTHORIZATION ts_contract_reader")
		tablespaceExec(t, p, b, c, "SET ROLE ts_contract_super")
		for _, owner := range []string{"CURRENT_USER", "CURRENT_ROLE", "SESSION_USER"} {
			tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_owner", owner, tablespaceAbsent), tablespacePhysical(b, tablespaceAbsent), false)
		}
		liveSQL := tablespaceSQL("ts_contract_live", "SESSION_USER", tablespaceAbsent)
		live, err := c.PrepareContext(p.Ctx, liveSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer live.Close()
		tablespaceExec(t, p, b, b.DB, "ALTER ROLE ts_contract_super NOSUPERUSER")
		_, err = live.ExecContext(p.Ctx)
		denied := tablespaceDiagnostic("42501", "permission denied to create tablespace \"ts_contract_live\"")
		denied.Hint = "Must be superuser to create a tablespace."
		fixture.AssertDiagnostic(t, err, &denied)
		live.Close()
		tablespaceExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		tablespaceNoUserRows(t, p, b, c)
	})
}

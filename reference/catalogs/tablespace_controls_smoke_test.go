package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTablespaceControls(t *testing.T) {
	tablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		tablespaceExec(t, p, b, c, "CREATE TABLE ts_contract_control_sentinel(a INT)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ts_contract_control_sentinel VALUES(81)", 1))
		tablespaceBuiltins(t, p, b, c)
		tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_control", "", tablespaceAbsent), tablespacePhysical(b, tablespaceAbsent), false)
		check := func() {
			t.Helper()
			tablespaceBuiltins(t, p, b, c)
			tablespaceNoUserRows(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT a FROM ts_contract_control_sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(81)}}))
		}
		check()
		if !b.PG {
			var dump strings.Builder
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "CREATE TABLESPACE") || strings.Contains(dump.String(), tablespaceAbsent) {
				t.Fatal("Dump published physical tablespace state")
			}
			retained, err := c.QueryContext(p.Ctx, "SELECT oid,spcname,spcowner FROM pg_tablespace ORDER BY oid")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+";"+tablespaceSQL("ts_contract_restore", "", tablespaceAbsent)+";")); err == nil {
				t.Fatal("Restore accepted physical creation")
			}
			check()
			var after strings.Builder
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if after.String() != dump.String() {
				t.Fatal("failed Restore changed logical fixture")
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			tablespaceBuiltins(t, p, b, c)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			check()
			for _, want := range []struct {
				oid  int64
				name string
			}{{1663, "pg_default"}, {1664, "pg_global"}} {
				if !retained.Next() {
					t.Fatal("lost retained builtin row", retained.Err())
				}
				var oid, owner int64
				var name string
				if err := retained.Scan(&oid, &name, &owner); err != nil {
					t.Fatal(err)
				}
				if oid != want.oid || name != want.name || owner != 10 {
					t.Fatal("retained builtin identity changed", oid, name, owner)
				}
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("unexpected retained rows", retained.Err())
			}
			retained.Close()
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if _, err := c.ExecContext(canceled, tablespaceSQL("ts_contract_canceled", "", tablespaceAbsent)); !errors.Is(err, context.Canceled) {
				t.Fatal("canceled create", err)
			}
			check()
		}
	})
}

func TestReferenceTablespaceFeatureSmoke(t *testing.T) {
	tablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		tablespaceBuiltins(t, p, b, c)
		tablespaceExec(t, p, b, c, "CREATE TABLE ts_contract_smoke_sentinel(a INT)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ts_contract_smoke_sentinel VALUES(19)", 1))
		tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_smoke", "", "relative"), tablespaceDiagnostic("42P17", "tablespace location must be an absolute path"), false)
		reserved := tablespaceDiagnostic("42939", "unacceptable tablespace name \"pg_default\"")
		reserved.Detail = "The prefix \"pg_\" is reserved for system tablespaces."
		tablespaceError(t, p, b, c, tablespaceSQL("pg_default", "", tablespaceAbsent), reserved, false)
		tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_smoke", "ts_contract_missing", "relative"), tablespaceDiagnostic("42704", "role \"ts_contract_missing\" does not exist"), false)
		tablespaceRole(t, p, b, c, "ts_contract_smoke_reader", "")
		tablespaceExec(t, p, b, c, "SET ROLE ts_contract_smoke_reader")
		denied := tablespaceDiagnostic("42501", "permission denied to create tablespace \"ts_contract_smoke\"")
		denied.Hint = "Must be superuser to create a tablespace."
		tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_smoke", "ts_contract_missing", "relative"), denied, false)
		tablespaceTransactionChecks(t, p, b, c)
		tablespaceExec(t, p, b, c, "RESET ROLE")
		for range 2 {
			tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_smoke", "", tablespaceAbsent), tablespacePhysical(b, tablespaceAbsent), false)
		}
		tablespaceNoUserRows(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT a FROM ts_contract_smoke_sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(19)}}))
	})
}

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
			retained, err := c.QueryContext(p.Ctx, "SELECT oid,spcname,spcowner,spcacl,spcoptions FROM pg_tablespace ORDER BY oid")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			retainedTypes, err := retained.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			tablespaceMetadata(t, retainedTypes, false)
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
				var acl, options any
				if err := retained.Scan(&oid, &name, &owner, &acl, &options); err != nil {
					t.Fatal(err)
				}
				if oid != want.oid || name != want.name || owner != 10 || acl != nil || options != nil {
					t.Fatal("retained builtin metadata changed", oid, name, owner, acl, options)
				}
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("unexpected retained rows", retained.Err())
			}
			retained.Close()
			tablespaceMetadata(t, retainedTypes, false)
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
		tablespaceHeldReads(t, p, b, c)
		pgAttributeCheck(t, p, b, c, fixture.Select("SELECT attnum,atttypid,attnotnull,attcollation FROM pg_attribute WHERE attrelid=1213 AND attname='spcacl'", []string{"attnum", "atttypid", "attnotnull", "attcollation"}, []string{"INT2", "OID", "BOOL", "OID"}, [][]any{{int64(4), int64(1034), false, int64(0)}}))
		tablespaceExec(t, p, b, c, "CREATE TABLE ts_contract_smoke_sentinel(a INT)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ts_contract_smoke_sentinel VALUES(19)", 1))
		tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_smoke", "", "relative"), tablespaceDiagnostic("42P17", "tablespace location must be an absolute path"), false)
		reserved := tablespaceDiagnostic("42939", "unacceptable tablespace name \"pg_default\"")
		reserved.Detail = "The prefix \"pg_\" is reserved for system tablespaces."
		tablespaceError(t, p, b, c, tablespaceSQL("pg_default", "", tablespaceAbsent), reserved, false)
		tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_smoke", "ts_contract_missing", "relative"), tablespaceDiagnostic("42704", "role \"ts_contract_missing\" does not exist"), false)
		tablespaceRole(t, p, b, c, "ts_contract_smoke_reader", "")
		tablespaceExec(t, p, b, c, "SET ROLE ts_contract_smoke_reader")
		tablespaceBuiltins(t, p, b, c)
		denied := tablespaceDiagnostic("42501", "permission denied to create tablespace \"ts_contract_smoke\"")
		denied.Hint = "Must be superuser to create a tablespace."
		tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_smoke", "ts_contract_missing", "relative"), denied, false)
		tablespaceTransactionChecks(t, p, b, c)
		tablespaceExec(t, p, b, c, "RESET ROLE")
		for range 2 {
			tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_smoke", "", tablespaceAbsent), tablespacePhysical(b, tablespaceAbsent), false)
		}
		if !b.PG {
			tablespaceError(t, p, b, c, "DELETE FROM pg_catalog.pg_tablespace WHERE oid=1663", tablespaceDiagnostic("0A000", "catalog source mutation is unsupported"), false)
			tablespaceBuiltins(t, p, b, c)
		}
		tablespaceNoUserRows(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT a FROM ts_contract_smoke_sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(19)}}))
	})
}

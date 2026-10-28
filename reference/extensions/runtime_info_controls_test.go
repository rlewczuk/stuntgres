package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func runtimeControlState(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, state string) {
	t.Helper()
	rows := runtimeControlACLRows(state)
	if b.PG && state == "seed" { rows = [][]any{{int64(3800), "{stuntgres=X/stuntgres,pg_monitor=X/stuntgres}"}, {int64(3801), "{stuntgres=X/stuntgres,pg_monitor=X/stuntgres}"}} }
	runtimeSelect(t, p, b, c, "SELECT oid::bigint AS oid, proacl::text AS acl FROM pg_proc WHERE oid IN (3800,3801) ORDER BY oid", []string{"oid", "acl"}, []string{"INT8", "TEXT"}, rows)
	loadExec(t, p, b, c, "SET ROLE runtime_restore_reader")
	if state == "explicit" {
		runtimeSelect(t, p, b, c, "SELECT pg_current_logfile(NULL::text) AS logfile", []string{"logfile"}, []string{"TEXT"}, [][]any{{nil}})
	} else if state == "null" {
		runtimeSelect(t, p, b, c, "SELECT pg_current_logfile() AS logfile", []string{"logfile"}, []string{"TEXT"}, [][]any{{nil}})
		runtimeSelect(t, p, b, c, "SELECT pg_current_logfile(NULL::text) AS logfile", []string{"logfile"}, []string{"TEXT"}, [][]any{{nil}})
	} else {
		loadError(t, p, b, c, "SELECT pg_current_logfile(NULL::text)", runtimeDenied("pg_current_logfile"), false)
	}
	if state != "null" { loadError(t, p, b, c, "SELECT pg_current_logfile()", runtimeDenied("pg_current_logfile"), false) }
	loadExec(t, p, b, c, "RESET ROLE")
	loadExec(t, p, b, c, "SET ROLE runtime_restore_monitor")
	if state == "seed" || state == "null" {
		runtimeSelect(t, p, b, c, "SELECT pg_current_logfile() AS logfile", []string{"logfile"}, []string{"TEXT"}, [][]any{{nil}})
		runtimeSelect(t, p, b, c, "SELECT pg_current_logfile(NULL::text) AS logfile", []string{"logfile"}, []string{"TEXT"}, [][]any{{nil}})
	} else {
		loadError(t, p, b, c, "SELECT pg_current_logfile(NULL::text)", runtimeDenied("pg_current_logfile"), false)
		loadError(t, p, b, c, "SELECT pg_current_logfile()", runtimeDenied("pg_current_logfile"), false)
	}
	loadExec(t, p, b, c, "RESET ROLE")
	loadExec(t, p, b, c, "SET jit = off")
	runtimeSelect(t, p, b, c, "SELECT pg_jit_available() AS jit, pg_numa_available() AS numa", []string{"jit", "numa"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}})
	loadExec(t, p, b, c, "RESET jit")
	runtimeSelect(t, p, b, c, "SELECT * FROM pg_get_loaded_modules() WHERE module_name = 'runtime_contract_absent'", []string{"module_name", "version", "file_name"}, []string{"TEXT", "TEXT", "TEXT"}, nil)
}

// Raw ACL spelling uses the fixture's actual bootstrap owner, supplied below;
// no expected role/OID is obtained from the ACL being asserted.
func runtimeControlACLRows(state string) [][]any {
	var zero, text any
	switch state {
	case "seed": zero = "{postgres=X/postgres,pg_monitor=X/postgres}"; text = zero
	case "empty": zero, text = "{}", "{}"
	case "null": zero, text = nil, nil
	case "explicit": zero = "{postgres=X/postgres}"; text = "{postgres=X/postgres,runtime_restore_reader=X/postgres}"
	}
	return [][]any{{int64(3800), zero}, {int64(3801), text}}
}

func TestReferenceRuntimeInfoControls(t *testing.T) {
	for _, state := range []string{"seed", "explicit", "empty", "null"} {
		t.Run(state, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"runtime_restore_reader", "runtime_restore_monitor"})
			p := fixture.NewScenario(t)
			var snapshot []byte
			// Exact seed authority needs no repair, so a genuine dump can be empty.
			captured := false
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer c.Close()
					defer c.ExecContext(p.Ctx, "RESET ROLE")
					for _, query := range []string{"CREATE ROLE runtime_restore_reader", "CREATE ROLE runtime_restore_monitor", "GRANT pg_monitor TO runtime_restore_monitor"} { loadExec(t, p, b, c, query) }
					if b.PG {
						var original0, original1 string
						if err := c.QueryRowContext(p.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=3800").Scan(&original0); err != nil { t.Fatal(err) }
						if err := c.QueryRowContext(p.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=3801").Scan(&original1); err != nil { t.Fatal(err) }
						defer func() {
							c.ExecContext(p.Ctx, "RESET ROLE")
							c.ExecContext(p.Ctx, "UPDATE pg_proc SET proacl=$1::aclitem[] WHERE oid=3800", original0)
							c.ExecContext(p.Ctx, "UPDATE pg_proc SET proacl=$1::aclitem[] WHERE oid=3801", original1)
						}()
					}
					defer func() {
						c.ExecContext(p.Ctx, "RESET ROLE")
						c.ExecContext(p.Ctx, "REVOKE EXECUTE ON FUNCTION pg_current_logfile(), pg_current_logfile(text) FROM runtime_restore_reader")
						c.ExecContext(p.Ctx, "GRANT EXECUTE ON FUNCTION pg_current_logfile(), pg_current_logfile(text) TO pg_monitor")
						c.ExecContext(p.Ctx, "REVOKE EXECUTE ON FUNCTION pg_current_logfile(), pg_current_logfile(text) FROM PUBLIC")
					}()
					if !b.PG {
						if state != "seed" { loadExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_current_logfile(), pg_current_logfile(text) FROM pg_monitor") }
						if state == "explicit" { loadExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_current_logfile(text) TO runtime_restore_reader") }
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
						snapshot = bytes.Clone(dump.Bytes())
						captured = true
						if state == "empty" || state == "null" {
							lines := strings.Split(string(snapshot), "\n"); count := 0
							for i, line := range lines {
								if !strings.HasPrefix(line, "UPDATE pg_catalog.pg_proc SET proacl=") || !strings.Contains(line, "pg_current_logfile") { continue }
								_, locator, ok := strings.Cut(line, " WHERE "); if !ok { t.Fatal(line) }
								value := `pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[]`
								if state == "null" { value = "NULL::pg_catalog.aclitem[]" }
								lines[i] = "UPDATE pg_catalog.pg_proc SET proacl="+value+" WHERE "+locator; count++
							}
							if count != 2 { t.Fatal("expected both genuine logfile repair locators", count) }
							snapshot = []byte(strings.Join(lines, "\n"))
						}
						retained, err := c.QueryContext(p.Ctx, "SELECT * FROM pg_get_loaded_modules()")
						if err != nil { t.Fatal(err) }
						defer retained.Close()
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot)); err != nil { t.Fatal(err) }
						metadata, err := retained.ColumnTypes()
						if err != nil || len(metadata) != 3 { t.Fatal("retained module shape", metadata, err) }
						for i, name := range []string{"module_name", "version", "file_name"} {
							if metadata[i].Name() != name || metadata[i].DatabaseTypeName() != "TEXT" { t.Fatal("retained module member", metadata[i]) }
						}
						if retained.Next() || retained.Err() != nil { t.Fatal("retained absence source changed", retained.Err()) }
						retained.Close()
						runtimeControlState(t, p, b, c, state)
						// Dump the restored state again: raw NULL/empty must survive
						// without the destination bootstrap seed replacing it.
						dump.Reset(); if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }; snapshot = bytes.Clone(dump.Bytes())
						driver := &stuntgres.Driver{}
						connector, err := driver.OpenConnector("runtime_restore_fresh"); if err != nil { t.Fatal(err) }
						control := sql.OpenDB(connector); defer control.Close()
						for _, query := range []string{"CREATE ROLE runtime_restore_reader", "CREATE ROLE runtime_restore_monitor", "GRANT pg_monitor TO runtime_restore_monitor"} { if _, err := control.ExecContext(p.Ctx, query); err != nil { t.Fatal(err) } }
						if err := stuntgres.Restore(p.Ctx, control, bytes.NewReader(snapshot)); err != nil { t.Fatal(err) }
						pool, recorder := fixture.RecordedNativePool(t, connector)
						fresh, err := pool.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer fresh.Close()
						runtimeControlState(t, p, fixture.Backend{Name: "fresh", DB: pool, Recorder: recorder}, fresh, state)
					} else {
						if !captured { t.Fatal("missing genuine native dump") }
						for line := range strings.SplitSeq(string(snapshot), "\n") {
							if strings.HasPrefix(line, "UPDATE pg_catalog.pg_proc SET proacl=") && strings.Contains(line, "pg_current_logfile") {
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(line, 1))
							}
						}
						runtimeControlState(t, p, b, c, state)
					}
				})
			}
		})
	}
}

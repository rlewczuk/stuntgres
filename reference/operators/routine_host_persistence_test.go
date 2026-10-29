package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func hostOpenMatching(t *testing.T, runtime *hostNativeRuntime) (*sql.DB, fixture.Backend) {
	t.Helper()
	driver := stuntgres.NewDriver()
	if err := registerHostNative(driver, runtime); err != nil {
		t.Fatal(err)
	}
	connector, err := driver.OpenConnector("host-restore-target")
	if err != nil {
		t.Fatal(err)
	}
	return hostNativePools(t, connector)
}
func hostPersistenceResults(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	hostBag(t, p, b, c, "host_persist_inc('a=>7,b=>NULL'::hstore)", [][]any{{"a", "8"}, {"b", nil}, {"c", "3"}})
	hostBag(t, p, b, c, "host_persist_double('a=>7,b=>NULL'::hstore)", [][]any{{"a", "14"}, {"b", nil}, {"d", "second"}})
	hostBag(t, p, b, c, "(SELECT v FROM host_persist_data)", [][]any{{"a", "2"}, {"b", nil}, {"c", "3"}})
	hostBag(t, p, b, c, "(SELECT v FROM host_persist_view)", [][]any{{"a", "4"}, {"b", nil}, {"c", "3"}, {"d", "second"}})
	hostMetadata(t, p, b, c, "SELECT host_persist_inc('a=>7'::hstore) AS v")
}
func TestReferenceRoutineHostPersistence(t *testing.T) {
	hostNativeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, native *hostNativeRuntime) {
		hostFunction(t, p, b, c, "host_persist_inc", "increment-v1", hostIncrementPython, "", true)
		hostFunction(t, p, b, c, "host_persist_double", "double-v1", hostDoublePython, "", true)
		hostExec(t, p, b, c, "CREATE TABLE host_persist_data(v hstore DEFAULT host_persist_inc('a=>1,b=>NULL'::hstore))")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO host_persist_data DEFAULT VALUES", 1))
		hostExec(t, p, b, c, "CREATE VIEW host_persist_view AS SELECT host_persist_double(v) AS v FROM host_persist_data")
		hostPersistenceResults(t, p, b, c)
		if b.PG {
			return
		}
		// Dump preserves role references, not cluster roles. Provision every
		// independent destination and the source used by Reset/Restore below.
		hostExec(t, p, b, c, "CREATE ROLE host_persist_reader")
		hostReplayAbsenceControls(t, p, b)
		var stream bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &stream); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stream.String(), "stuntgres-host-runtime-v1 ") {
			t.Fatal("logical host prerequisites missing")
		}
		for round := range 2 {
			fresh := new(hostNativeRuntime)
			db, destination := hostOpenMatching(t, fresh)
			hostExec(t, p, destination, destination.DB, "CREATE ROLE host_persist_reader")
			if err := stuntgres.Restore(p.Ctx, db, bytes.NewReader(stream.Bytes())); err != nil {
				t.Fatalf("fresh driver replay round %d: %v", round, err)
			}
			target, err := destination.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			hostPersistenceResults(t, p, destination, target)
			q := "SELECT key,value FROM each(host_persist_inc($1::hstore)) ORDER BY key"
			stmt, err := target.PrepareContext(p.Ctx, q)
			if err != nil {
				target.Close()
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: target, Statement: stmt, SQL: q}
			for _, input := range []struct{ value, a string }{{"a=>1,b=>NULL", "2"}, {"a=>7,b=>NULL", "8"}} {
				hostRows(t, p, destination, held, q, []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"a", input.a}, {"b", nil}, {"c", "3"}}, input.value)
			}
			hostExec(t, p, destination, target, "REVOKE ALL ON FUNCTION host_persist_inc(hstore) FROM host_persist_reader")
			hostExec(t, p, destination, target, "REVOKE EXECUTE ON FUNCTION host_persist_inc(hstore) FROM PUBLIC")
			hostExec(t, p, destination, target, "SET ROLE host_persist_reader")
			rows, err := stmt.QueryContext(p.Ctx, "a=>1")
			if rows != nil {
				rows.Close()
			}
			fixture.AssertDiagnostic(t, err, new(fixture.ControlsReferenceDiagnostic("42501", "permission denied for function host_persist_inc")))
			hostExec(t, p, destination, target, "RESET ROLE")
			hostExec(t, p, destination, target, "GRANT EXECUTE ON FUNCTION host_persist_inc(hstore) TO host_persist_reader")
			hostExec(t, p, destination, target, "SET ROLE host_persist_reader")
			hostRows(t, p, destination, held, q, []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "8"}, {"c", "3"}}, "a=>7")
			hostExec(t, p, destination, target, "RESET ROLE")
			stmt.Close()
			target.Close()
			var next bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, db, &next); err != nil {
				t.Fatal(err)
			}
			// Replay the current ACL, including the reader's explicit grant.
			if round == 0 {
				stream.Reset()
				stream.Write(next.Bytes())
			}
		}
		for _, mismatch := range []string{"absent", "abi", "program", "type", "direction", "bootstrap", "corrupt", "canceled"} {
			t.Run(mismatch, func(t *testing.T) {
				var definition *stuntgres.HostLanguageDefinition
				d := native.definition()
				switch mismatch {
				case "abi":
					d.ABIVersion = "incompatible"
				case "program":
					d.Programs[0].Version = "incompatible"
				case "type":
					d.Programs[0].Inputs[0] = stuntgres.HostText
					d.Programs[0].InputConversions[0] = ""
				case "direction":
					d.Conversions[0].FromSQL = nil
					d.Conversions[0].FromName = stuntgres.HostName{}
				case "bootstrap":
					d.Conversions[0].Bootstrap = false
					for i := range d.Programs {
						for j := range d.Programs[i].InputConversions {
							d.Programs[i].InputConversions[j] = ""
						}
						d.Programs[i].ResultConversion = ""
					}
				}
				if mismatch != "absent" {
					definition = &d
				}
				driver := stuntgres.NewDriver()
				if definition != nil {
					if err := driver.RegisterHostLanguage(*definition); err != nil {
						t.Fatal(err)
					}
				}
				for _, direction := range []string{"from", "to", "none", "unavailable"} {
					if err := driver.RegisterHostLanguage(native.directionDefinition(direction)); err != nil {
						t.Fatal(err)
					}
				}
				connector, err := driver.OpenConnector("host-mismatch")
				if err != nil {
					t.Fatal(err)
				}
				targetDB, destination := hostNativePools(t, connector)
				target, err := destination.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer target.Close()
				hostExec(t, p, destination, target, "CREATE ROLE host_persist_reader")
				hostExec(t, p, destination, target, "CREATE TABLE host_restore_sentinel(v integer)")
				fixture.RunStep(t, p.Ctx, target, destination.Recorder, fixture.Exec("INSERT INTO host_restore_sentinel VALUES (71)", 1))
				stmt, err := target.PrepareContext(p.Ctx, "SELECT v FROM host_restore_sentinel")
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				data := stream.Bytes()
				if mismatch == "corrupt" {
					data = bytes.Replace(data, []byte("stuntgres-host-runtime-v1 "), []byte("stuntgres-host-runtime-v1 !"), 1)
				}
				ctx := p.Ctx
				if mismatch == "canceled" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
				if err := stuntgres.Restore(ctx, targetDB, bytes.NewReader(data)); err == nil {
					t.Fatal("incompatible restore replaced target")
				}
				var got int
				if err := stmt.QueryRowContext(p.Ctx).Scan(&got); err != nil || got != 71 {
					t.Fatalf("same held sentinel=%d/%v", got, err)
				}
			})
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(stream.Bytes())); err != nil {
			t.Fatal(err)
		}
		hostPersistenceResults(t, p, b, c)
	})
}

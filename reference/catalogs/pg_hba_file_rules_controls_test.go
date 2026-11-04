package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func hbaNativePool(t *testing.T, d *stuntgres.Driver, dsn string) (*sql.DB, fixture.Backend) {
	t.Helper()
	connector, err := d.OpenConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	raw := sql.OpenDB(connector)
	t.Cleanup(func() { raw.Close() })
	recorded, recorder := fixture.RecordedNativePool(t, connector)
	return raw, fixture.Backend{Name: "memory", DB: recorded, Recorder: recorder}
}
func hbaDriver(rules []stuntgres.HBARule) *stuntgres.Driver {
	d := stuntgres.NewDriver()
	d.SetHBARules(rules)
	return d
}

// Restore creates its private destination stage before consuming this reader.
// Install a newer public batch during that I/O, not merely before Restore.
type hbaRestoreInput struct {
	io.Reader
	install func()
}

func (r *hbaRestoreInput) Read(dst []byte) (int, error) {
	if r.install != nil {
		r.install()
		r.install = nil
	}
	return r.Reader.Read(dst)
}

func hbaDump(t *testing.T, p *fixture.Pair, raw *sql.DB) string {
	t.Helper()
	var out bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, raw, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
func hbaControlFlags(t *testing.T, p *fixture.Pair, b fixture.Backend, granted bool) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT has_table_privilege('pg_read_all_stats','pg_catalog.pg_hba_file_rules','SELECT') AS whole,has_column_privilege('pg_signal_backend','pg_catalog.pg_hba_file_rules','options','SELECT') AS col,has_function_privilege('pg_signal_backend','pg_catalog.pg_hba_file_rules()','EXECUTE') AS routine", []string{"whole", "col", "routine"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{granted, granted, granted}}))
	hbaInitialACL(t, p, b, b.DB, !granted)
}
func TestReferencePGHBAFileRulesControls(t *testing.T) {
	p, f := fixture.NewHBAFileScenario(t, hbaLocalSet("local all all peer map=source_hba_marker\n", "peer", "map=source_hba_marker"))
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			want := [][]any{hbaLocal(1, f.Path(b, ""), 1, "peer", "map=source_hba_marker")}
			hbaRead(t, p, b, b.DB, hbaSelect, want, false)
			hbaInitialACL(t, p, b, b.DB, true)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE hba_control_sentinel(n int)", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO hba_control_sentinel VALUES(42)", 1))
			if b.PG {
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
					defer cancel()
					for _, q := range []string{"REVOKE SELECT ON pg_catalog.pg_hba_file_rules FROM pg_read_all_stats", "REVOKE SELECT(options) ON pg_catalog.pg_hba_file_rules FROM pg_signal_backend", "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_hba_file_rules() FROM pg_signal_backend"} {
						if _, err := b.DB.ExecContext(ctx, q); err != nil {
							t.Error(err)
						}
					}
				}()
			}
			if !b.PG {
				baseline := hbaDump(t, p, p.Native)
				for _, fragment := range []string{"pg_hba_file_rules", "source_hba_marker", f.Path(b, "")} {
					if strings.Contains(baseline, fragment) {
						t.Fatal("baseline leaks source or reconstructed ACL", fragment, baseline)
					}
				}
			}
			for _, q := range []string{"GRANT SELECT ON pg_catalog.pg_hba_file_rules TO pg_read_all_stats", "GRANT SELECT(options) ON pg_catalog.pg_hba_file_rules TO pg_signal_backend", "GRANT EXECUTE ON FUNCTION pg_catalog.pg_hba_file_rules() TO pg_signal_backend"} {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
			}
			hbaControlFlags(t, p, b, true)
			actor, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, actor, b.Recorder, fixture.Exec("SET ROLE pg_signal_backend", 0))
			fixture.RunStep(t, p.Ctx, actor, b.Recorder, fixture.Select("SELECT options FROM pg_hba_file_rules", []string{"options"}, []string{"_TEXT"}, [][]any{{hbaArray("map=source_hba_marker")}}))
			fixture.RunStep(t, p.Ctx, actor, b.Recorder, fixture.Exec("RESET ROLE", 0))
			actor.Close()
			f.Install(t, p.Ctx, b, hbaLocalSet("local all all peer map=current\n", "peer", "map=current"))
			hbaRead(t, p, b, b.DB, hbaSelect, [][]any{hbaLocal(1, f.Path(b, ""), 1, "peer", "map=current")}, true)
			f.Install(t, p.Ctx, b, hbaLocalSet("local all all peer map=source_hba_marker\n", "peer", "map=source_hba_marker"))
			if b.PG {
				return
			}
			t.Run("native-dump-reset-destination-restore", func(t *testing.T) {
				f.Install(t, p.Ctx, b, fixture.HBARuleSet{})
				saved := hbaDump(t, p, p.Native)
				for _, fragment := range []string{"pg_hba_file_rules", "pg_read_all_stats", "pg_signal_backend", "attacl", "proacl", "relacl"} {
					if !strings.Contains(saved, fragment) {
						t.Fatal("missing symbolic ACL", fragment, saved)
					}
				}
				for _, fragment := range []string{f.Path(b, ""), "source_hba_marker"} {
					if strings.Contains(saved, fragment) {
						t.Fatal("dump serialized authentication authority", saved)
					}
				}
				f.Install(t, p.Ctx, b, hbaLocalSet("local all all peer map=source_hba_marker\n", "peer", "map=source_hba_marker"))
				if after := hbaDump(t, p, p.Native); after != saved {
					t.Fatal("fact replacement changed catalog-only dump", saved, after)
				}
				old, err := b.DB.QueryContext(p.Ctx, hbaSelect)
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				hbaRead(t, p, b, b.DB, hbaSelect, want, true)
				hbaControlFlags(t, p, b, false)
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(saved)); err != nil {
					t.Fatal(err)
				}
				hbaRead(t, p, b, b.DB, hbaSelect, want, false)
				hbaControlFlags(t, p, b, true)
				hbaConsume(t, old, want, false)
				const destination = "destination-label"
				d := hbaDriver([]stuntgres.HBARule{hbaLocalFact(destination, "peer", "map=destination_hba_marker")})
				raw, target := hbaNativePool(t, d, p.NativeDSN)
				stmt, err := target.DB.PrepareContext(p.Ctx, hbaSelect)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: target.DB, Statement: stmt, SQL: hbaSelect}
				d.SetHBARules([]stuntgres.HBARule{hbaLocalFact(destination, "peer", "map=destination_hba_latest")})
				input := &hbaRestoreInput{Reader: strings.NewReader(saved), install: func() {
					d.SetHBARules([]stuntgres.HBARule{hbaLocalFact(destination, "peer", "map=destination_during_restore")})
				}}
				if err := stuntgres.Restore(p.Ctx, raw, input); err != nil {
					t.Fatal(err)
				}
				targetWant := [][]any{hbaLocal(1, destination, 1, "peer", "map=destination_during_restore")}
				hbaRead(t, p, target, held, hbaSelect, targetWant, true)
				hbaControlFlags(t, p, target, true)
				before := hbaDump(t, p, raw)
				for _, fragment := range []string{destination, "destination_hba_", "destination_during_restore", "source_hba_marker", f.Path(b, "")} {
					if strings.Contains(before, fragment) {
						t.Fatal("destination dump leaked simulated facts", fragment, before)
					}
				}
				retained, err := target.DB.QueryContext(p.Ctx, hbaSelect)
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				d.SetHBARules([]stuntgres.HBARule{hbaLocalFact(destination, "peer", "map=failed_latest")})
				if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader("CREATE TABLE hba_partial(n int); INSERT INTO hba_partial VALUES('bad');")); err == nil {
					t.Fatal("bad Restore accepted")
				}
				hbaRead(t, p, target, held, hbaSelect, [][]any{hbaLocal(1, destination, 1, "peer", "map=failed_latest")}, true)
				d.SetHBARules([]stuntgres.HBARule{hbaLocalFact(destination, "peer", "map=cancelled_latest")})
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				if err := stuntgres.Restore(ctx, raw, strings.NewReader(saved)); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if after := hbaDump(t, p, raw); after != before {
					t.Fatal("failed Restore changed data/ACL", before, after)
				}
				hbaRead(t, p, target, held, hbaSelect, [][]any{hbaLocal(1, destination, 1, "peer", "map=cancelled_latest")}, false)
				hbaControlFlags(t, p, target, true)
				fixture.RunStep(t, p.Ctx, target.DB, target.Recorder, fixture.Select("SELECT n FROM hba_control_sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
				d.SetHBARules([]stuntgres.HBARule{hbaLocalFact(destination, "trust")})
				hbaRead(t, p, target, target.DB, hbaSelect, [][]any{hbaLocal(1, destination, 1, "trust")}, true)
				hbaConsume(t, retained, targetWant, false)
				if err := stuntgres.Reset(p.Ctx, raw); err != nil {
					t.Fatal(err)
				}
				hbaRead(t, p, target, held, hbaSelect, [][]any{hbaLocal(1, destination, 1, "trust")}, true)
				hbaControlFlags(t, p, target, false)
				hbaRead(t, p, b, b.DB, hbaSelect, want, false)
			})
			t.Run("native-absent-routine-acl", func(t *testing.T) {
				// Use an isolated destination: the earlier explicit-grant scenario
				// must not supply this actor's backing-routine EXECUTE authority.
				const destination = "destinationB-label"
				markers := []string{"destination_absent_0", "destination_absent_1", "destination_absent_2"}
				d := hbaDriver([]stuntgres.HBARule{hbaLocalFact(destination, "peer", "map="+markers[0])})
				raw, target := hbaNativePool(t, d, p.NativeDSN)
				hbaRead(t, p, target, target.DB, hbaSelect, [][]any{hbaLocal(1, destination, 1, "peer", "map="+markers[0])}, false)
				hbaInitialACL(t, p, target, target.DB, true)
				fixture.RunStep(t, p.Ctx, target.DB, target.Recorder, fixture.Exec("CREATE TABLE hba_absent_acl_sentinel(n int)", 0))
				fixture.RunStep(t, p.Ctx, target.DB, target.Recorder, fixture.Exec("INSERT INTO hba_absent_acl_sentinel VALUES(42)", 1))
				fixture.RunStep(t, p.Ctx, target.DB, target.Recorder, fixture.Exec("GRANT SELECT ON pg_catalog.pg_hba_file_rules TO pg_signal_backend", 0))
				privileges := fixture.Select("SELECT has_table_privilege('pg_signal_backend','pg_catalog.pg_hba_file_rules','SELECT') AS can_select,has_function_privilege('pg_signal_backend','pg_catalog.pg_hba_file_rules()','EXECUTE') AS can_execute,has_function_privilege('public','pg_catalog.pg_hba_file_rules()','EXECUTE') AS public_execute", []string{"can_select", "can_execute", "public_execute"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, false, false}})
				fixture.RunStep(t, p.Ctx, target.DB, target.Recorder, privileges)
				fixture.RunStep(t, p.Ctx, target.DB, target.Recorder, fixture.Prepared(privileges))
				setup := hbaDump(t, p, raw)
				const nullRepair = "UPDATE pg_catalog.pg_proc SET proacl=NULL::pg_catalog.aclitem[] WHERE oid='pg_catalog.pg_hba_file_rules()'::pg_catalog.regprocedure;\n"
				// Dump always quotes both identifiers and wraps the symbolic
				// identity in an E-string, unlike the accepted setup spelling.
				const dumpedNullRepair = "UPDATE pg_catalog.pg_proc SET proacl=NULL::pg_catalog.aclitem[] WHERE oid=E'\"pg_catalog\".\"pg_hba_file_rules\"()'::pg_catalog.regprocedure;\n"
				if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(setup+nullRepair)); err != nil {
					t.Fatal(err)
				}
				for round, marker := range markers {
					// Raw NULL and its effective PUBLIC authority are distinct
					// from the explicit owner-only seed and historical initprivs.
					for _, s := range []fixture.Step{
						fixture.Select("SELECT proacl IS NULL AS absent FROM pg_catalog.pg_proc WHERE oid=3401", []string{"absent"}, []string{"BOOL"}, [][]any{{true}}),
						fixture.Select("SELECT proacl::text FROM pg_catalog.pg_proc WHERE oid=3401", []string{"proacl"}, []string{"TEXT"}, [][]any{{nil}}),
					} {
						fixture.RunStep(t, p.Ctx, target.DB, target.Recorder, s)
						fixture.RunStep(t, p.Ctx, target.DB, target.Recorder, fixture.Prepared(s))
					}
					privileges.Rows = [][]any{{true, true, true}}
					fixture.RunStep(t, p.Ctx, target.DB, target.Recorder, privileges)
					fixture.RunStep(t, p.Ctx, target.DB, target.Recorder, fixture.Prepared(privileges))
					hbaInitialACL(t, p, target, target.DB, false)
					fixture.RunStep(t, p.Ctx, target.DB, target.Recorder, fixture.Select("SELECT n FROM hba_absent_acl_sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
					func() {
						actor, err := target.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer actor.Close()
						defer actor.ExecContext(context.Background(), "RESET ROLE")
						fixture.RunStep(t, p.Ctx, actor, target.Recorder, fixture.Exec("SET ROLE pg_signal_backend", 0))
						targetWant := [][]any{hbaLocal(1, destination, 1, "peer", "map="+marker)}
						hbaRead(t, p, target, actor, hbaSelect, targetWant, false)
						hbaRead(t, p, target, actor, hbaSelect, targetWant, true)
						fixture.RunStep(t, p.Ctx, actor, target.Recorder, fixture.Exec("RESET ROLE", 0))
					}()
					if round < 2 {
						d.SetHBARules([]stuntgres.HBARule{hbaLocalFact(destination, "peer", "map="+markers[round+1])})
						saved := hbaDump(t, p, raw)
						if !strings.Contains(saved, dumpedNullRepair) {
							t.Fatal("dump omitted absent backing-routine ACL repair", saved)
						}
						for _, fragment := range []string{f.Path(b, ""), destination, "source_hba_marker", "destination_absent_"} {
							if strings.Contains(saved, fragment) {
								t.Fatal("dump serialized authentication authority", fragment, saved)
							}
						}
						if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(saved)); err != nil {
							t.Fatal(err)
						}
					}
				}
				if err := stuntgres.Reset(p.Ctx, raw); err != nil {
					t.Fatal(err)
				}
				hbaInitialACL(t, p, target, target.DB, true)
				privileges.Rows = [][]any{{false, false, false}}
				fixture.RunStep(t, p.Ctx, target.DB, target.Recorder, privileges)
				fixture.RunStep(t, p.Ctx, target.DB, target.Recorder, fixture.Prepared(privileges))
				hbaRead(t, p, target, target.DB, hbaSelect, [][]any{hbaLocal(1, destination, 1, "peer", "map="+markers[2])}, false)
				hbaRead(t, p, b, b.DB, hbaSelect, want, false)
			})
			t.Run("native-driver-logical-database-and-reconnect", func(t *testing.T) {
				d := p.NativeConnector.Driver().(*stuntgres.Driver)
				_, other := hbaNativePool(t, d, p.NativeDSN+" opaque distinct key")
				hbaRead(t, p, other, other.DB, hbaSelect, want, true)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DATABASE hba_logical TEMPLATE template1", 0))
				connector, err := d.OpenDatabaseConnector(p.NativeDSN, "hba_logical")
				if err != nil {
					t.Fatal(err)
				}
				db, recorder := fixture.RecordedNativePool(t, connector)
				logical := fixture.Backend{Name: "memory", DB: db, Recorder: recorder}
				hbaRead(t, p, logical, db, hbaSelect, want, false)
				raw, pooled := hbaNativePool(t, d, "hba-close-all")
				hbaRead(t, p, pooled, pooled.DB, hbaSelect, want, false)
				pooled.DB.Close()
				raw.Close()
				_, reopened := hbaNativePool(t, d, "hba-close-all")
				hbaRead(t, p, reopened, reopened.DB, hbaSelect, want, true)
				d.SetHBARules([]stuntgres.HBARule{hbaLocalFact("driver-wide-latest", "peer", "map=shared")})
				next := [][]any{hbaLocal(1, "driver-wide-latest", 1, "peer", "map=shared")}
				for _, current := range []fixture.Backend{b, other, logical, reopened} {
					hbaRead(t, p, current, current.DB, hbaSelect, next, true)
				}
				// A template cannot be cloned while its recorded pool retains
				// physical sessions, even when all query rows are closed.
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DATABASE hba_future TEMPLATE hba_logical", 0))
				db, recorder = fixture.RecordedNativePool(t, connector)
				logical = fixture.Backend{Name: "memory", DB: db, Recorder: recorder}
				hbaRead(t, p, logical, db, hbaSelect, next, true)
				futureConnector, err := d.OpenDatabaseConnector(p.NativeDSN, "hba_future")
				if err != nil {
					t.Fatal(err)
				}
				futureDB, futureRecorder := fixture.RecordedNativePool(t, futureConnector)
				future := fixture.Backend{Name: "memory", DB: futureDB, Recorder: futureRecorder}
				hbaRead(t, p, future, futureDB, hbaSelect, next, false)
				d.SetHBARules([]stuntgres.HBARule{})
				for _, current := range []fixture.Backend{b, other, logical, reopened, future} {
					hbaRead(t, p, current, current.DB, hbaSelect, nil, false)
				}
				f.Install(t, p.Ctx, b, hbaLocalSet("local all all peer map=source_hba_marker\n", "peer", "map=source_hba_marker"))
			})
			t.Run("native-unattached-is-not-show-label", func(t *testing.T) {
				for _, d := range []*stuntgres.Driver{stuntgres.NewDriver(), new(stuntgres.Driver)} {
					_, empty := hbaNativePool(t, d, p.NativeDSN)
					for _, prepared := range []bool{false, true} {
						hbaRead(t, p, empty, empty.DB, hbaSelect, nil, prepared)
					}
					fixture.RunStep(t, p.Ctx, empty.DB, empty.Recorder, fixture.Select("SHOW hba_file", []string{"hba_file"}, []string{"TEXT"}, [][]any{{"/stuntgres/simulated/pg_hba.conf"}}))
					fixture.RunStep(t, p.Ctx, empty.DB, empty.Recorder, fixture.Exec("CREATE ROLE hba_unattached_actor", 0))
					actor, err := empty.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer actor.Close()
					fixture.RunStep(t, p.Ctx, actor, empty.Recorder, fixture.Exec("SET ROLE hba_unattached_actor", 0))
					hbaDenied(t, p, empty, actor, hbaSelect, false)
				}
			})
		})
	}
}

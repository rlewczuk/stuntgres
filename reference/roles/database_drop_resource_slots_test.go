package reference_test

import (
	"context"
	"fmt"
	"net/url"
	"runtime"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
)

// This read-only host consumes a genuine provider result. Its physical handle
// deliberately remains registered with the session Owner after the scan ends.
// There is no local row substitute and no independent resource registry.
type dropSlotExternalHandler struct{}

func (*dropSlotExternalHandler) GetForeignRelSize(context.Context, stuntgres.ForeignRequest) (stuntgres.ForeignRelSize, error) {
	return stuntgres.ForeignRelSize{Rows: 1, Width: 4}, nil
}
func (*dropSlotExternalHandler) GetForeignPaths(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignRelSize) ([]stuntgres.ForeignPath, error) {
	return []stuntgres.ForeignPath{{Attributes: []int32{1}, TotalCost: 1}}, nil
}
func (*dropSlotExternalHandler) GetForeignPlan(_ context.Context, _ stuntgres.ForeignRequest, p stuntgres.ForeignPath) (stuntgres.ForeignPlan, error) {
	return stuntgres.ForeignPlan{Attributes: p.Attributes}, nil
}
func (*dropSlotExternalHandler) BeginForeignScan(ctx context.Context, r stuntgres.ForeignRequest, _ stuntgres.ForeignPlan) (stuntgres.ForeignScan, error) {
	if r.ExternalSQL == nil {
		return nil, fmt.Errorf("missing actual external SQL capability")
	}
	c, err := r.ExternalSQL.Open(ctx, stuntgres.ExternalSQLConnectRequest{})
	if err != nil {
		return nil, err
	}
	results, err := c.Execute(ctx, "SELECT v FROM public.drop_resource_source")
	if err != nil {
		return nil, err
	}
	if len(results) != 1 || len(results[0].Columns) != 1 || results[0].Columns[0].TypeOID != 23 || results[0].RowCount != 1 || len(results[0].Cells) != 1 || results[0].Cells[0].Null {
		return nil, fmt.Errorf("unexpected genuine source result: %#v", results)
	}
	return &dropSlotExternalScan{value: results[0].Cells[0].Text}, nil
}

type dropSlotExternalScan struct {
	value string
	done  bool
}

func (s *dropSlotExternalScan) IterateForeignScan(context.Context) (stuntgres.ForeignRow, bool, error) {
	if s.done {
		return nil, false, nil
	}
	s.done = true
	return stuntgres.ForeignRow{s.value}, true, nil
}
func (s *dropSlotExternalScan) ReScanForeignScan(context.Context, stuntgres.ForeignScanParameters) error {
	s.done = false
	return nil
}
func (s *dropSlotExternalScan) EndForeignScan(context.Context) error { s.value = ""; return nil }

func TestReferenceDatabaseDropResourceSlots(t *testing.T) {
	if !*fixture.Mode {
		t.Skip("real dblink/externalSQL resources require REFERENCE=1")
	}
	// Reuse this package's real logical-WAL oracle; no startup/config change in
	// the dblink or table-provider packages is needed to manufacture a slot.
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			const target = "drop_resource_slot_target"
			names := []string{target}
			fixture.DatabaseReferenceCleanup(t, &names)
			provider := &fixture.ExternalSQLProvider{}
			p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
				if err := d.RegisterExternalSQLProvider(stuntgres.ExternalSQLProviderDefinition{Name: "drop_slot_provider", Version: fixture.ExternalSQLFixtureVersion, Provider: provider}); err != nil {
					return err
				}
				if err := d.BindDblinkExternalSQLProvider("drop_slot_provider"); err != nil {
					return err
				}
				return d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Name: "drop_slot_handler"}, ExternalProvider: "drop_slot_provider", Wrapper: &dropSlotExternalHandler{}})
			})
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("CREATE TABLE drop_resource_source(v integer)", 0))
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("INSERT INTO drop_resource_source VALUES(43)", 1))
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(c fixture.Executor, r *fixture.Recorder, s fixture.Step) {
						if s.State != "" {
							s.ErrorStage = "execute"
						}
						fixture.RunStep(t, p.Ctx, c, r, s)
					}
					run(b.DB, b.Recorder, fixture.Exec("CREATE DATABASE "+target, 0))
					t.Cleanup(func() {
						ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
						defer cancel()
						if _, err := b.DB.ExecContext(ctx, "DROP DATABASE IF EXISTS "+target+" WITH (FORCE)"); err != nil {
							t.Error(err)
						}
					})
					db, _, rec := fixture.DatabaseScenarioPool(t, p, b.PG, target, "")
					c, err := db.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					before := len(provider.Snapshot())
					run(c, rec, fixture.Exec("CREATE EXTENSION dblink", 0))
					conninfo := "host=127.0.0.1 port=5432 dbname=stuntgres_reference user=stuntgres password=stuntgres sslmode=disable application_name=drop_resource_slot_dblink"
					if !b.PG {
						u, err := url.Parse(fixture.DSN)
						if err != nil {
							t.Fatal(err)
						}
						q := u.Query()
						q.Set("application_name", "drop_resource_slot_dblink")
						u.RawQuery = q.Encode()
						conninfo = u.String()
					}
					step := fixture.Select("SELECT dblink_connect('owned',$1::text) AS value", []string{"value"}, []string{"TEXT"}, [][]any{{"OK"}}, conninfo)
					step.Prepared = prepared
					run(c, rec, step)
					if b.PG {
						for _, q := range []string{"CREATE EXTENSION postgres_fdw", "CREATE SERVER drop_resource_remote FOREIGN DATA WRAPPER postgres_fdw OPTIONS(host '127.0.0.1',port '5432',dbname 'stuntgres_reference',application_name 'drop_resource_slot_fdw')", "CREATE USER MAPPING FOR CURRENT_USER SERVER drop_resource_remote OPTIONS(user 'stuntgres',password 'stuntgres')"} {
							run(c, rec, fixture.Exec(q, 0))
						}
					} else {
						config, err := pgx.ParseConfig(fixture.DSN)
						if err != nil {
							t.Fatal(err)
						}
						for _, q := range []string{"CREATE FOREIGN DATA WRAPPER drop_resource_native HANDLER drop_slot_handler", fmt.Sprintf("CREATE SERVER drop_resource_remote FOREIGN DATA WRAPPER drop_resource_native OPTIONS(host %s,port '%d',dbname %s,application_name 'drop_resource_slot_fdw')", pq.QuoteLiteral(config.Host), config.Port, pq.QuoteLiteral(config.Database)), fmt.Sprintf("CREATE USER MAPPING FOR CURRENT_USER SERVER drop_resource_remote OPTIONS(user %s,password %s)", pq.QuoteLiteral(config.User), pq.QuoteLiteral(config.Password))} {
							run(c, rec, fixture.Exec(q, 0))
						}
					}
					run(c, rec, fixture.Exec("CREATE FOREIGN TABLE resource_value(v integer) SERVER drop_resource_remote OPTIONS(schema_name 'public',table_name 'drop_resource_source')", 0))
					step = fixture.Select("SELECT v FROM resource_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(43)}})
					step.Prepared = prepared
					run(c, rec, step)
					var wires []*fixture.ExternalSQLWire
					if !b.PG {
						wires = provider.Snapshot()[before:]
						if len(wires) != 2 {
							t.Fatal("both actual gateways must own a real handle", len(wires))
						}
					}
					pids := make([]int32, 0, 2)
					for _, app := range []string{"drop_resource_slot_dblink", "drop_resource_slot_fdw"} {
						var pid int32
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pid FROM pg_stat_activity WHERE application_name=$1", app).Scan(&pid); err != nil || pid <= 0 {
							t.Fatal("actual resource backend", app, pid, err)
						}
						pids = append(pids, pid)
					}
					run(c, rec, fixture.Select("SELECT slot_name FROM pg_create_logical_replication_slot('drop_resource_active','test_decoding',true)", []string{"slot_name"}, []string{"NAME"}, [][]any{{"drop_resource_active"}}))
					var held = make(map[string]fixture.HeldStatement)
					for _, suffix := range []string{"", " WITH (FORCE)"} {
						q := "DROP DATABASE " + target + suffix
						var executor fixture.Executor = b.DB
						if prepared {
							stmt, err := b.DB.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							h := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
							held[q] = h
							executor = h
						}
						for range 2 {
							run(executor, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "55006", Message: `database "drop_resource_slot_target" is used by an active logical replication slot`, Detail: "There is 1 active slot."}))
							run(c, rec, fixture.Select("SELECT slot_name,active FROM pg_replication_slots WHERE slot_name='drop_resource_active'", []string{"slot_name", "active"}, []string{"NAME", "BOOL"}, [][]any{{"drop_resource_active", true}}))
							run(c, rec, fixture.Select("SELECT unnest(dblink_get_connections()) AS name", []string{"name"}, []string{"TEXT"}, [][]any{{"owned"}}))
							for _, pid := range pids {
								var n int
								if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_stat_activity WHERE pid=$1", pid).Scan(&n); err != nil || n != 1 {
									t.Fatal("slot refusal closed actual resource", pid, n, err)
								}
							}
							for _, w := range wires {
								if w.Activity().Closes != 0 {
									t.Fatal("slot refusal closed registered gateway resource", w.Activity())
								}
								if _, err := w.Execute(p.Ctx, "SELECT 47"); err != nil {
									t.Fatal("slot refusal invalidated actual transport", err)
								}
							}
						}
					}
					run(c, rec, fixture.Select("SELECT pg_drop_replication_slot('drop_resource_active')", []string{"pg_drop_replication_slot"}, []string{"VOID"}, [][]any{{""}}))
					q := "DROP DATABASE " + target + " WITH (FORCE)"
					var executor fixture.Executor = b.DB
					if prepared {
						executor = held[q]
					}
					run(executor, b.Recorder, fixture.Exec(q, 0))
					for _, pid := range pids {
						for {
							var n int
							if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_stat_activity WHERE pid=$1", pid).Scan(&n); err != nil {
								t.Fatal(err)
							}
							if n == 0 {
								break
							}
							if err := p.Ctx.Err(); err != nil {
								t.Fatal("resource backend did not end", pid, err)
							}
							runtime.Gosched()
						}
					}
					for _, w := range wires {
						if w.Activity().Closes != 1 {
							t.Fatal("actual gateway resource not closed exactly once after cleared blocker", w.Activity())
						}
						if _, err := w.Execute(p.Ctx, "SELECT 1"); err == nil {
							t.Fatal("retired resource still usable")
						}
					}
				})
			}
		})
	}
}

package reference_test

import (
	"context"
	"database/sql/driver"
	"slices"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// This provider is an actual public registered native method table, not a C
// handler emulator. The ordinary phase below uses only stock SQL/MED objects.
type sepgsqlForeignProvider struct {
	rows  []stuntgres.ForeignRow
	calls int
}

func (p *sepgsqlForeignProvider) GetForeignRelSize(context.Context, stuntgres.ForeignRequest) (stuntgres.ForeignRelSize, error) {
	p.calls++
	return stuntgres.ForeignRelSize{Rows: float64(len(p.rows)), Width: 4}, nil
}
func (p *sepgsqlForeignProvider) GetForeignPaths(_ context.Context, r stuntgres.ForeignRequest, _ stuntgres.ForeignRelSize) ([]stuntgres.ForeignPath, error) {
	p.calls++
	return []stuntgres.ForeignPath{{Attributes: r.RequiredAttributes}}, nil
}
func (p *sepgsqlForeignProvider) GetForeignPlan(_ context.Context, _ stuntgres.ForeignRequest, path stuntgres.ForeignPath) (stuntgres.ForeignPlan, error) {
	p.calls++
	return stuntgres.ForeignPlan{Attributes: path.Attributes}, nil
}
func (p *sepgsqlForeignProvider) BeginForeignScan(_ context.Context, request stuntgres.ForeignRequest, plan stuntgres.ForeignPlan) (stuntgres.ForeignScan, error) {
	p.calls++
	rows := p.rows
	if request.Transaction != nil {
		rows = request.Transaction.(*sepgsqlForeignTransaction).rows
	}
	return &sepgsqlForeignCursor{rows: rows, attributes: plan.Attributes}, nil
}
func (p *sepgsqlForeignProvider) AnalyzeForeignTable(context.Context, stuntgres.ForeignRequest) (stuntgres.ForeignAnalyzeInfo, bool, error) {
	p.calls++
	return stuntgres.ForeignAnalyzeInfo{AcquireSampleRows: func(_ context.Context, _ stuntgres.ForeignRequest, target int) (stuntgres.ForeignSample, error) {
		p.calls++
		rows := p.rows
		if len(rows) > target {
			rows = rows[:target]
		}
		return stuntgres.ForeignSample{Rows: rows, TotalRows: float64(len(p.rows)), TotalPages: 1}, nil
	}}, true, nil
}
func (p *sepgsqlForeignProvider) ImportForeignSchema(context.Context, stuntgres.ForeignImportRequest) ([]stuntgres.ForeignTableDefinition, error) {
	p.calls++
	return []stuntgres.ForeignTableDefinition{{Name: "imported", Columns: []stuntgres.ForeignColumnDefinition{{Name: "id", Type: "integer"}}}}, nil
}
func (p *sepgsqlForeignProvider) ExecForeignInsert(_ context.Context, request stuntgres.ForeignModifyRequest, _ any, row stuntgres.ForeignRow) (stuntgres.ForeignRow, bool, error) {
	p.calls++
	tx := request.Transaction.(*sepgsqlForeignTransaction)
	tx.rows = append(tx.rows, slices.Clone(row))
	return row, true, nil
}
func (p *sepgsqlForeignProvider) ExecForeignTruncate(_ context.Context, request stuntgres.ForeignTruncateRequest) error {
	p.calls++
	request.Transaction.(*sepgsqlForeignTransaction).rows = nil
	return nil
}

func (p *sepgsqlForeignProvider) ForeignTransactionCapabilities(ctx context.Context, _ stuntgres.ForeignRequest) (stuntgres.ForeignTransactionCapabilities, error) {
	p.calls++
	return stuntgres.ForeignTransactionCapabilities{StatementRollback: true, TransactionRollback: true}, ctx.Err()
}
func (p *sepgsqlForeignProvider) BeginForeignTransaction(ctx context.Context, _ stuntgres.ForeignRequest) (stuntgres.ForeignTransaction, error) {
	p.calls++
	return &sepgsqlForeignTransaction{provider: p, rows: slices.Clone(p.rows)}, ctx.Err()
}

// Pending tuples remain private until the actual participant commits. Integer
// cells are immutable; statement snapshots need only copy their tuple vector.
type sepgsqlForeignTransaction struct {
	provider  *sepgsqlForeignProvider
	rows      []stuntgres.ForeignRow
	statement []stuntgres.ForeignRow
}

func (tx *sepgsqlForeignTransaction) BeginStatement(ctx context.Context) error {
	tx.statement = slices.Clone(tx.rows)
	return ctx.Err()
}
func (tx *sepgsqlForeignTransaction) EndStatement(ctx context.Context, success bool) error {
	if !success {
		tx.rows = tx.statement
	}
	tx.statement = nil
	return ctx.Err()
}
func (tx *sepgsqlForeignTransaction) Commit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx.provider.rows = tx.rows
	tx.rows, tx.statement = nil, nil
	return nil
}
func (tx *sepgsqlForeignTransaction) Rollback(context.Context) error {
	tx.rows, tx.statement = nil, nil
	return nil
}

type sepgsqlForeignCursor struct {
	rows       []stuntgres.ForeignRow
	attributes []int32
	next       int
}

func (s *sepgsqlForeignCursor) IterateForeignScan(context.Context) (stuntgres.ForeignRow, bool, error) {
	if s.next == len(s.rows) {
		return nil, false, nil
	}
	row := make(stuntgres.ForeignRow, len(s.attributes))
	for i, attribute := range s.attributes {
		row[i] = s.rows[s.next][attribute-1]
	}
	s.next++
	return row, true, nil
}
func (s *sepgsqlForeignCursor) ReScanForeignScan(context.Context, stuntgres.ForeignScanParameters) error {
	s.next = 0
	return nil
}
func (*sepgsqlForeignCursor) EndForeignScan(context.Context) error { return nil }

func TestReferenceSepgsqlSimulationForeignFactory(t *testing.T) {
	t.Run("provider_independent_sql_med", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			t.Run(backend.Name, func(t *testing.T) {
				c, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, step) }
				run(fixture.Exec("CREATE FOREIGN DATA WRAPPER sepgsql_foreign NO HANDLER", 0))
				run(fixture.Exec("CREATE SERVER sepgsql_foreign FOREIGN DATA WRAPPER sepgsql_foreign OPTIONS (endpoint 'fixture')", 0))
				run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER sepgsql_foreign OPTIONS (token 'unit')", 0))
				run(fixture.Exec("CREATE FOREIGN TABLE sepgsql_foreign(id integer) SERVER sepgsql_foreign", 0))
				for _, prepared := range []bool{false, true} {
					step := fixture.Select("SELECT fdwhandler=0 AS absent,fdwvalidator=0 AS unvalidated FROM pg_foreign_data_wrapper WHERE fdwname='sepgsql_foreign'", []string{"absent", "unvalidated"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
					step.Prepared = prepared
					run(step)
					failure := fixture.ImplicitError("SELECT id FROM sepgsql_foreign", fixture.Diagnostic{Code: "55000", Message: `foreign-data wrapper "sepgsql_foreign" has no handler`})
					failure.Prepared = prepared
					run(failure)
				}
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("ALTER SERVER sepgsql_foreign OPTIONS (SET endpoint 'rollback')", 0))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Select("SELECT srvoptions::text AS options FROM pg_foreign_server WHERE srvname='sepgsql_foreign'", []string{"options"}, []string{"TEXT"}, [][]any{{"{endpoint=fixture}"}}))
				run(fixture.Exec("ALTER FOREIGN TABLE sepgsql_foreign RENAME TO sepgsql_foreign_renamed", 0))
				run(sepgsqlCount("SELECT count(*) FROM pg_foreign_table WHERE ftrelid='sepgsql_foreign_renamed'::regclass", 1))
			})
		}
	})
	t.Run("unsupported_import_sql_med", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"sepgsql_import_sql_reader"})
		fixture.SchemaReferenceCleanup(t, "sepgsql_import")
		// Register before the pair: all physical handles close first, then
		// database-wide wrappers/servers and the non-public schema retire
		// before RoleReferenceCleanup removes their ACL principal.
		t.Cleanup(func() {
			if !fixture.Started {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			for _, query := range []string{
				`DROP FOREIGN DATA WRAPPER IF EXISTS "SQL import alias" CASCADE`,
				`DROP FOREIGN DATA WRAPPER IF EXISTS "Renamed SQL alias" CASCADE`,
				"DROP FOREIGN DATA WRAPPER IF EXISTS sepgsql_no_handler CASCADE",
			} {
				if _, err := fixture.Admin.ExecContext(ctx, query); err != nil {
					t.Errorf("unsupported import foreign metadata cleanup %s: %v", query, err)
				}
			}
		})
		provider := &sepgsqlForeignProvider{}
		// Expose only the genuine scan method set: the registered wrapper has
		// no optional importer, rather than an importer that fabricates failure.
		wrapper := struct{ stuntgres.ForeignDataWrapper }{provider}
		p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
			return d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Name: "sepgsql_scan_only_handler"}, Wrapper: wrapper})
		})
		for _, backend := range p.Backends() {
			t.Run(backend.Name, func(t *testing.T) {
				c, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, step) }
				handler := "sepgsql_scan_only_handler"
				if backend.PG {
					// file_fdw's real method table lacks ImportForeignSchema.
					// No foreign file, remote host or active SELinux is needed.
					run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS file_fdw", 0))
					handler = "file_fdw_handler"
				}
				run(fixture.Exec(`CREATE FOREIGN DATA WRAPPER "SQL import alias" HANDLER `+handler+` NO VALIDATOR`, 0))
				run(fixture.Exec(`CREATE SERVER sepgsql_no_import FOREIGN DATA WRAPPER "SQL import alias"`, 0))
				run(fixture.Exec("CREATE SCHEMA sepgsql_import", 0))
				run(fixture.Exec("CREATE TABLE sepgsql_import.sentinel(id integer)", 0))
				run(fixture.Exec("INSERT INTO sepgsql_import.sentinel VALUES(7)", 1))
				const query = "IMPORT FOREIGN SCHEMA remote FROM SERVER sepgsql_no_import INTO sepgsql_import"
				statement, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer statement.Close()
				held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
				for _, name := range []string{"SQL import alias", "Renamed SQL alias"} {
					if name == "Renamed SQL alias" {
						run(fixture.Exec(`ALTER FOREIGN DATA WRAPPER "SQL import alias" RENAME TO "Renamed SQL alias"`, 0))
					}
					failure := fixture.ImplicitError(query, fixture.Diagnostic{Code: "HV00P", Message: `foreign-data wrapper "` + name + `" does not support IMPORT FOREIGN SCHEMA`})
					failure.ErrorStage = "execute"
					run(failure)
					prepared := failure
					prepared.Prepared = true
					run(prepared)
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, failure)
					run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relnamespace='sepgsql_import'::regnamespace", 1))
					run(fixture.Select("SELECT id FROM sepgsql_import.sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				}
				run(fixture.Exec("CREATE FOREIGN DATA WRAPPER sepgsql_no_handler NO HANDLER", 0))
				run(fixture.Exec("CREATE SERVER sepgsql_no_handler FOREIGN DATA WRAPPER sepgsql_no_handler", 0))
				for _, failure := range []fixture.Step{
					fixture.ImplicitError("IMPORT FOREIGN SCHEMA remote FROM SERVER absent_import_server INTO sepgsql_import", fixture.Diagnostic{Code: "42704", Message: `server "absent_import_server" does not exist`}),
					fixture.ImplicitError("IMPORT FOREIGN SCHEMA remote FROM SERVER sepgsql_no_import INTO absent_import_schema", fixture.Diagnostic{Code: "3F000", Message: `schema "absent_import_schema" does not exist`}),
					fixture.ImplicitError("IMPORT FOREIGN SCHEMA remote FROM SERVER sepgsql_no_handler INTO sepgsql_import", fixture.Diagnostic{Code: "55000", Message: `foreign-data wrapper "sepgsql_no_handler" has no handler`}),
				} {
					run(failure)
					failure.Prepared = true
					run(failure)
				}
				run(fixture.Exec("CREATE ROLE sepgsql_import_sql_reader", 0))
				run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER sepgsql_no_import TO sepgsql_import_sql_reader", 0))
				run(fixture.Exec("GRANT USAGE ON SCHEMA sepgsql_import TO sepgsql_import_sql_reader", 0))
				for _, message := range []string{
					"permission denied for schema sepgsql_import",
					"permission denied for foreign server sepgsql_no_import",
				} {
					if message == "permission denied for foreign server sepgsql_no_import" {
						run(fixture.Exec("REVOKE USAGE ON FOREIGN SERVER sepgsql_no_import FROM sepgsql_import_sql_reader", 0))
					}
					run(fixture.Exec("SET ROLE sepgsql_import_sql_reader", 0))
					failure := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: message})
					failure.ErrorStage = "execute"
					run(failure)
					prepared := failure
					prepared.Prepared = true
					run(prepared)
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, failure)
					run(fixture.Exec("RESET ROLE", 0))
				}
				if !backend.PG && provider.calls != 0 {
					t.Fatal("unsupported import invoked a provider method")
				}
			})
		}
	})
	t.Run("native_no_importer_simulation", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"sepgsql_import_reader"})
		provider := &sepgsqlForeignProvider{rows: []stuntgres.ForeignRow{{driver.Value(int64(7))}}}
		wrapper := struct{ stuntgres.ForeignDataWrapper }{provider}
		if _, ok := any(wrapper).(stuntgres.ForeignSchemaImporter); ok {
			t.Fatal("scan-only wrapper exposes optional importer")
		}
		var events []stuntgres.SepgsqlAuditEvent
		config := sepgsqlConfig()
		config.AuditAllowed = true
		config.Audit = func(event stuntgres.SepgsqlAuditEvent) { events = append(events, event) }
		config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlS, Class: "db_procedure", Deny: []string{"execute"}})
		config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions, stuntgres.SepgsqlTrustedTransition{Source: sepgsqlR, Procedure: sepgsqlU, Result: sepgsqlT})
		p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
			if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Name: "sepgsql_scan_only_handler"}, Wrapper: wrapper}); err != nil {
				return err
			}
			return d.RegisterSepgsqlSimulation(config)
		})
		for _, backend := range p.Backends() {
			if backend.PG {
				continue
			}
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, step) }
			for _, query := range []string{
				`CREATE FOREIGN DATA WRAPPER "SQL import alias" HANDLER sepgsql_scan_only_handler`,
				`CREATE SERVER sepgsql_no_import FOREIGN DATA WRAPPER "SQL import alias"`,
				"CREATE SCHEMA sepgsql_import",
				"CREATE TABLE sepgsql_import.sentinel(id integer)",
				"INSERT INTO sepgsql_import.sentinel VALUES(7)",
				"CREATE ROLE sepgsql_import_reader",
				"GRANT USAGE ON FOREIGN SERVER sepgsql_no_import TO sepgsql_import_reader",
				"GRANT USAGE,CREATE ON SCHEMA sepgsql_import TO sepgsql_import_reader",
				"REVOKE ALL ON FUNCTION sepgsql_scan_only_handler() FROM PUBLIC",
				"SECURITY LABEL ON FUNCTION sepgsql_scan_only_handler() IS '" + sepgsqlS + "'",
			} {
				affected := int64(0)
				if query == "INSERT INTO sepgsql_import.sentinel VALUES(7)" {
					affected = 1
				}
				run(fixture.Exec(query, affected))
			}
			const query = "IMPORT FOREIGN SCHEMA remote FROM SERVER sepgsql_no_import INTO sepgsql_import"
			statement, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
			for _, phase := range []struct{ name, label, code string }{
				{"SQL import alias", sepgsqlS, "HV00P"},
				{"Renamed SQL alias", sepgsqlU, "42501"},
				{"Renamed SQL alias", sepgsqlS, "HV00P"},
			} {
				if phase.label == sepgsqlU {
					run(fixture.Exec(`ALTER FOREIGN DATA WRAPPER "SQL import alias" RENAME TO "Renamed SQL alias"`, 0))
					run(fixture.Exec("ALTER FUNCTION sepgsql_scan_only_handler() RENAME TO sepgsql_scan_renamed", 0))
				}
				handler := "sepgsql_scan_only_handler"
				if phase.name == "Renamed SQL alias" {
					handler = "sepgsql_scan_renamed"
				}
				run(fixture.Exec("SECURITY LABEL ON FUNCTION "+handler+"() IS '"+phase.label+"'", 0))
				saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
				run(sepgsqlSet(sepgsqlR))
				run(fixture.Exec("SET ROLE sepgsql_import_reader", 0))
				failure := sepgsqlDenied(query)
				if phase.code == "HV00P" {
					failure = fixture.ImplicitError(query, fixture.Diagnostic{Code: "HV00P", Message: `foreign-data wrapper "` + phase.name + `" does not support IMPORT FOREIGN SCHEMA`})
				}
				failure.ErrorStage = "execute"
				for _, route := range []string{"direct", "prepared", "held"} {
					events = nil
					step := failure
					step.Prepared = route == "prepared"
					if route == "held" {
						fixture.RunStep(t, p.Ctx, held, backend.Recorder, step)
					} else {
						run(step)
					}
					entry, transition := false, false
					for _, event := range events {
						if event.Class == "db_procedure" {
							if slices.Contains(event.Required, "execute") {
								t.Fatal("import fabricated SQL execute authorization", event)
							}
							if event.Subject != sepgsqlR || event.Target != phase.label || !slices.Contains(event.Required, "entrypoint") || (phase.code == "42501") != slices.Contains(event.Denied, "entrypoint") {
								t.Fatal("wrong import factory audit", event)
							}
							entry = true
						}
						transition = transition || event.Class == "process" && slices.Contains(event.Required, "transition")
					}
					if !entry || phase.code == "HV00P" && !transition || provider.calls != 0 || len(provider.rows) != 1 {
						t.Fatal("missing factory phase or partial provider effects", route, events, provider.calls, provider.rows)
					}
					run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
				}
				run(fixture.Exec("RESET ROLE", 0))
				run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
				if fixture.ExtensionDump(t, p.Ctx, p.Native) != saved {
					t.Fatal("failed import changed tables, labels, options or rows")
				}
				run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relnamespace='sepgsql_import'::regnamespace", 1))
				run(fixture.Select("SELECT id FROM sepgsql_import.sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
			// Ordinary SQL authority still wins over the denied real factory,
			// even for the held handle that previously reached that factory.
			run(fixture.Exec("SECURITY LABEL ON FUNCTION sepgsql_scan_renamed() IS '"+sepgsqlU+"'", 0))
			for _, boundary := range []struct{ revoke, message string }{
				{"REVOKE CREATE ON SCHEMA sepgsql_import FROM sepgsql_import_reader", "permission denied for schema sepgsql_import"},
				{"REVOKE USAGE ON FOREIGN SERVER sepgsql_no_import FROM sepgsql_import_reader", "permission denied for foreign server sepgsql_no_import"},
			} {
				run(fixture.Exec(boundary.revoke, 0))
				saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
				run(sepgsqlSet(sepgsqlR))
				run(fixture.Exec("SET ROLE sepgsql_import_reader", 0))
				failure := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: boundary.message})
				failure.ErrorStage = "execute"
				for _, route := range []string{"direct", "prepared", "held"} {
					events = nil
					step := failure
					step.Prepared = route == "prepared"
					if route == "held" {
						fixture.RunStep(t, p.Ctx, held, backend.Recorder, step)
					} else {
						run(step)
					}
					for _, event := range events {
						if event.Class == "db_procedure" || event.Class == "process" && slices.Contains(event.Required, "transition") {
							t.Fatal("ordinary ACL error entered the handler factory", event)
						}
					}
					if provider.calls != 0 || len(provider.rows) != 1 {
						t.Fatal("ordinary ACL error reached provider methods")
					}
					run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
				}
				run(fixture.Exec("RESET ROLE", 0))
				run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
				if fixture.ExtensionDump(t, p.Ctx, p.Native) != saved {
					t.Fatal("ordinary ACL import failure changed persistent state")
				}
			}
		}
	})
	t.Run("native_registered_provider_simulation", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"sepgsql_foreign_reader"})
		provider := &sepgsqlForeignProvider{rows: []stuntgres.ForeignRow{{driver.Value(int64(1))}, {driver.Value(int64(2))}}}
		config := sepgsqlConfig()
		config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlS, Class: "db_procedure", Deny: []string{"execute"}})
		config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions, stuntgres.SepgsqlTrustedTransition{Source: sepgsqlR, Procedure: sepgsqlU, Result: sepgsqlT})
		p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
			if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Name: "sepgsql_foreign_handler"}, Wrapper: provider}); err != nil {
				return err
			}
			return d.RegisterSepgsqlSimulation(config)
		})
		for _, backend := range p.Backends() {
			if backend.PG {
				continue
			}
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, step) }
			for _, query := range []string{
				"CREATE FOREIGN DATA WRAPPER sepgsql_foreign HANDLER sepgsql_foreign_handler",
				"CREATE SERVER sepgsql_foreign FOREIGN DATA WRAPPER sepgsql_foreign",
				"CREATE USER MAPPING FOR PUBLIC SERVER sepgsql_foreign",
				"CREATE FOREIGN TABLE sepgsql_foreign(id integer) SERVER sepgsql_foreign",
				"CREATE ROLE sepgsql_foreign_reader",
				"GRANT SELECT ON sepgsql_foreign TO sepgsql_foreign_reader",
				"GRANT USAGE ON FOREIGN SERVER sepgsql_foreign TO sepgsql_foreign_reader",
				"REVOKE ALL ON FUNCTION sepgsql_foreign_handler() FROM PUBLIC",
				"SECURITY LABEL ON FUNCTION sepgsql_foreign_handler() IS '" + sepgsqlS + "'",
			} {
				run(fixture.Exec(query, 0))
			}
			run(sepgsqlSet(sepgsqlR))
			run(fixture.Exec("SET ROLE sepgsql_foreign_reader", 0))
			run(sepgsqlBool("SELECT has_function_privilege(current_user,'sepgsql_foreign_handler()','EXECUTE') AS allowed", "allowed", false))
			for _, prepared := range []bool{false, true} {
				step := sepgsqlCount("SELECT count(*) FROM sepgsql_foreign", 2)
				step.Prepared = prepared
				run(step)
				run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
			}
			run(fixture.Exec("RESET ROLE", 0))
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			run(fixture.Exec("ALTER FUNCTION sepgsql_foreign_handler() RENAME TO sepgsql_foreign_renamed", 0))
			run(fixture.Exec("ANALYZE sepgsql_foreign", 0))
			run(fixture.Exec("IMPORT FOREIGN SCHEMA remote FROM SERVER sepgsql_foreign INTO public", 0))
			run(fixture.Exec("INSERT INTO imported VALUES(3)", 1))
			run(sepgsqlCount("SELECT count(*) FROM imported", 3))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO imported VALUES(4)", 1))
			run(sepgsqlCount("SELECT count(*) FROM imported", 4))
			run(fixture.Exec("ROLLBACK", 0))
			run(sepgsqlCount("SELECT count(*) FROM imported", 3))
			run(fixture.Exec("SECURITY LABEL ON FUNCTION sepgsql_foreign_renamed() IS '"+sepgsqlU+"'", 0))
			run(sepgsqlSet(sepgsqlR))
			before := provider.calls
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_foreign WHERE false", 0))
			if provider.calls != before {
				t.Fatal("unused source invoked provider methods")
			}
			for _, query := range []string{"SELECT count(*) FROM sepgsql_foreign", "EXPLAIN SELECT id FROM sepgsql_foreign", "ANALYZE sepgsql_foreign", "INSERT INTO imported VALUES(4)", "IMPORT FOREIGN SCHEMA remote FROM SERVER sepgsql_foreign INTO public", "TRUNCATE sepgsql_foreign"} {
				run(sepgsqlDenied(query))
				run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
			}
			if provider.calls != before {
				t.Fatal("denied factory reached a raw provider method")
			}
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			run(sepgsqlCount("SELECT count(*) FROM imported", 3))
			run(fixture.Exec("TRUNCATE imported", 0))
			run(sepgsqlCount("SELECT count(*) FROM imported", 0))
		}
	})
}

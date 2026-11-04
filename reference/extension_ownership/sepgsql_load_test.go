package reference_test

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// REL_18_6 sepgsql_utility_command rejects LOAD in enforcing mode; otherwise
// standard_ProcessUtility still calls load_file(filename, !superuser()). The
// provider-independent phase tests that real ordinary authorization on both
// backends. Registered-provider success below is explicitly native simulation,
// not an installed oracle library or active SELinux parity.
func TestReferenceSepgsqlSimulationLoadDispatch(t *testing.T) {
	for _, mode := range []string{"enforcing", "permissive"} {
		t.Run(mode, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"sepgsql_load_reader", "sepgsql_load_super"})
			sepgsqlCase(t, sepgsqlLoadOrdinary, func(config *stuntgres.SepgsqlSimulationConfig) {
				config.Permissive = mode == "permissive"
			}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
				sepgsqlLoadRoles(run)
				before := sepgsqlLoadInventory(t, p, c)
				held := sepgsqlLoadHeld(t, p, c)
				sepgsqlLoadUnloaded(t, p, backend, c)
				if mode == "enforcing" {
					for _, reader := range []bool{false, true} {
						if reader {
							run(fixture.Exec("SET ROLE sepgsql_load_reader", 0))
						}
						for _, filename := range append(sepgsqlLoadFilenames(), "$bad/name") {
							sepgsqlLoadError(t, p, backend, c, "LOAD '"+filename+"'", "42501", "SELinux: LOAD is not permitted")
						}
						for _, statement := range held {
							step := fixture.ImplicitError(statement.SQL, fixture.Diagnostic{Code: "42501", Message: "SELinux: LOAD is not permitted"})
							step.ErrorStage = "execute"
							fixture.RunStep(t, p.Ctx, statement, backend.Recorder, step)
						}
						sepgsqlLoadUnloaded(t, p, backend, c)
					}
					run(fixture.Exec("RESET ROLE", 0))
				} else {
					sepgsqlLoadCurrentAuthority(t, p, backend, c, run, held)
					for range 2 {
						run(fixture.Exec("LOAD '$libdir/sepgsql'", 0))
						sepgsqlLoadUnloaded(t, p, backend, c)
						fixture.RunStep(t, p.Ctx, held[0], backend.Recorder, fixture.Exec(held[0].SQL, 0))
						sepgsqlLoadUnloaded(t, p, backend, c)
						run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlA))
						sepgsqlLoadUnchanged(t, p, c, before)
					}
					sepgsqlLoadError(t, p, backend, c, "LOAD '$bad/name'", "42602", "invalid macro name in path: $bad/name")
					for _, filename := range []string{"missing", "$libdir/plugins/missing", "sepgsql", "$libdir/sepgsql.so"} {
						sepgsqlLoadError(t, p, backend, c, "LOAD '"+filename+"'", "0A000", "LOAD native libraries are unsupported in embedded mode")
					}
					// Every alias starts on its own fresh physical connection, so
					// no previous alias can conceal a missing activation route.
					for _, alias := range sepgsqlLoadFilenames()[1:] {
						pool, _, recorder := fixture.DatabaseConnectorScenarioPool(t, p.NativeConnector, false)
						peer, err := pool.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						func() {
							defer peer.Close()
							b := backend
							b.Recorder = recorder
							sepgsqlLoadUnloaded(t, p, b, peer)
							fixture.RunStep(t, p.Ctx, peer, recorder, fixture.Exec("LOAD '$libdir/sepgsql'", 0))
							sepgsqlLoadUnloaded(t, p, b, peer)
							query := "LOAD '" + alias + "'"
							statement, err := peer.PrepareContext(p.Ctx, query)
							if err != nil {
								t.Fatal(err)
							}
							defer statement.Close()
							sepgsqlLoadUnloaded(t, p, b, peer)
							heldAlias := fixture.HeldStatement{Executor: peer, Statement: statement, SQL: query}
							for range 2 {
								fixture.RunStep(t, p.Ctx, peer, recorder, fixture.Exec(query, 0))
								sepgsqlLoadDebug(t, p, peer)
								fixture.RunStep(t, p.Ctx, heldAlias, recorder, fixture.Exec(query, 0))
								sepgsqlLoadDebug(t, p, peer)
							}
							// An already loaded overexplain capability also survives
							// the registered-provider no-op without being replaced.
							fixture.RunStep(t, p.Ctx, peer, recorder, fixture.Exec("LOAD '$libdir/sepgsql'", 0))
							sepgsqlLoadDebug(t, p, peer)
						}()
						sepgsqlLoadUnloaded(t, p, backend, c)
					}
					// Genuine LOAD on the original connection enables DEBUG there.
					run(fixture.Exec("LOAD 'pg_overexplain'", 0))
					sepgsqlLoadDebug(t, p, c)
					pool, _, recorder := fixture.DatabaseConnectorScenarioPool(t, p.NativeConnector, false)
					fresh, err := pool.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer fresh.Close()
					b := backend
					b.Recorder = recorder
					sepgsqlLoadUnloaded(t, p, b, fresh)
				}
				run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlA))
				run(sepgsqlCount("SELECT count(*) FROM pg_extension WHERE extname IN ('sepgsql','pg_overexplain')", 0))
				sepgsqlLoadUnchanged(t, p, c, before)
			})
		})
	}
}

func sepgsqlLoadFilenames() []string {
	return []string{"$libdir/sepgsql", "pg_overexplain", "pg_overexplain.so", "$libdir/pg_overexplain", "$libdir/pg_overexplain.so"}
}

func sepgsqlLoadRoles(run sepgsqlRun) {
	run(fixture.Exec("CREATE ROLE sepgsql_load_reader INHERIT", 0))
	run(fixture.Exec("CREATE ROLE sepgsql_load_super SUPERUSER", 0))
	run(fixture.Exec("GRANT sepgsql_load_super TO sepgsql_load_reader WITH INHERIT TRUE, SET TRUE", 0))
}

func sepgsqlLoadHeld(t *testing.T, p *fixture.Pair, c *sql.Conn) []fixture.HeldStatement {
	t.Helper()
	var held []fixture.HeldStatement
	for _, filename := range sepgsqlLoadFilenames() {
		query := "LOAD '" + filename + "'"
		statement, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal("LOAD Prepare must defer admission and activation", query, err)
		}
		t.Cleanup(func() {
			if err := statement.Close(); err != nil {
				t.Error(err)
			}
		})
		held = append(held, fixture.HeldStatement{Executor: c, Statement: statement, SQL: query})
	}
	return held
}

func sepgsqlLoadError(t *testing.T, p *fixture.Pair, backend fixture.Backend, c fixture.Executor, query, code, message string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		step := fixture.ImplicitError(query, fixture.Diagnostic{Code: code, Message: message})
		step.Prepared, step.ErrorStage = prepared, "execute"
		fixture.RunStep(t, p.Ctx, c, backend.Recorder, step)
	}
}

func sepgsqlLoadCurrentAuthority(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun, held []fixture.HeldStatement) {
	t.Helper()
	defer c.ExecContext(p.Ctx, "RESET ROLE")
	for _, role := range []string{"sepgsql_load_reader", "sepgsql_load_super"} {
		run(fixture.Exec("SET ROLE "+role, 0))
		currentHeld := held
		if role == "sepgsql_load_super" {
			currentHeld = sepgsqlLoadHeld(t, p, c)
			// This independent admin connection really changes the held
			// statement's current role descriptor after Prepare and SET ROLE.
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("ALTER ROLE sepgsql_load_super NOSUPERUSER", 0))
		}
		for i, filename := range sepgsqlLoadFilenames() {
			message := "access to library \"" + filename + "\" is not allowed"
			sepgsqlLoadError(t, p, backend, c, currentHeld[i].SQL, "42501", message)
			step := fixture.ImplicitError(currentHeld[i].SQL, fixture.Diagnostic{Code: "42501", Message: message})
			step.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, currentHeld[i], backend.Recorder, step)
		}
		sepgsqlLoadUnloaded(t, p, backend, c)
		run(fixture.Exec("RESET ROLE", 0))
	}
	// Restore the intentional fixture mutation before comparing LOAD's public
	// nonpublication snapshot. LOAD itself never changes a role descriptor.
	run(fixture.Exec("ALTER ROLE sepgsql_load_super SUPERUSER", 0))
}

func sepgsqlLoadOrdinary(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
	sepgsqlLoadRoles(run)
	held := sepgsqlLoadHeld(t, p, c)
	sepgsqlLoadUnloaded(t, p, backend, c)
	sepgsqlLoadCurrentAuthority(t, p, backend, c, run, held)
	sepgsqlLoadError(t, p, backend, c, "LOAD '$bad/name'", "42602", "invalid macro name in path: $bad/name")
	// This phase deliberately never attempts an administrative sepgsql LOAD
	// on stock PostgreSQL. Its ordinary library denial is meaningful without
	// any SELinux library or provider fixture.
	run(sepgsqlCount("SELECT count(*) FROM pg_proc WHERE proname IN ('sepgsql_getcon','sepgsql_setcon','sepgsql_restorecon')", 0))
	if !backend.PG {
		sepgsqlLoadError(t, p, backend, c, "LOAD '$libdir/sepgsql'", "0A000", "LOAD native libraries are unsupported in embedded mode")
	}
}

func sepgsqlLoadUnloaded(t *testing.T, p *fixture.Pair, backend fixture.Backend, c fixture.Executor) {
	t.Helper()
	const query = "/*é*/ EXPLAIN (DEBUG) SELECT 1"
	position := fixture.Position(len([]rune(query[:strings.Index(query, "DEBUG")])) + 1)
	for _, prepared := range []bool{false, true} {
		step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42601", Message: "unrecognized EXPLAIN option \"debug\"", Position: position})
		step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
		fixture.RunStep(t, p.Ctx, c, backend.Recorder, step)
	}
}

// Inspect actual public EXPLAIN metadata and structural DEBUG output. Native
// bound node IDs are positive statement-local identities, not a stock plan or
// a golden string copied from the embedded renderer.
func sepgsqlLoadDebug(t *testing.T, p *fixture.Pair, c *sql.Conn) {
	t.Helper()
	const query = "EXPLAIN (DEBUG, FORMAT JSON, COSTS OFF) SELECT 1"
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		var err error
		if prepared {
			statement, prepareErr := c.PrepareContext(p.Ctx, query)
			if prepareErr != nil {
				t.Fatal(prepareErr)
			}
			rows, err = statement.QueryContext(p.Ctx)
			if err := statement.Close(); err != nil {
				t.Fatal(err)
			}
		} else {
			rows, err = c.QueryContext(p.Ctx, query)
		}
		if err != nil {
			t.Fatal("genuine overexplain LOAD did not enable DEBUG", err)
		}
		func() {
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(columns, []string{"QUERY PLAN"}) {
				t.Fatal("DEBUG result columns", columns, err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "JSON" {
				t.Fatal("DEBUG result types", types, err)
			}
			if !rows.Next() {
				t.Fatal("DEBUG missing plan document", rows.Err())
			}
			var text string
			if err := rows.Scan(&text); err != nil {
				t.Fatal(err)
			}
			var document []struct {
				Plan struct {
					NodeType string `json:"Node Type"`
					Debug    struct {
						NodeID int `json:"Node ID"`
					} `json:"Embedded Debug"`
				} `json:"Plan"`
			}
			if err := json.Unmarshal([]byte(text), &document); err != nil || len(document) != 1 ||
				document[0].Plan.NodeType == "" || document[0].Plan.Debug.NodeID <= 0 {
				t.Fatal("DEBUG omitted real bound-node identity", text, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("DEBUG extra plan document or terminal error", rows.Err())
			}
		}()
	}
}

// Public snapshots supplement the executor's exact immutable-root/allocator
// assertions: declarations, installed inventory, SQL roles, relation identities
// and all local/shared labels must not change as a consequence of LOAD.
func sepgsqlLoadInventory(t *testing.T, p *fixture.Pair, c *sql.Conn) [][][]string {
	t.Helper()
	queries := []string{
		"SELECT oid::text,extname,extowner::text,extnamespace::text,extrelocatable::text,extversion FROM pg_extension ORDER BY oid",
		"SELECT oid::text,proname,proowner::text,pronamespace::text,prolang::text,prosrc,proargtypes::text FROM pg_proc WHERE proname LIKE 'sepgsql_%' ORDER BY oid",
		"SELECT oid::text,rolname,rolsuper::text,rolcanlogin::text FROM pg_roles ORDER BY oid",
		"SELECT oid::text,relname,relnamespace::text,relkind::text FROM pg_class ORDER BY oid",
		"SELECT objoid::text,classoid::text,objsubid::text,provider,label FROM pg_seclabel ORDER BY classoid,objoid,objsubid,provider",
		"SELECT objoid::text,classoid::text,provider,label FROM pg_shseclabel ORDER BY classoid,objoid,provider",
	}
	var inventory [][][]string
	for _, query := range queries {
		rows, err := c.QueryContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		var table [][]string
		func() {
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				values := make([]string, len(columns))
				pointers := make([]any, len(columns))
				for i := range values {
					pointers[i] = &values[i]
				}
				if err := rows.Scan(pointers...); err != nil {
					t.Fatal(err)
				}
				table = append(table, values)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		}()
		inventory = append(inventory, table)
	}
	return inventory
}

func sepgsqlLoadUnchanged(t *testing.T, p *fixture.Pair, c *sql.Conn, before [][][]string) {
	t.Helper()
	if after := sepgsqlLoadInventory(t, p, c); !reflect.DeepEqual(before, after) {
		t.Fatal("LOAD changed installed inventory, declarations, roles, catalog identities or labels", before, after)
	}
}

package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAmcheckControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"am_control_owner", "am_control_reader"})
	fixture.SchemaReferenceCleanup(t, "am_tools")
	p := fixture.NewScenario(t)
	for _, query := range []string{
		"CREATE SCHEMA am_tools",
		"CREATE EXTENSION amcheck WITH SCHEMA am_tools",
		"CREATE TABLE am_heap(n INT,s TEXT)",
		"CREATE INDEX am_idx ON am_heap(n)",
		"CREATE INDEX am_expr ON am_heap(lower(s)) WHERE n>1",
		"CREATE UNIQUE INDEX am_unique ON am_heap(s) WHERE n=1",
		"CREATE VIEW am_verified AS SELECT am_tools.bt_index_parent_check('am_idx',true,true,true) IS NULL AS null_result",
	} {
		p.Step(t, fixture.Exec(query, 0))
	}
	p.Step(t, fixture.Exec("INSERT INTO am_heap VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", 5))
	for _, query := range []string{
		"CREATE ROLE am_control_owner",
		"CREATE ROLE am_control_reader",
		"ALTER TABLE am_heap OWNER TO am_control_owner",
		"ALTER FUNCTION am_tools.bt_index_check(regclass) OWNER TO am_control_owner",
		"GRANT EXECUTE ON FUNCTION am_tools.bt_index_check(regclass) TO am_control_reader WITH GRANT OPTION",
	} {
		p.Step(t, fixture.Exec(query, 0))
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			// Pin the preparation owner: a pool may otherwise prepare the old
			// *sql.Stmt on a different connection and hide its stale capture.
			heldConn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer heldConn.Close()
			query := "SELECT am_tools.bt_index_parent_check('public.am_idx',true,true,true) AS ok"
			held, err := heldConn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			owner := fixture.HeldStatement{Executor: heldConn, Statement: held, SQL: query}
			fixture.RunStep(t, p.Ctx, owner, backend.Recorder, amcheckSelect(query, false))
			viewQuery := "SELECT null_result FROM am_verified"
			viewHeld, err := heldConn.PrepareContext(p.Ctx, viewQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer viewHeld.Close()
			viewOwner := fixture.HeldStatement{Executor: heldConn, Statement: viewHeld, SQL: viewQuery}
			viewStep := fixture.Select(viewQuery, []string{"null_result"}, []string{"BOOL"}, [][]any{{false}})
			fixture.RunStep(t, p.Ctx, viewOwner, backend.Recorder, viewStep)
			parameterQuery := "SELECT am_tools.bt_index_parent_check($1::regclass,true,true,true) AS ok"
			parameterHeld, err := heldConn.PrepareContext(p.Ctx, parameterQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer parameterHeld.Close()
			parameterOwner := fixture.HeldStatement{Executor: heldConn, Statement: parameterHeld, SQL: parameterQuery}
			parameterStep := amcheckSelect(parameterQuery, false, "public.am_idx")
			fixture.RunStep(t, p.Ctx, parameterOwner, backend.Recorder, parameterStep)
			// Preserve the original runtime lifetime regression's five routes
			// here, using real held driver definitions in both fixture modes.
			nullQuery := "SELECT am_tools.bt_index_parent_check('public.am_idx',true,true,true) IS NULL AS null_result"
			nullHeld, err := heldConn.PrepareContext(p.Ctx, nullQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer nullHeld.Close()
			nullOwner := fixture.HeldStatement{Executor: heldConn, Statement: nullHeld, SQL: nullQuery}
			nullStep := fixture.Select(nullQuery, []string{"null_result"}, []string{"BOOL"}, [][]any{{false}})
			checkLifetime := func(label string, replaced bool) {
				t.Run(label, func(t *testing.T) {
					if replaced {
						rows, err := nullHeld.QueryContext(p.Ctx)
						if rows != nil {
							rows.Close()
							t.Fatal("stale regclass capture returned rows")
						}
						fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
					} else {
						fixture.RunStep(t, p.Ctx, nullOwner, backend.Recorder, nullStep)
					}
					fixture.RunStep(t, p.Ctx, heldConn, backend.Recorder, nullStep)
					fixture.RunStep(t, p.Ctx, heldConn, backend.Recorder, fixture.Prepared(nullStep))
					fixture.RunStep(t, p.Ctx, viewOwner, backend.Recorder, viewStep)
					fixture.RunStep(t, p.Ctx, heldConn, backend.Recorder, viewStep)
				})
			}
			checkLifetime("lifetime_before", false)
			var dump bytes.Buffer
			if !backend.PG {
				old, err := backend.DB.QueryContext(p.Ctx, "SELECT n,s FROM am_heap ORDER BY n NULLS LAST,s")
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
				var got [][]any
				for old.Next() {
					var n, s any
					if err := old.Scan(&n, &s); err != nil {
						t.Fatal(err)
					}
					got = append(got, []any{fixture.Normalize(n), fixture.Normalize(s)})
				}
				if err := old.Err(); err != nil {
					t.Fatal(err)
				}
				want := [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}, {nil, nil}}
				if len(got) != len(want) {
					t.Fatal("retained eager rows", got)
				}
				for i := range want {
					for j := range want[i] {
						if got[i][j] != want[i][j] {
							t.Fatal("retained eager row changed", got)
						}
					}
				}
				checkLifetime("lifetime_restored", true)
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
					t.Fatal("canceled restore", err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE broken (")); err == nil {
					t.Fatal("failed restore succeeded")
				}
			}
			checkLifetime("lifetime_failed_replacements", !backend.PG)
			if !backend.PG {
				// Native replacement retires the exact persistent regclass
				// literal. It must not be rebound to the restored index by name.
				rows, err := held.QueryContext(p.Ctx)
				if rows != nil {
					rows.Close()
					t.Fatal("stale regclass capture returned rows")
				}
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			} else {
				// PostgreSQL did not undergo the native-only control phase.
				fixture.RunStep(t, p.Ctx, owner, backend.Recorder, amcheckSelect(query, false))
			}
			fixture.RunStep(t, p.Ctx, parameterOwner, backend.Recorder, parameterStep)
			fixture.RunStep(t, p.Ctx, heldConn, backend.Recorder, amcheckSelect(query, false))
			fixture.RunStep(t, p.Ctx, heldConn, backend.Recorder, fixture.Prepared(amcheckSelect(query, false)))
			fixture.RunStep(t, p.Ctx, viewOwner, backend.Recorder, viewStep)
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT null_result FROM am_verified", []string{"null_result"}, []string{"BOOL"}, [][]any{{false}}))
			fixture.RunStep(t, p.Ctx, heldConn, backend.Recorder, fixture.Prepared(viewStep))
			actor, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer actor.Close()
			defer actor.ExecContext(p.Ctx, "RESET ROLE")
			// Keep namespace ACLs out of the serialized source, then permit
			// qualified routine and regclass lookup before testing EXECUTE.
			fixture.RunStep(t, p.Ctx, actor, backend.Recorder, fixture.Exec("GRANT USAGE ON SCHEMA am_tools,public TO am_control_reader", 0))
			fixture.RunStep(t, p.Ctx, actor, backend.Recorder, fixture.Exec("SET ROLE am_control_reader", 0))
			// This distinct overload was granted before Dump. Invocation after
			// Restore proves serialized authority without authorizing the parent slot.
			basic := amcheckSelect("SELECT am_tools.bt_index_check('public.am_idx') AS ok", false)
			fixture.RunStep(t, p.Ctx, actor, backend.Recorder, basic)
			fixture.RunStep(t, p.Ctx, actor, backend.Recorder, fixture.Prepared(basic))
			denied := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function bt_index_parent_check"})
			denied.SelectRows = true
			fixture.RunStep(t, p.Ctx, actor, backend.Recorder, denied)
			fixture.RunStep(t, p.Ctx, actor, backend.Recorder, fixture.Exec("RESET ROLE", 0))
			// The all-slot grantor/options/PUBLIC/empty replay matrix lives in
			// TestReferenceAmcheckBtreeACLProvenance; retain this lifetime witness.
			fixture.RunStep(t, p.Ctx, actor, backend.Recorder, fixture.Exec("GRANT EXECUTE ON FUNCTION am_tools.bt_index_parent_check(regclass,boolean,boolean,boolean) TO am_control_reader", 0))
			fixture.RunStep(t, p.Ctx, actor, backend.Recorder, fixture.Exec("SET ROLE am_control_reader", 0))
			fixture.RunStep(t, p.Ctx, actor, backend.Recorder, amcheckSelect(query, false))
			fixture.RunStep(t, p.Ctx, actor, backend.Recorder, fixture.Exec("RESET ROLE", 0))
			if err := actor.Close(); err != nil {
				t.Fatal(err)
			}
			if !backend.PG {
				connector, err := stuntgres.NewDriver().OpenConnector("amcheck-independent")
				if err != nil {
					t.Fatal(err)
				}
				isolated := sql.OpenDB(connector)
				defer isolated.Close()
				var installed int64
				if err := isolated.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_extension WHERE extname='amcheck'").Scan(&installed); err != nil {
					t.Fatal(err)
				}
				if installed != 0 {
					t.Fatalf("independent driver inherited amcheck installation: %d", installed)
				}
			}
			for _, statement := range []string{"INSERT INTO am_heap VALUES(4,'d')", "UPDATE am_heap SET n=5 WHERE n=4", "DELETE FROM am_heap WHERE n=5"} {
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(statement, 1))
				for _, index := range []string{"am_idx", "am_expr", "am_unique"} {
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, amcheckSelect("SELECT am_tools.bt_index_parent_check('"+index+"',true,true,true) AS ok", false))
				}
				checkLifetime("lifetime_maintained", !backend.PG)
			}
		})
	}
}

func TestReferenceAmcheckFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	amcheckSetup(t, p)
	for _, overload := range amcheckOverloads {
		arguments := "'am_idx'"
		for i := 1; i < overload.arguments; i++ {
			arguments += ",true"
		}
		p.Step(t, amcheckSelect("SELECT "+overload.name+"("+arguments+") AS ok", false))
	}
	p.Step(t, fixture.Prepared(amcheckSelect("SELECT bt_index_parent_check($1::regclass,true,true,true) AS ok", true, nil)))
	p.Step(t, fixture.Exec("CREATE INDEX am_hash ON am_heap USING hash(n)", 0))
	p.Step(t, fixture.ImplicitError("SELECT bt_index_check('am_hash')", fixture.Diagnostic{Code: "0A000", Message: `expected "btree" index as targets for verification`, Detail: `Relation "am_hash" is a hash index.`}))
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			amcheckReferenceLockStrength(t, p, backend)
			if !backend.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
			}
			for _, query := range []string{"INSERT INTO am_heap VALUES(4,'d')", "UPDATE am_heap SET n=5 WHERE n=4", "DELETE FROM am_heap WHERE n=5"} {
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(query, 1))
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, amcheckSelect("SELECT bt_index_parent_check('am_expr',true,true,true) AS ok", false))
			}
			tx, err := backend.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for _, query := range []string{"INSERT INTO am_heap VALUES(4,'D')", "SAVEPOINT private", "UPDATE am_heap SET n=5 WHERE n=4", "DELETE FROM am_heap WHERE n=5", "ROLLBACK TO private"} {
				affected := int64(1)
				if strings.HasPrefix(query, "SAVEPOINT") || strings.HasPrefix(query, "ROLLBACK") {
					affected = 0
				}
				fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec(query, affected))
				fixture.RunStep(t, p.Ctx, tx, backend.Recorder, amcheckSelect("SELECT bt_index_parent_check('am_expr',true,true,true) AS ok", false))
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

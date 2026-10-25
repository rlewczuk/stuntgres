package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ngrok/sqlmw"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePartitionRoutingControls(t *testing.T) {
	referencePartitionRoutingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		referenceRoutingConstraintTree(run)
		run(fixture.Exec("CREATE TABLE pr_constraint_default PARTITION OF pr_constraint DEFAULT", 0))
		run(fixture.Exec("INSERT INTO pr_constraint VALUES(10,2,10),(20,4,20),(30,8,30)", 3))
		run(fixture.Exec("CREATE TABLE pr_control_list(id INT,k INT) PARTITION BY LIST(k)", 0))
		run(fixture.Exec("CREATE TABLE pr_control_one PARTITION OF pr_control_list FOR VALUES IN(1)", 0))
		run(fixture.Exec("CREATE TABLE pr_control_null PARTITION OF pr_control_list FOR VALUES IN(NULL)", 0))
		run(fixture.Exec("CREATE TABLE pr_control_default PARTITION OF pr_control_list DEFAULT", 0))
		run(fixture.Exec("INSERT INTO pr_control_list VALUES(40,1),(50,NULL)", 2))
		run(fixture.Exec("CREATE TABLE pr_control_hash(id INT,k INT) PARTITION BY HASH(k)", 0))
		run(fixture.Exec("CREATE TABLE pr_control_h0 PARTITION OF pr_control_hash FOR VALUES WITH(MODULUS 4,REMAINDER 0)", 0))
		run(fixture.Exec("CREATE TABLE pr_control_h1 PARTITION OF pr_control_hash FOR VALUES WITH(MODULUS 4,REMAINDER 1)", 0))
		run(fixture.Exec("CREATE TABLE pr_control_h2 PARTITION OF pr_control_hash FOR VALUES WITH(MODULUS 4,REMAINDER 2)", 0))
		run(fixture.Exec("CREATE TABLE pr_control_h3 PARTITION OF pr_control_hash FOR VALUES WITH(MODULUS 4,REMAINDER 3)", 0))
		run(fixture.Exec("INSERT INTO pr_control_hash VALUES(60,1),(70,NULL)", 2))
		partitionExclusionTree(run, "")
		run(fixture.Exec("INSERT INTO pe_root VALUES(80,'10.0.0.129','[1,5)')", 1))
		readSQL := "SELECT id,k,payload FROM pr_constraint ORDER BY id"
		readStmt, err := b.DB.PrepareContext(p.Ctx, readSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer readStmt.Close()
		moveSQL := "UPDATE pr_constraint SET k=$1 WHERE id=$2"
		moveStmt, err := b.DB.PrepareContext(p.Ctx, moveSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer moveStmt.Close()
		move := fixture.HeldStatement{Executor: b.DB, Statement: moveStmt, SQL: moveSQL}
		var retained *sql.Rows
		if !b.PG {
			retained, err = b.DB.QueryContext(p.Ctx, readSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
		}
		fixture.RunStep(t, p.Ctx, move, b.Recorder, fixture.Exec(moveSQL, 1, 3, 10))
		fixture.RunStep(t, p.Ctx, move, b.Recorder, fixture.Exec(moveSQL, 1, 8, 20))
		run(fixture.Exec("UPDATE pr_control_list SET k=7 WHERE id=40", 1))
		run(fixture.Exec("UPDATE pr_control_list SET k=1 WHERE id=50", 1))
		run(fixture.Exec("UPDATE pr_control_hash SET k=2 WHERE id=60", 1))
		run(fixture.Exec("UPDATE pr_control_hash SET k=-1 WHERE id=70", 1))
		run(fixture.Exec("UPDATE pe_root SET tenant='10.0.0.1' WHERE id=80", 1))
		rangeRows := [][]any{{int64(10), int64(3), int64(10)}, {int64(20), int64(8), int64(20)}, {int64(30), int64(8), int64(30)}}
		check := func(x fixture.Executor, recorder *fixture.Recorder, pg bool) {
			step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, recorder, s) }
			for _, c := range []struct {
				table string
				rows  [][]any
			}{
				{"pr_constraint", rangeRows}, {"pr_constraint_low", nil},
				{"pr_constraint_high", rangeRows[:1]}, {"pr_constraint_default", rangeRows[1:]},
			} {
				s := fixture.Select("SELECT id,k,payload FROM "+c.table+" ORDER BY id", []string{"id", "k", "payload"}, []string{"INT4", "INT4", "INT4"}, c.rows)
				nullable := []bool{true, true, true}
				if c.table == "pr_constraint_high" {
					nullable[2] = false
				}
				s.CheckMetadata = referenceRangeMetadata(pg, s.Columns, s.Types, nullable)
				step(s)
			}
			for _, c := range []struct {
				table string
				rows  [][]any
			}{
				{"pr_control_list", [][]any{{int64(40), int64(7)}, {int64(50), int64(1)}}},
				{"pr_control_one", [][]any{{int64(50), int64(1)}}}, {"pr_control_null", nil}, {"pr_control_default", [][]any{{int64(40), int64(7)}}},
				{"pr_control_hash", [][]any{{int64(60), int64(2)}, {int64(70), int64(-1)}}},
				{"pr_control_h0", nil}, {"pr_control_h1", [][]any{{int64(70), int64(-1)}}}, {"pr_control_h2", [][]any{{int64(60), int64(2)}}}, {"pr_control_h3", nil},
			} {
				s := fixture.Select("SELECT id,k FROM "+c.table+" ORDER BY id", []string{"id", "k"}, []string{"INT4", "INT4"}, c.rows)
				s.CheckMetadata = referenceRangeMetadata(pg, s.Columns, s.Types, []bool{true, true})
				step(s)
			}
			step(fixture.Select("SELECT id,tenant,during FROM pe_low", []string{"id", "tenant", "during"}, []string{"INT4", "INET", "INT4RANGE"}, [][]any{{int64(80), "10.0.0.1", "[1,5)"}}))
			step(fixture.Select("SELECT id FROM pe_other", []string{"id"}, []string{"INT4"}, nil))
			step(referenceRangeError("INSERT INTO pr_constraint_low VALUES(90,3,90)", "23514", `new row for relation "pr_constraint_low" violates check constraint "pr_source"`, fixture.Diagnostic{Schema: "public", Table: "pr_constraint_low", Constraint: "pr_source", Detail: "Failing row contains (90, 3, 90)."}))
			step(referenceRangeLeafMiss("INSERT INTO pr_constraint_low VALUES(90,0,90)", "pr_constraint_low", "90, 0, 90"))
			step(referenceRoutingConstraintFailure("INSERT INTO pr_constraint_high VALUES(90,3,NULL)", "not_null", "90, 3, null"))
			step(referenceRoutingConstraintFailure("INSERT INTO pr_constraint_high VALUES(90,3,0)", "check", "90, 3, 0"))
			step(referenceRangeError("INSERT INTO pr_constraint_high VALUES(90,3,10)", "23505", `duplicate key value violates unique constraint "pr_high_payload"`, fixture.Diagnostic{Schema: "public", Table: "pr_constraint_high", Constraint: "pr_high_payload", Detail: "Key (payload)=(10) already exists."}))
			fixture.ExclusionReferenceRun(t, p, fixture.Backend{PG: pg, Recorder: recorder}, x, partitionExclusionConflict("INSERT INTO pe_root VALUES(90,'10.0.0.1','[3,7)')", "pe_low", "pe_low_tenant_during_excl", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
		}
		check(b.DB, b.Recorder, b.PG)
		if b.PG {
			return
		} // Logical native controls have no PostgreSQL API counterpart.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dump.String(), "UPDATE ") {
			t.Fatal("logical dump contains mutation history")
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(b.DB, b.Recorder, false)
		want := fixture.Select(readSQL, []string{"id", "k", "payload"}, []string{"INT4", "INT4", "INT4"}, rangeRows)
		want.CheckMetadata = referenceRangeMetadata(false, want.Columns, want.Types, []bool{true, true, true})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: readStmt, SQL: readSQL}, b.Recorder, want)
		var oldRows [][]any
		for retained.Next() {
			var id, k, payload any
			if err := retained.Scan(&id, &k, &payload); err != nil {
				t.Fatal(err)
			}
			oldRows = append(oldRows, []any{id, k, payload})
		}
		original := [][]any{{int64(10), int64(2), int64(10)}, {int64(20), int64(4), int64(20)}, {int64(30), int64(8), int64(30)}}
		if err := retained.Err(); err != nil || !reflect.DeepEqual(oldRows, original) {
			t.Fatalf("retained=%#v err=%v", oldRows, err)
		}
		if err := retained.Close(); err != nil {
			t.Fatal(err)
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("unstable redump", err)
		}
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("routing-control-destination")
		if err != nil {
			t.Fatal(err)
		}
		destinationNative := sql.OpenDB(connector)
		defer destinationNative.Close()
		wrapped := sqlmw.Driver(driver, fixture.Interceptor{})
		wrappedConnector, err := wrapped.(sqldriver.DriverContext).OpenConnector("routing-control-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(wrappedConnector)
		defer destination.Close()
		if err := stuntgres.Restore(p.Ctx, destinationNative, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(destination, &fixture.Recorder{}, false)
		if p.Postgres != nil {
			oracle, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer oracle.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, q := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
					if _, err := oracle.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			if _, err := oracle.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("PostgreSQL rejected unmodified dump", err)
			}
			check(oracle, p.PostgresRecord, true)
		}
		for _, failure := range []fixture.Step{
			referenceRangeError("INSERT INTO pr_constraint_low VALUES(90,3,90);", "23514", `new row for relation "pr_constraint_low" violates check constraint "pr_source"`, fixture.Diagnostic{Schema: "public", Table: "pr_constraint_low", Constraint: "pr_source", Detail: "Failing row contains (90, 3, 90)."}),
			referenceRangeLeafMiss("INSERT INTO pr_constraint_low VALUES(90,0,90);", "pr_constraint_low", "90, 0, 90"),
		} {
			bad := dump.String() + "\n" + failure.Query
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
			fixture.AssertDiagnostic(t, err, failure.Diagnostic)
			check(b.DB, b.Recorder, false)
			redump.Reset()
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal("failed restore replaced data", err)
			}
		}
		fixture.RunStep(t, p.Ctx, move, b.Recorder, fixture.Exec(moveSQL, 1, 2, 10))
		run(fixture.Select("SELECT id,k,payload FROM pr_constraint_low", []string{"id", "k", "payload"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(10), int64(2), int64(10)}}))
	})
}

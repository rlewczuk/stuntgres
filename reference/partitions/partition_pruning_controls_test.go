package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"

	"github.com/ngrok/sqlmw"
)

func referencePruningControlBody(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	referencePruningRangeSetup(run)
	referencePruningInheritanceSetup(run)
	for _, q := range []string{
		"CREATE TABLE pp_control(k INT,payload TEXT) PARTITION BY RANGE(k)",
		"CREATE TABLE pp_control_mid PARTITION OF pp_control FOR VALUES FROM(1) TO(4) PARTITION BY LIST(k)",
		"CREATE TABLE pp_control_hash PARTITION OF pp_control_mid FOR VALUES IN(1,2) PARTITION BY HASH(k)",
		"CREATE TABLE pp_control_leaf PARTITION OF pp_control_hash FOR VALUES WITH(MODULUS 1,REMAINDER 0)",
		"CREATE TABLE pp_control_three PARTITION OF pp_control_mid DEFAULT",
		"CREATE TABLE pp_control_def PARTITION OF pp_control DEFAULT",
		"CREATE TYPE pp_control_color AS ENUM('red','green','blue')",
		"CREATE DOMAIN pp_control_id AS BIGINT",
		"CREATE TABLE pp_control_enum(k pp_control_color) PARTITION BY LIST(k)",
		"CREATE TABLE pp_control_enum_a PARTITION OF pp_control_enum FOR VALUES IN('green',NULL)",
		"CREATE TABLE pp_control_domain(k pp_control_id) PARTITION BY RANGE(k)",
		"CREATE TABLE pp_control_domain_a PARTITION OF pp_control_domain FOR VALUES FROM(9007199254740993) TO(9007199254740995)",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO pp_control VALUES(1,'a'),(1,'a'),(3,'boundary'),(5,'default'),(NULL,'null')", 5))
	run(fixture.Exec("INSERT INTO pp_control_enum VALUES('green'),(NULL)", 2))
	run(fixture.Exec("INSERT INTO pp_control_domain VALUES(9007199254740994)", 1))
	query := "SELECT k,payload FROM pp_control ORDER BY k NULLS LAST,payload"
	want := fixture.Select(query, []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(3), "boundary"}, {int64(5), "default"}, {nil, "null"}})
	check := func(db fixture.Executor, record *fixture.Recorder, pg bool) {
		checkRun := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, db, record, s) }
		s := want
		s.CheckMetadata = referenceRangeMetadata(pg, s.Columns, s.Types, []bool{true, true})
		checkRun(s)
		referencePruningKeys(checkRun, pg, "SELECT k FROM pp_control WHERE k=1", int64(1), int64(1))
		referencePruningKeys(checkRun, pg, "SELECT k FROM pp_control WHERE k=3", int64(3))
		referencePruningKeys(checkRun, pg, "SELECT k FROM pp_control WHERE k=5", int64(5))
		referencePruningKeys(checkRun, pg, "SELECT k FROM pp_control WHERE k IS NULL", nil)
		referencePruningKeys(checkRun, pg, "SELECT k FROM pp_control WHERE k=99")
		referencePruningInheritanceChecks(checkRun, pg)
		checkRun(fixture.Select("SELECT k::TEXT AS k FROM pp_control_enum WHERE k='green'::pp_control_color", []string{"k"}, []string{"TEXT"}, [][]any{{"green"}}))
		checkRun(fixture.Select("SELECT k::TEXT AS k FROM pp_control_domain WHERE k=9007199254740994::pp_control_id", []string{"k"}, []string{"TEXT"}, [][]any{{"9007199254740994"}}))
	}
	check(b.DB, b.Recorder, b.PG)
	referencePruningHeld(t, p, b, "pp_control", []any{1, 3, 5, nil, 1}, [][]any{{int64(1), int64(1)}, {int64(3)}, {int64(5)}, nil, {int64(1), int64(1)}})
	if b.PG {
		return
	} // Native controls have no PostgreSQL API equivalent.
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	old, err := b.DB.QueryContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	columns, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	heldQuery := "SELECT k FROM pp_control WHERE k=$1"
	stmt, err := b.DB.PrepareContext(p.Ctx, heldQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: heldQuery}
	heldCheck := func(key int, keys ...int64) {
		s := referenceRangeRows(heldQuery, keys...)
		s.Args = []any{key}
		s.CheckMetadata = referenceRangeMetadata(false, s.Columns, s.Types, []bool{true})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	heldCheck(1, 1, 1)
	run(fixture.Exec("INSERT INTO pp_control VALUES(1,'later')", 1))
	heldCheck(1, 1, 1, 1)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(b.DB, b.Recorder, false)
	heldCheck(5, 5)
	heldCheck(99)
	heldCheck(1, 1, 1)
	fixture.TableDDLColumnTypes(t, false, columns, want.Columns, want.Types, []bool{true, true})
	var retained [][]any
	for old.Next() {
		var k, payload any
		if err := old.Scan(&k, &payload); err != nil {
			t.Fatal(err)
		}
		retained = append(retained, []any{k, payload})
	}
	if err := old.Err(); err != nil || !reflect.DeepEqual(retained, want.Rows) {
		t.Fatalf("old rows=%#v err=%v", retained, err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatalf("round trip changed persistent state: %v", err)
	}
	// A genuinely independent public driver owns destination type identities.
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector("partition-pruning-destination")
	if err != nil {
		t.Fatal(err)
	}
	destinationNative := sql.OpenDB(connector)
	defer destinationNative.Close()
	wrapped := sqlmw.Driver(driver, fixture.Interceptor{})
	configured, err := wrapped.(sqldriver.DriverContext).OpenConnector("partition-pruning-destination")
	if err != nil {
		t.Fatal(err)
	}
	destination := sql.OpenDB(configured)
	defer destination.Close()
	if _, err := destination.ExecContext(p.Ctx, "CREATE TYPE allocator_history AS ENUM('old')"); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, destinationNative, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(destination, &fixture.Recorder{}, false)
	for _, q := range []string{"INSERT INTO pp_control_enum VALUES('green')", "INSERT INTO pp_control_domain VALUES(9007199254740993)"} {
		if _, err := destination.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	// Execute the actual emitted stream, not a hand-written approximation.
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
			t.Fatal("PostgreSQL rejected actual pruning dump", err)
		}
		check(oracle, p.PostgresRecord, true)
	}
}

func TestReferencePartitionPruningControls(t *testing.T) {
	referencePruningScenario(t, referencePruningControlBody)
}

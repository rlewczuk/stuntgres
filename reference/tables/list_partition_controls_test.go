package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"github.com/ngrok/sqlmw"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func referenceListSmokeBody(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	for _, q := range []string{
		"CREATE TABLE lp_smoke(k INT,payload TEXT) PARTITION BY LIST(k)",
		"CREATE TABLE lp_smoke_a PARTITION OF lp_smoke FOR VALUES IN(1,1,NULL,NULL) PARTITION BY LIST(payload)",
		"CREATE TABLE lp_smoke_ax PARTITION OF lp_smoke_a FOR VALUES IN('x')",
		"CREATE TABLE lp_smoke_ad PARTITION OF lp_smoke_a DEFAULT",
		"CREATE TABLE lp_smoke_def PARTITION OF lp_smoke DEFAULT",
	} {
		run(fixture.Exec(q, 0))
	}
	// Both routes execute in every body, regardless of the outer DDL route.
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO lp_smoke VALUES(1,'x'),(NULL,'n'),(7,'d')", 3))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO lp_smoke VALUES($1,$2)", 1, 1, "other")))
	want := fixture.Select("SELECT k,payload FROM lp_smoke ORDER BY k NULLS LAST,payload", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "other"}, {int64(1), "x"}, {int64(7), "d"}, {nil, "n"}})
	check := func(db fixture.Executor, recorder *fixture.Recorder, pg bool) {
		s := want
		s.CheckMetadata = referenceRangeMetadata(pg, s.Columns, s.Types, []bool{true, true})
		fixture.RunStep(t, p.Ctx, db, recorder, s)
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT k,payload FROM lp_smoke_ax", s.Columns, s.Types, [][]any{{int64(1), "x"}}))
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT k,payload FROM lp_smoke_ad ORDER BY k NULLS LAST", s.Columns, s.Types, [][]any{{int64(1), "other"}, {nil, "n"}}))
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT k,payload FROM lp_smoke_def", s.Columns, s.Types, [][]any{{int64(7), "d"}}))
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT k,payload FROM ONLY lp_smoke", s.Columns, s.Types, nil))
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT c.relname,p.relname AS parent FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid JOIN pg_class p ON p.oid=i.inhparent WHERE p.relname IN ('lp_smoke','lp_smoke_a') ORDER BY c.relname", []string{"relname", "parent"}, []string{"NAME", "NAME"}, [][]any{{"lp_smoke_a", "lp_smoke"}, {"lp_smoke_ad", "lp_smoke_a"}, {"lp_smoke_ax", "lp_smoke_a"}, {"lp_smoke_def", "lp_smoke"}}))
	}
	check(b.DB, b.Recorder, b.PG)
	q := "CREATE TABLE bad PARTITION OF lp_smoke FOR VALUES IN(1)"
	run(referenceRangeError(q, "42P17", `partition "bad" would overlap partition "lp_smoke_a"`, fixture.Diagnostic{Position: referenceRangePosition(q, "1)")}))
	referenceListAbsent(run, "bad")
	run(referenceRangeError("CREATE TABLE bad PARTITION OF lp_smoke FOR VALUES IN(7)", "23514", `updated partition constraint for default partition "lp_smoke_def" would be violated by some row`, fixture.Diagnostic{Schema: "public", Table: "lp_smoke_def"}))
	referenceListAbsent(run, "bad")
	check(b.DB, b.Recorder, b.PG)
	run(fixture.Exec("CREATE TABLE bad PARTITION OF lp_smoke FOR VALUES IN(8)", 0))
	// Remove the successful name-reuse witness so the exact topology check stays fixed.
	run(fixture.Exec("DROP TABLE bad", 0))
	if b.PG {
		return
	} // Native controls are not a PostgreSQL API.
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	old, err := b.DB.QueryContext(p.Ctx, want.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	held, err := b.DB.PrepareContext(p.Ctx, want.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(b.DB, b.Recorder, false)
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: want.Query}, b.Recorder, want)
	var retained [][]any
	for old.Next() {
		var k, payload any
		if err := old.Scan(&k, &payload); err != nil {
			t.Fatal(err)
		}
		retained = append(retained, []any{k, payload})
	}
	if err := old.Err(); err != nil || !reflect.DeepEqual(retained, want.Rows) {
		t.Fatalf("retained=%#v err=%v", retained, err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatalf("redump differs: %v\n%s", err, redump.String())
	}
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector("list-smoke-destination")
	if err != nil {
		t.Fatal(err)
	}
	destinationNative := sql.OpenDB(connector)
	defer destinationNative.Close()
	wrapped := sqlmw.Driver(driver, fixture.Interceptor{})
	configured, err := wrapped.(sqldriver.DriverContext).OpenConnector("list-smoke-destination")
	if err != nil {
		t.Fatal(err)
	}
	destination := sql.OpenDB(configured)
	defer destination.Close()
	// Advance an independent destination's allocator before replacement.
	if _, err := destination.ExecContext(p.Ctx, "CREATE TABLE allocator_history(k INT)"); err != nil {
		t.Fatal(err)
	}
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
			t.Fatal("PostgreSQL rejected actual LIST dump", err)
		}
		check(oracle, p.PostgresRecord, true)
	}
}

func TestReferenceListPartitionFeatureSmoke(t *testing.T) {
	referenceListScenario(t, referenceListSmokeBody)
}

func TestReferenceListPartitionControls(t *testing.T) {
	referenceListScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TYPE lp_control_color AS ENUM('red','green','blue')", 0))
		run(fixture.Exec("CREATE DOMAIN lp_control_id AS BIGINT", 0))
		run(fixture.Exec("CREATE SEQUENCE lp_control_seq START 10", 0))
		for _, c := range []struct{ name, typ, bound, input, output string }{
			{"enum", "lp_control_color", "'green',NULL", "'green'", "green"},
			{"domain", "lp_control_id", "9007199254740993,NULL", "9007199254740993", "9007199254740993"},
			{"once", "BIGINT", "nextval('lp_control_seq')", "10", "10"},
		} {
			run(fixture.Exec("CREATE TABLE lp_control_"+c.name+"(k "+c.typ+") PARTITION BY LIST(k)", 0))
			run(fixture.Exec("CREATE TABLE lp_control_"+c.name+"_leaf PARTITION OF lp_control_"+c.name+" FOR VALUES IN("+c.bound+")", 0))
			run(fixture.Exec("INSERT INTO lp_control_"+c.name+" VALUES("+c.input+")", 1))
		}
		check := func(db fixture.Executor, recorder *fixture.Recorder) {
			for _, c := range []struct{ name, value string }{{"enum", "green"}, {"domain", "9007199254740993"}, {"once", "10"}} {
				fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT k::TEXT AS k FROM lp_control_"+c.name, []string{"k"}, []string{"TEXT"}, [][]any{{c.value}}))
			}
			fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT last_value FROM lp_control_seq", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(10)}}))
		}
		check(b.DB, b.Recorder)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dump.String(), "nextval(") {
			t.Fatal("dump retained a volatile bound expression")
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(b.DB, b.Recorder)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("typed LIST redump differs", err)
		}
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("list-typed-destination")
		if err != nil {
			t.Fatal(err)
		}
		native := sql.OpenDB(connector)
		defer native.Close()
		wrapped := sqlmw.Driver(driver, fixture.Interceptor{})
		configured, err := wrapped.(sqldriver.DriverContext).OpenConnector("list-typed-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(configured)
		defer destination.Close()
		if _, err := destination.ExecContext(p.Ctx, "CREATE TYPE allocator_color AS ENUM('before','after')"); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		destinationRecord := &fixture.Recorder{}
		check(destination, destinationRecord)
		redump.Reset()
		if err := stuntgres.Dump(p.Ctx, native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("independent typed LIST redump differs", err)
		}
		fixture.RunStep(t, p.Ctx, destination, destinationRecord, fixture.Prepared(fixture.Exec("INSERT INTO lp_control_enum VALUES(NULL)", 1)))
		fixture.RunStep(t, p.Ctx, destination, destinationRecord, fixture.Prepared(fixture.Exec("INSERT INTO lp_control_domain VALUES(NULL)", 1)))
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
				t.Fatal("PostgreSQL rejected actual typed LIST dump", err)
			}
			check(oracle, p.PostgresRecord)
		}
		run(fixture.Exec("INSERT INTO lp_control_enum VALUES(NULL)", 1))
		run(fixture.Exec("INSERT INTO lp_control_domain VALUES(NULL)", 1))
	})
}

func referenceListReplayLocalCheck(t *testing.T, p *fixture.Pair, db fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT conname,connoinherit,conislocal FROM pg_constraint WHERE conrelid='lp_replay_a'::regclass::OID AND contype='c'", []string{"conname", "connoinherit", "conislocal"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"lp_local", true, true}}))
}

func TestReferenceListPartitionReplayBoundary(t *testing.T) {
	referenceListScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		for _, q := range []string{
			"CREATE TABLE lp_replay(k INT,payload TEXT) PARTITION BY LIST(k)",
			"CREATE TABLE lp_replay_a PARTITION OF lp_replay(CONSTRAINT lp_local CHECK(k IS NULL OR k>0) NO INHERIT) FOR VALUES IN(1,NULL)",
			"CREATE TABLE lp_replay_def PARTITION OF lp_replay DEFAULT",
			"CREATE TABLE lp_candidate(k INT,payload TEXT)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO lp_replay VALUES(1,'one'),(7,'default')", 2))
		referenceListReplayLocalCheck(t, p, b.DB, b.Recorder)
		q := "ALTER TABLE lp_replay ATTACH PARTITION lp_candidate FOR VALUES IN(8)"
		if b.PG {
			run(fixture.Exec(q, 0))
			run(fixture.Exec("INSERT INTO lp_replay VALUES(8,'attached')", 1))
			run(referenceRangeRows("SELECT k FROM lp_candidate", 8))
			return
		}
		run(referenceRangeError(q, "0A000", "list partition attachment is unsupported", fixture.Diagnostic{}))
		run(fixture.Select("SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid WHERE c.relname='lp_candidate'", []string{"relname"}, []string{"NAME"}, nil))
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "ATTACH PARTITION") {
			t.Fatal("fallback witness did not emit actual private attachment")
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check := func() {
			run(fixture.Select("SELECT k,payload FROM lp_replay ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(7), "default"}}))
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
				t.Fatalf("restore changed retained catalog: %v\n%s", err, redump.String())
			}
		}
		check()
		referenceListReplayLocalCheck(t, p, b.DB, b.Recorder)
		for _, c := range []struct {
			suffix, token string
			fields        fixture.Diagnostic
		}{
			{"CREATE TABLE bad PARTITION OF lp_replay FOR VALUES IN(1);", "1);", fixture.Diagnostic{Code: "42P17", Message: `partition "bad" would overlap partition "lp_replay_a"`}},
			{"CREATE TABLE bad PARTITION OF lp_replay FOR VALUES IN(7);", "", fixture.Diagnostic{Code: "23514", Message: `updated partition constraint for default partition "lp_replay_def" would be violated by some row`, Schema: "public", Table: "lp_replay_def"}},
		} {
			script := dump.String() + c.suffix
			want := c.fields
			want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
			if c.token != "" {
				want.Position = referenceRangePosition(script, c.token)
			}
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &want)
			check()
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		reader := &referenceRangeCancelEOF{Reader: strings.NewReader(dump.String()), cancel: cancel}
		if err := stuntgres.Restore(ctx, p.Native, reader); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled EOF: %v", err)
		}
		check()
		ioFailure := errors.New("LIST restore I/O sentinel")
		if err := stuntgres.Restore(p.Ctx, p.Native, &referenceRangeErrorReader{err: ioFailure}); !errors.Is(err, ioFailure) {
			t.Fatal(err)
		}
		check()
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
				t.Fatal("PostgreSQL rejected actual private LIST replay", err)
			}
			fixture.RunStep(t, p.Ctx, oracle, p.PostgresRecord, fixture.Select("SELECT k,payload FROM lp_replay ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(7), "default"}}))
			referenceListReplayLocalCheck(t, p, oracle, p.PostgresRecord)
		}
	})
}

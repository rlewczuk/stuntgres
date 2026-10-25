package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"github.com/ngrok/sqlmw"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func referenceRangeControlBody(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE TYPE rp_control_color AS ENUM('red','green','blue')", 0))
	run(fixture.Exec("CREATE DOMAIN rp_control_id AS BIGINT", 0))
	run(fixture.Exec("CREATE TABLE rp_control(k INT,payload TEXT DEFAULT 'parent',CHECK(k IS NULL OR k>0)) PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE rp_control_a PARTITION OF rp_control(payload DEFAULT 'leaf') FOR VALUES FROM(1) TO(3)", 0))
	run(fixture.Exec("CREATE TABLE rp_control_def PARTITION OF rp_control DEFAULT PARTITION BY RANGE((k+1))", 0))
	run(fixture.Exec("CREATE TABLE rp_control_deep PARTITION OF rp_control_def DEFAULT", 0))
	run(fixture.Exec("COMMENT ON TABLE rp_control IS 'range root'", 0))
	run(fixture.Exec("COMMENT ON COLUMN rp_control.payload IS 'payload comment'", 0))
	run(fixture.Exec("INSERT INTO rp_control(k) VALUES(1),(1),(5),(NULL)", 4))
	run(fixture.Exec("INSERT INTO rp_control_a(k) VALUES(2)", 1))
	run(fixture.Exec("CREATE TABLE rp_control_enum(k rp_control_color) PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE rp_control_enum_leaf PARTITION OF rp_control_enum FOR VALUES FROM('red') TO('blue')", 0))
	run(fixture.Exec("INSERT INTO rp_control_enum VALUES('green')", 1))
	run(fixture.Exec("CREATE TABLE rp_control_domain(k rp_control_id) PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE rp_control_domain_leaf PARTITION OF rp_control_domain FOR VALUES FROM(9007199254740993) TO(9007199254740995)", 0))
	run(fixture.Exec("INSERT INTO rp_control_domain VALUES(9007199254740994)", 1))
	run(fixture.Exec("CREATE SEQUENCE rp_control_seq START 10", 0))
	run(fixture.Exec("CREATE TABLE rp_control_once(k BIGINT) PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE rp_control_once_leaf PARTITION OF rp_control_once FOR VALUES FROM(nextval('rp_control_seq')) TO(20)", 0))
	run(fixture.Exec("INSERT INTO rp_control_once VALUES(10)", 1))
	run(fixture.Exec("CREATE TABLE rp_control_exclusion(k INT,payload INT) PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE rp_control_exclusion_leaf PARTITION OF rp_control_exclusion(CONSTRAINT rp_control_exclusion_key EXCLUDE USING btree(payload WITH =)) FOR VALUES FROM(5) TO(8)", 0))
	run(fixture.Exec("INSERT INTO rp_control_exclusion VALUES(6,42)", 1))
	query := "SELECT k,payload FROM rp_control ORDER BY k NULLS LAST,payload"
	want := fixture.Select(query, []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "parent"}, {int64(1), "parent"}, {int64(2), "leaf"}, {int64(5), "parent"}, {nil, "parent"}})
	want.CheckMetadata = referenceRangeMetadata(b.PG, want.Columns, want.Types, []bool{true, true})
	run(want)
	check := func(db fixture.Executor, record *fixture.Recorder, pg bool, owner string) {
		s := want
		s.CheckMetadata = referenceRangeMetadata(pg, s.Columns, s.Types, []bool{true, true})
		fixture.RunStep(t, p.Ctx, db, record, s)
		fixture.RunStep(t, p.Ctx, db, record, fixture.Select("SELECT * FROM rp_control_exclusion", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(6), int64(42)}}))
		fixture.RunStep(t, p.Ctx, db, record, fixture.ErrorDiagnostic("INSERT INTO rp_control_exclusion VALUES(7,42)", fixture.ExclusionReferenceConflict("rp_control_exclusion_leaf", "rp_control_exclusion_key", "payload", "42", "42")))
		fixture.RunStep(t, p.Ctx, db, record, fixture.Select("SELECT k::TEXT AS k FROM rp_control_enum", []string{"k"}, []string{"TEXT"}, [][]any{{"green"}}))
		fixture.RunStep(t, p.Ctx, db, record, fixture.Select("SELECT k::TEXT AS k FROM rp_control_domain", []string{"k"}, []string{"TEXT"}, [][]any{{"9007199254740994"}}))
		fixture.RunStep(t, p.Ctx, db, record, fixture.Select("SELECT last_value FROM rp_control_seq", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(10)}}))
		fixture.RunStep(t, p.Ctx, db, record, fixture.Select("SELECT obj_description('rp_control'::regclass,'pg_class') AS comment", []string{"comment"}, []string{"TEXT"}, [][]any{{"range root"}}))
		fixture.RunStep(t, p.Ctx, db, record, fixture.Select("SELECT col_description('rp_control'::regclass,2) AS comment", []string{"comment"}, []string{"TEXT"}, [][]any{{"payload comment"}}))
		fixture.RunStep(t, p.Ctx, db, record, fixture.Select("SELECT c.relname,c.relowner=p.relowner AS same_owner FROM pg_class c JOIN pg_class p ON p.relname='rp_control' WHERE c.relname='rp_control' OR c.relname='rp_control_a' OR c.relname='rp_control_def' OR c.relname='rp_control_deep' ORDER BY c.relname", []string{"relname", "same_owner"}, []string{"NAME", "BOOL"}, [][]any{{"rp_control", true}, {"rp_control_a", true}, {"rp_control_deep", true}, {"rp_control_def", true}}))
		fixture.RunStep(t, p.Ctx, db, record, fixture.Select("SELECT tableowner AS rolname FROM pg_tables WHERE schemaname='public' AND tablename='rp_control'", []string{"rolname"}, []string{"NAME"}, [][]any{{owner}}))
	}
	owner := "postgres"
	if b.PG {
		owner = "stuntgres"
	}
	check(b.DB, b.Recorder, b.PG, owner)
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
	held, err := b.DB.PrepareContext(p.Ctx, query)
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
	check(b.DB, b.Recorder, false, "postgres")
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: query}, b.Recorder, want)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatalf("redump differs: %v\n%s", err, redump.String())
	}
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
		t.Fatalf("retained=%#v err=%v", retained, err)
	}
	// Independent destination uses the actual public Driver/Connector, not the
	// source fixture's driver. Enum/domain keys must be retagged after Reset.
	destinationDriver := stuntgres.NewDriver()
	connector, err := destinationDriver.OpenConnector("range-control-destination")
	if err != nil {
		t.Fatal(err)
	}
	destinationNative := sql.OpenDB(connector)
	defer destinationNative.Close()
	wrapped := sqlmw.Driver(destinationDriver, fixture.Interceptor{})
	configured, err := wrapped.(sqldriver.DriverContext).OpenConnector("range-control-destination")
	if err != nil {
		t.Fatal(err)
	}
	destination := sql.OpenDB(configured)
	defer destination.Close()
	if err := stuntgres.Restore(p.Ctx, destinationNative, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	check(destination, &fixture.Recorder{}, false, "postgres")
	for _, q := range []string{"INSERT INTO rp_control_enum VALUES('red')", "INSERT INTO rp_control_domain VALUES(9007199254740993)"} {
		if _, err := destination.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	// Actual emitted SQL is consumed by pinned PostgreSQL, then removed before
	// the shared PostgreSQL body begins. Cleanup is registered before replay.
	if p.Postgres != nil {
		oracle, err := p.Postgres.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer oracle.Close()
		// Smoke composes this body with other RANGE witnesses in the same
		// native catalog, so replay cleanup owns the complete fixture schema.
		cleanup := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, query := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
				if _, err := oracle.ExecContext(ctx, query); err != nil {
					t.Error(err)
				}
			}
		}
		defer cleanup()
		if _, err := oracle.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("PostgreSQL rejected actual hierarchy dump", err)
		}
		// The native stream preserves its named postgres owner on PostgreSQL;
		// this differs from ordinary fixture DDL owned by stuntgres.
		check(oracle, p.PostgresRecord, true, "postgres")
	}
	for _, c := range []struct {
		suffix, token string
		diagnostic    fixture.Diagnostic
	}{
		{"CREATE TABLE rp_bad PARTITION OF rp_control FOR VALUES FROM(1) TO(2);", "1)", fixture.Diagnostic{Code: "42P17", Message: `partition "rp_bad" would overlap partition "rp_control_a"`}},
		{"CREATE TABLE rp_bad PARTITION OF rp_control FOR VALUES FROM(5) TO(7);", "", fixture.Diagnostic{Code: "23514", Message: `updated partition constraint for default partition "rp_control_def" would be violated by some row`, Schema: "public", Table: "rp_control_def"}},
		{"CREATE TABLE rp_bad PARTITION OF rp_control FOR VALUES FROM(NULL) TO(7);", "", fixture.Diagnostic{Code: "42P17", Message: "cannot specify NULL in range bound"}},
		{"CREATE TABLE rp_bad(k INT) garbage;", "garbage", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "garbage"`}},
	} {
		script := dump.String() + c.suffix
		want := c.diagnostic
		want.Severity = "ERROR"
		want.SeverityUnlocalized = "ERROR"
		if c.token != "" {
			want.Position = referenceRangePosition(script, c.suffix[strings.Index(c.suffix, c.token):])
		}
		err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
		fixture.AssertDiagnostic(t, err, &want)
		check(b.DB, b.Recorder, false, "postgres")
		redump.Reset()
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
			t.Fatal("failed import changed hierarchy", err)
		}
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	reader := &referenceRangeCancelEOF{Reader: strings.NewReader(dump.String()), cancel: cancel}
	if err := stuntgres.Restore(ctx, p.Native, reader); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled EOF import: %v", err)
	}
	cancel()
	check(b.DB, b.Recorder, false, "postgres")
	ioFailure := errors.New("range import I/O sentinel")
	if err := stuntgres.Restore(p.Ctx, p.Native, &fixture.ReferenceRangeErrorReader{Err: ioFailure}); !errors.Is(err, ioFailure) {
		t.Fatal(err)
	}
	check(b.DB, b.Recorder, false, "postgres")
	// Blocked reader proves target catalog reads do not wait behind external I/O.
	gate := &fixture.ReferenceRangeGateReader{Entered: make(chan struct{}), Release: make(chan struct{}), Reader: strings.NewReader(dump.String())}
	ctx, cancel = context.WithCancel(p.Ctx)
	defer cancel()
	result := make(chan error, 1)
	joined := false
	released := false
	defer func() {
		cancel()
		if !released {
			close(gate.Release)
		}
		if !joined {
			select {
			case <-result:
			case <-time.After(5 * time.Second):
				t.Error("blocked import did not join")
			}
		}
	}()
	go func() { result <- stuntgres.Restore(ctx, p.Native, gate) }()
	select {
	case <-gate.Entered:
	case <-time.After(5 * time.Second):
		t.Fatal("import never entered reader")
	}
	readCtx, readCancel := context.WithTimeout(p.Ctx, time.Second)
	defer readCancel()
	var count int
	if err := b.DB.QueryRowContext(readCtx, "SELECT COUNT(*) FROM rp_control").Scan(&count); err != nil || count != 5 {
		t.Fatalf("blocked I/O held target locks: count=%d err=%v", count, err)
	}
	cancel()
	close(gate.Release)
	released = true
	select {
	case err := <-result:
		joined = true
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled reader did not join")
	}
	check(b.DB, b.Recorder, false, "postgres")
	// Dump captures before entering external writer I/O. A real routed write
	// must finish while that writer is blocked, without altering its snapshot.
	var snapshot bytes.Buffer
	writer := &referenceRangeGateWriter{Writer: &snapshot, entered: make(chan struct{}), release: make(chan struct{})}
	dumpResult := make(chan error, 1)
	dumpJoined := false
	writerReleased := false
	dumpCtx, dumpCancel := context.WithCancel(p.Ctx)
	defer dumpCancel()
	defer func() {
		dumpCancel()
		if !writerReleased {
			close(writer.release)
		}
		if !dumpJoined {
			select {
			case <-dumpResult:
			case <-time.After(5 * time.Second):
				t.Error("blocked dump did not join")
			}
		}
	}()
	go func() { dumpResult <- stuntgres.Dump(dumpCtx, p.Native, writer) }()
	select {
	case <-writer.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("dump never entered writer")
	}
	writeCtx, writeCancel := context.WithTimeout(p.Ctx, time.Second)
	defer writeCancel()
	if _, err := b.DB.ExecContext(writeCtx, "INSERT INTO rp_control VALUES(6,'concurrent')"); err != nil {
		t.Fatal("dump writer held target locks", err)
	}
	close(writer.release)
	writerReleased = true
	select {
	case err := <-dumpResult:
		dumpJoined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dump writer did not join")
	}
	if snapshot.String() != dump.String() {
		t.Fatal("blocked dump did not retain captured hierarchy/data")
	}
	if err := stuntgres.Restore(p.Ctx, destinationNative, bytes.NewReader(snapshot.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(destination, &fixture.Recorder{}, false, "postgres")
	run(fixture.Select("SELECT COUNT(*) FROM rp_control", []string{"count"}, []string{"INT8"}, [][]any{{int64(6)}}))
	exportFailure := errors.New("range export I/O sentinel")
	if err := stuntgres.Dump(p.Ctx, p.Native, &referenceRangeErrorWriter{err: exportFailure}); !errors.Is(err, exportFailure) {
		t.Fatal(err)
	}
	run(fixture.Select("SELECT COUNT(*) FROM rp_control", []string{"count"}, []string{"INT8"}, [][]any{{int64(6)}}))
}

func TestReferenceRangePartitionControls(t *testing.T) {
	referenceRangeScenario(t, referenceRangeControlBody)
}

type referenceRangeCancelEOF struct {
	io.Reader
	cancel context.CancelFunc
}

func (r *referenceRangeCancelEOF) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		r.cancel()
	}
	return n, err
}

type referenceRangeGateWriter struct {
	io.Writer
	entered, release chan struct{}
	started          bool
}

func (w *referenceRangeGateWriter) Write(p []byte) (int, error) {
	if !w.started {
		w.started = true
		close(w.entered)
		<-w.release
	}
	return w.Writer.Write(p)
}

type referenceRangeErrorWriter struct{ err error }

func (w *referenceRangeErrorWriter) Write([]byte) (int, error) { return 0, w.err }

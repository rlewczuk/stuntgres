package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/lib/pq"
	"github.com/ngrok/sqlmw"
	"stuntgres/reference/fixture"
)

func triggerWireConnection(t *testing.T, f *fixture.ServerFixture, b fixture.Backend) (*sql.Conn, *[]fixture.Diagnostic) {
	t.Helper()
	records := new([]fixture.Diagnostic)
	base, err := pq.NewConnector(f.DSNFor(b))
	if err != nil {
		t.Fatal(err)
	}
	connector := pq.ConnectorWithNoticeHandler(base, func(n *pq.Error) { *records = append(*records, fixture.ExtractDiagnostic(t, n)) })
	wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{Connector: connector}, fixture.Interceptor{})
	configured, err := wrapped.(driver.DriverContext).OpenConnector("")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(configured)
	t.Cleanup(func() { db.Close() })
	c, err := db.Conn(f.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, records
}

func triggerWireNotices(t *testing.T, pg bool, q string, records []fixture.Diagnostic) {
	t.Helper()
	type occurrence struct {
		at    int
		token [2]string
	}
	var tokens []occurrence
	// Explicit independent expectation table, ordered by lexical occurrence.
	for _, pair := range [][2]string{{triggerLongSource, triggerLongCanonical}, {triggerLongCanonical + "_other", triggerLongCanonical}} {
		for start := 0; start < len(q); {
			i := strings.Index(q[start:], pair[0])
			if i < 0 {
				break
			}
			tokens = append(tokens, occurrence{start + i, pair})
			start += i + len(pair[0])
		}
	}
	sort.Slice(tokens, func(i, j int) bool { return tokens[i].at < tokens[j].at })
	var got []fixture.Diagnostic
	for _, d := range records {
		if pg && d.Severity == "DEBUG" {
			fixture.NoticesInternalDebug(t, d)
			continue
		}
		got = append(got, d)
	}
	if len(got) != len(tokens) {
		t.Fatalf("%s notices: got %#v, want %d", q, got, len(tokens))
	}
	for i, token := range tokens {
		want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: fmt.Sprintf("identifier %q will be truncated to %q", token.token[0], token.token[1])}
		if got[i] != want {
			t.Fatalf("notice %d: got %#v want %#v", i, got[i], want)
		}
	}
}

func triggerWireLifecycle(t *testing.T, f *fixture.ServerFixture, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
	triggerNameSetup(run)
	defer func() {
		c.ExecContext(f.Ctx, "ROLLBACK")
		c.ExecContext(f.Ctx, "DROP TABLE tn_data,tn_other")
		c.ExecContext(f.Ctx, "DROP FUNCTION tn_add()")
	}()
	run(fixture.Exec(triggerNameCreate("tn_data", triggerLongSource, "3"), 0))
	run(fixture.Exec(triggerNameCreate("tn_other", triggerLongSource, "11"), 0))
	oid := func(name string) int64 {
		var value int64
		if err := c.QueryRowContext(f.Ctx, "SELECT oid FROM pg_trigger WHERE tgrelid='tn_data'::regclass AND tgname=$1", name).Scan(&value); err != nil || value <= 0 {
			t.Fatalf("OID %d: %v", value, err)
		}
		return value
	}
	original := oid(triggerLongCanonical)
	q := "INSERT INTO tn_data VALUES($1,5,NULL) RETURNING id,v,mark"
	stmt, err := c.PrepareContext(f.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	fire := func(id, value int64, name any) {
		s := triggerNameRows(q, []any{id, value, name})
		s.Args = []any{id}
		fixture.RunStep(t, f.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
	}
	stable := func(name string) {
		if oid(name) != original {
			t.Fatal("trigger identity changed")
		}
		triggerNameCatalog(run, "tn_data", name)
	}
	fire(1, 8, triggerLongCanonical)
	run(triggerNameError(triggerNameCreate("tn_data", triggerLongCanonical+"_other", "7"), "42710", `trigger "`+triggerLongCanonical+`" for relation "tn_data" already exists`))
	stable(triggerLongCanonical)
	fire(2, 8, triggerLongCanonical)
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("SAVEPOINT duplicate", 0))
	run(triggerNameError(triggerNameCreate("tn_data", triggerLongSource, "7"), "42710", `trigger "`+triggerLongCanonical+`" for relation "tn_data" already exists`))
	run(fixture.Exec("ROLLBACK TO duplicate", 0))
	stable(triggerLongCanonical)
	run(fixture.Exec("ROLLBACK", 0))
	replace := strings.Replace(triggerNameCreate("tn_data", triggerLongCanonical+"_other", "7"), "CREATE TRIGGER", "CREATE OR REPLACE TRIGGER", 1)
	run(fixture.Exec(replace, 0))
	stable(triggerLongCanonical)
	fire(3, 12, triggerLongCanonical)
	run(fixture.Select("SELECT t.tgtype,t.tgnargs,p.proname,t.tgqual IS NOT NULL AS has_when FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid WHERE t.tgrelid='tn_data'::regclass", []string{"tgtype", "tgnargs", "proname", "has_when"}, []string{"INT2", "INT2", "NAME", "BOOL"}, [][]any{{int64(23), int64(1), "tn_add", true}}))
	run(fixture.Exec("ALTER TRIGGER "+triggerLongSource+" ON tn_data RENAME TO "+triggerLongCanonical+"_other", 0))
	stable(triggerLongCanonical)
	run(fixture.Exec("ALTER TRIGGER "+triggerLongSource+" ON tn_data RENAME TO short", 0))
	stable("short")
	fire(4, 12, "short")
	run(fixture.Exec(triggerNameCreate("tn_data", triggerLongSource, "3"), 0))
	collisionOID := oid(triggerLongCanonical)
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("SAVEPOINT collision", 0))
	run(triggerNameError("ALTER TRIGGER short ON tn_data RENAME TO "+triggerLongSource, "42710", `trigger "`+triggerLongCanonical+`" for relation "tn_data" already exists`))
	run(triggerNameError("SELECT 1", "25P02", "current transaction is aborted, commands ignored until end of transaction block"))
	run(fixture.Exec("ROLLBACK TO collision", 0))
	run(fixture.Exec("COMMIT", 0))
	if oid("short") != original || oid(triggerLongCanonical) != collisionOID {
		t.Fatal("rename failure changed identities")
	}
	fire(5, 15, triggerLongCanonical)
	run(fixture.Exec("DROP TRIGGER "+triggerLongSource+" ON tn_data", 0))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("SAVEPOINT lifecycle", 0))
	run(fixture.Exec("ALTER TRIGGER short ON tn_data RENAME TO "+triggerLongSource, 0))
	stable(triggerLongCanonical)
	fire(6, 12, triggerLongCanonical)
	run(fixture.Exec("DROP TRIGGER "+triggerLongSource+" ON tn_data", 0))
	fire(7, 5, nil)
	run(fixture.Exec("ROLLBACK TO lifecycle", 0))
	stable("short")
	fire(8, 12, "short")
	run(fixture.Exec("ROLLBACK", 0))
	stable("short")
	run(fixture.Select("SELECT count(*) FROM tn_data WHERE id>=6", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("ALTER TRIGGER short ON tn_data RENAME TO "+triggerLongSource, 0))
	run(fixture.Exec("COMMIT", 0))
	stable(triggerLongCanonical)
	fire(9, 12, triggerLongCanonical)
	run(triggerNameError("DROP TRIGGER short ON tn_data", "42704", `trigger "short" for table "tn_data" does not exist`))
	run(fixture.Exec("DROP TRIGGER "+triggerLongSource+" ON tn_data", 0))
	fire(10, 5, nil)
	run(triggerNameRows("INSERT INTO tn_other VALUES(1,5,NULL) RETURNING id,v,mark", []any{int64(1), int64(16), triggerLongCanonical}))
}

func triggerWirePartition(t *testing.T, f *fixture.ServerFixture, c *sql.Conn, run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE wire_partition(id integer,v integer,mark text) PARTITION BY RANGE(id)", 0))
	run(fixture.Exec("CREATE TABLE wire_partition_child PARTITION OF wire_partition FOR VALUES FROM (0) TO (100)", 0))
	run(fixture.Exec("CREATE FUNCTION wire_partition_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+TG_ARGV[0]::integer; NEW.mark:=TG_NAME; RETURN NEW; END$$", 0))
	defer func() {
		c.ExecContext(f.Ctx, "ROLLBACK")
		c.ExecContext(f.Ctx, "DROP TABLE wire_partition")
		c.ExecContext(f.Ctx, "DROP FUNCTION wire_partition_fn()")
	}()
	create := "CREATE TRIGGER " + triggerLongSource + " BEFORE INSERT ON wire_partition FOR EACH ROW EXECUTE FUNCTION wire_partition_fn('3')"
	run(fixture.Exec(create, 0))
	var root, child int64
	if err := c.QueryRowContext(f.Ctx, "SELECT p.oid,c.oid FROM pg_trigger p JOIN pg_trigger c ON c.tgparentid=p.oid WHERE p.tgrelid='wire_partition'::regclass").Scan(&root, &child); err != nil || root <= 0 || child <= 0 || root == child {
		t.Fatalf("clone identities %d/%d: %v", root, child, err)
	}
	check := func(name string) {
		for _, table := range []string{"wire_partition", "wire_partition_child"} {
			triggerNameCatalog(run, table, name)
		}
		run(fixture.Select(fmt.Sprintf("SELECT p.oid=%d AS root_same,c.oid=%d AS child_same FROM pg_trigger p JOIN pg_trigger c ON c.tgparentid=p.oid WHERE p.tgrelid='wire_partition'::regclass", root, child), []string{"root_same", "child_same"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	}
	check(triggerLongCanonical)
	run(triggerNameRows("INSERT INTO wire_partition VALUES(1,5,NULL) RETURNING id,v,mark", []any{int64(1), int64(8), triggerLongCanonical}))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec(strings.Replace(strings.Replace(create, "CREATE TRIGGER", "CREATE OR REPLACE TRIGGER", 1), "('3')", "('7')", 1), 0))
	check(triggerLongCanonical)
	run(fixture.Exec("ALTER TRIGGER "+triggerLongSource+" ON wire_partition RENAME TO short", 0))
	check("short")
	run(triggerNameRows("INSERT INTO wire_partition_child VALUES(2,5,NULL) RETURNING id,v,mark", []any{int64(2), int64(12), "short"}))
	run(fixture.Exec("COMMIT", 0))
	run(fixture.Exec("ALTER TRIGGER short ON wire_partition RENAME TO "+triggerLongSource, 0))
	check(triggerLongCanonical)
	run(fixture.Exec("DROP TRIGGER "+triggerLongSource+" ON wire_partition", 0))
	run(fixture.Select("SELECT count(*) FROM pg_trigger WHERE tgrelid IN ('wire_partition'::regclass,'wire_partition_child'::regclass)", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	run(triggerNameRows("INSERT INTO wire_partition VALUES(3,5,NULL) RETURNING id,v,mark", []any{int64(3), int64(5), nil}))
}

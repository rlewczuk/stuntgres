package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionRenameColumnPendingDetach(t *testing.T) {
	for _, nested := range []bool{true, false} {
		for _, held := range []bool{false, true} {
			for _, mode := range []string{"recursive", "only", "pending_nonowner", "active_nonowner"} {
				if !nested && mode == "active_nonowner" {
					continue
				}
				name := map[bool]string{true: "active_and_nested_pending", false: "pending_only"}[nested]
				name += "/" + map[bool]string{false: "direct", true: "held_before_marker"}[held] + "/" + mode
				t.Run(name, func(t *testing.T) {
					if mode == "pending_nonowner" || mode == "active_nonowner" {
						fixture.RoleReferenceCleanup(t, []string{"rp_owner", "rp_other"})
					}
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							partitionRenamePendingCase(t, p, b, nested, held, mode)
						})
					}
				})
			}
		}
	}
}

func partitionRenamePendingSetup(run func(fixture.Step), nested bool) {
	run(fixture.Exec("CREATE TABLE dc_parent(k INT,payload TEXT) PARTITION BY RANGE(k)", 0))
	if nested {
		run(fixture.Exec("CREATE TABLE dc_keep PARTITION OF dc_parent FOR VALUES FROM(0) TO(2)", 0))
		run(fixture.Exec("CREATE TABLE dc_leave PARTITION OF dc_parent FOR VALUES FROM(2) TO(4) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE dc_leave_leaf PARTITION OF dc_leave FOR VALUES FROM(2) TO(4)", 0))
	} else {
		run(fixture.Exec("CREATE TABLE dc_leave PARTITION OF dc_parent FOR VALUES FROM(0) TO(4)", 0))
	}
	run(fixture.Exec("INSERT INTO dc_parent VALUES(1,'one'),(2,'two')", 2))
}

// These catalog-only captures never acquire AS on the addressed user tables.
// Keep allocated OIDs relational: the before/after equality proves identity,
// while the SQL assertions below independently specify names and membership.
func partitionRenamePendingCatalog(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) [][]any {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			values[i] = fixture.Normalize(values[i])
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

const partitionRenamePendingAttributes = "SELECT c.relname,c.oid,c.relowner,c.relispartition,a.attnum,a.attname,a.attinhcount,a.attislocal FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid WHERE c.relname IN('dc_parent','dc_keep','dc_leave','dc_leave_leaf') AND a.attnum>0 AND NOT a.attisdropped ORDER BY c.relname,a.attnum"
const partitionRenamePendingEdges = "SELECT inhparent,inhrelid,inhseqno,inhdetachpending FROM pg_inherits WHERE inhparent IN('dc_parent'::regclass,'dc_leave'::regclass) ORDER BY inhparent,inhrelid"

func partitionRenamePendingNames(run func(fixture.Step), nested, renamed bool) {
	key := "k"
	if renamed {
		key = "renamed"
	}
	want := [][]any{{"dc_leave", "k"}, {"dc_parent", key}}
	if nested {
		want = [][]any{{"dc_keep", key}, {"dc_leave", "k"}, {"dc_leave_leaf", "k"}, {"dc_parent", key}}
	}
	run(fixture.Select("SELECT c.relname,a.attname FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid WHERE c.relname IN('dc_parent','dc_keep','dc_leave','dc_leave_leaf') AND a.attnum=1 ORDER BY c.relname", []string{"relname", "attname"}, []string{"NAME", "NAME"}, want))
	run(detachConcurrentGraph("dc_parent", "dc_leave", true, true))
	if nested {
		run(detachConcurrentGraph("dc_leave", "dc_leave_leaf", true, false))
	}
}

func partitionRenamePendingCase(t *testing.T, p *fixture.Pair, b fixture.Backend, nested, held bool, mode string) {
	t.Helper()
	admin := detachConcurrentConn(t, p, b)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, s) }
	partitionRenamePendingSetup(run, nested)
	worker := detachConcurrentConn(t, p, b)
	own := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, worker, b.Recorder, s) }
	if mode == "pending_nonowner" || mode == "active_nonowner" {
		for _, q := range []string{
			"CREATE ROLE rp_owner LOGIN", "CREATE ROLE rp_other LOGIN",
			"GRANT USAGE ON SCHEMA public TO rp_owner,rp_other",
			"ALTER TABLE dc_parent OWNER TO rp_owner",
			"ALTER TABLE dc_leave OWNER TO rp_other",
		} {
			run(fixture.Exec(q, 0))
		}
		if nested {
			run(fixture.Exec("ALTER TABLE dc_leave_leaf OWNER TO rp_other", 0))
			owner := "rp_owner"
			if mode == "active_nonowner" {
				owner = "rp_other"
			}
			run(fixture.Exec("ALTER TABLE dc_keep OWNER TO "+owner, 0))
		}
		own(fixture.Exec("SET SESSION AUTHORIZATION rp_owner", 0))
		defer own(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	}

	q := "ALTER TABLE dc_parent RENAME COLUMN k TO renamed"
	if mode == "only" {
		q = "ALTER TABLE ONLY dc_parent RENAME COLUMN k TO renamed"
	}
	// Prepare on the executor that will actually execute, before the marker.
	// Preparing on a pool, or preparing after pending setup, is not this witness.
	var executor fixture.Executor = worker
	if held {
		stmt := partitionRenamePrepare(t, p, worker, q)
		executor = fixture.HeldStatement{Executor: worker, Statement: stmt, SQL: q}
	}
	errors := []fixture.Step{
		referenceRangeError("ALTER TABLE dc_leave RENAME k TO child_k", "42P16", `cannot rename inherited column "k"`, fixture.Diagnostic{}),
		referenceRangeError("ALTER TABLE dc_leave RENAME missing TO child_k", "42703", `column "missing" does not exist`, fixture.Diagnostic{}),
	}
	if mode == "only" {
		code, message := "42703", `column "missing" does not exist`
		if nested {
			code, message = "42P16", `inherited column "missing" must be renamed in child tables too`
		}
		errors = append(errors, referenceRangeError("ALTER TABLE ONLY dc_parent RENAME missing TO x", code, message, fixture.Diagnostic{}))
	}
	errorExecutors := make([]fixture.Executor, len(errors))
	for i, step := range errors {
		errorExecutors[i] = admin
		if held {
			stmt := partitionRenamePrepare(t, p, admin, step.Query)
			errorExecutors[i] = fixture.HeldStatement{Executor: admin, Statement: stmt, SQL: step.Query}
		}
	}

	// This helper observes both the committed marker and the real completion
	// wait, cancels and joins the detach worker, then releases its RR reader.
	detachConcurrentPendingRelations(t, p, b, "dc_parent", "dc_leave", held)
	partitionRenamePendingNames(run, nested, false)
	before := partitionRenamePendingCatalog(t, p, admin, partitionRenamePendingAttributes)
	edges := partitionRenamePendingCatalog(t, p, admin, partitionRenamePendingEdges)
	assertCatalog := func(renamed bool) {
		t.Helper()
		want := make([][]any, len(before))
		for i, row := range before {
			want[i] = append([]any(nil), row...)
			if renamed && (row[0] == "dc_parent" || row[0] == "dc_keep") && row[5] == "k" {
				want[i][5] = "renamed"
			}
		}
		if after := partitionRenamePendingCatalog(t, p, admin, partitionRenamePendingAttributes); !reflect.DeepEqual(want, after) {
			t.Fatal("rename changed names or stable catalog identity", want, after)
		}
		if after := partitionRenamePendingCatalog(t, p, admin, partitionRenamePendingEdges); !reflect.DeepEqual(edges, after) {
			t.Fatal("rename changed canonical membership", edges, after)
		}
		partitionRenamePendingNames(run, nested, renamed)
	}
	for i, step := range errors {
		fixture.RunStep(t, p.Ctx, errorExecutors[i], b.Recorder, step)
		assertCatalog(false)
	}

	// Separate AS holders on the leaving branch and its nested leaf must not
	// participate in root rename admission. Neither holder reads dc_parent.
	var holders []*sql.Tx
	if mode == "recursive" {
		tables := []string{"dc_leave"}
		if nested {
			tables = append(tables, "dc_leave_leaf")
		}
		for _, table := range tables {
			tx, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { tx.Rollback() })
			holders = append(holders, tx)
			values := [][]any{{int64(2)}}
			if !nested {
				values = [][]any{{int64(1)}, {int64(2)}}
			}
			if nested && table == "dc_leave" {
				values = nil
			}
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT k FROM ONLY "+table+" ORDER BY k", []string{"k"}, []string{"INT4"}, values))
		}
		own(fixture.Exec("SET lock_timeout='10ms'", 0))
	}
	step := fixture.Exec(q, 0)
	failed := mode == "active_nonowner" || (mode == "only" && nested)
	if mode == "active_nonowner" {
		step = referenceRangeError(q, "42501", "must be owner of table dc_keep", fixture.Diagnostic{})
	} else if failed {
		step = referenceRangeError(q, "42P16", `inherited column "k" must be renamed in child tables too`, fixture.Diagnostic{})
	}
	fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
	// Inspect while the holders remain active: success cannot be explained by
	// releasing an AS lock before admission. Omitted metadata is unchanged.
	assertCatalog(!failed)
	for _, tx := range holders {
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "recursive" {
		own(fixture.Exec("RESET lock_timeout", 0))
	}
	pendingRows := [][]any{{int64(2), "two"}}
	if !nested {
		pendingRows = [][]any{{int64(1), "one"}, {int64(2), "two"}}
	}
	if !failed {
		rootRows := [][]any(nil)
		if nested {
			rootRows = [][]any{{int64(1), "one"}}
		}
		run(fixture.Select("SELECT * FROM dc_parent ORDER BY renamed", []string{"renamed", "payload"}, []string{"INT4", "TEXT"}, rootRows))
		if nested {
			run(fixture.Select("SELECT * FROM dc_keep", []string{"renamed", "payload"}, []string{"INT4", "TEXT"}, rootRows))
		}
		// Recheck the direct inherited guard while parent/child names diverge.
		fixture.RunStep(t, p.Ctx, errorExecutors[0], b.Recorder, errors[0])
		run(fixture.Exec("ALTER TABLE dc_parent RENAME renamed TO k", 0))
		assertCatalog(false)
	}
	run(fixture.Select("SELECT * FROM dc_leave ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, pendingRows))
	if nested {
		run(fixture.Select("SELECT * FROM dc_leave_leaf", []string{"k", "payload"}, []string{"INT4", "TEXT"}, pendingRows))
	}

	// FINALIZE is required only after the root/active names match again.
	run(fixture.Exec("ALTER TABLE dc_parent DETACH PARTITION dc_leave FINALIZE", 0))
	run(detachConcurrentGraph("dc_parent", "dc_leave", false, false))
	run(fixture.Select("SELECT relispartition FROM pg_class WHERE oid='dc_leave'::regclass", []string{"relispartition"}, []string{"BOOL"}, [][]any{{false}}))
	run(fixture.Select("SELECT attname,attinhcount,attislocal FROM pg_attribute WHERE attrelid='dc_leave'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "attinhcount", "attislocal"}, []string{"NAME", "INT2", "BOOL"}, [][]any{{"k", int64(0), true}, {"payload", int64(0), true}}))
	detachConcurrentExecute(t, p, b, admin, "ALTER TABLE dc_leave RENAME k TO child_k", held)
	values := [][]any{{int64(2), "two"}}
	if !nested {
		values = [][]any{{int64(1), "one"}, {int64(2), "two"}}
	}
	run(fixture.Select("SELECT * FROM dc_leave ORDER BY child_k", []string{"child_k", "payload"}, []string{"INT4", "TEXT"}, values))
	if nested {
		run(detachConcurrentGraph("dc_leave", "dc_leave_leaf", true, false))
		run(fixture.Select("SELECT * FROM dc_leave_leaf", []string{"child_k", "payload"}, []string{"INT4", "TEXT"}, values))
	}
}

package fixture

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

// DefinitionIdentityRows retains every exposed catalog field, including provider,
// owner, encoding and version, rather than comparing only counts or names.
func DefinitionIdentityRows(t *testing.T, ctx context.Context, x Executor, query string, args ...any) []map[string]any {
	t.Helper()
	rows, err := x.QueryContext(ctx, query, args...)
	if err != nil {
		t.Fatal(query, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	out := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		row := map[string]any{}
		for i, name := range columns {
			if bytes, ok := values[i].([]byte); ok {
				values[i] = string(bytes)
			}
			row[name] = values[i]
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func DefinitionIdentityEqual(t *testing.T, got, want []map[string]any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog identity/settings got %#v want %#v", got, want)
	}
}

// DefinitionAlterIdentity is shared only by the two owning reference families.
// PG18.6 discovery (root artifact263) retargets the fallback after a committed
// rename/move. Native deliberately fences the originally admitted identity.
func DefinitionAlterIdentity(t *testing.T, p *Pair, b Backend, family string, action string, held bool, route string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 30*time.Second)
	defer cancel()
	c, holder := LockConnections(t, ctx, b.DB)
	peer := SequenceCoreConn(t, ctx, b.DB)
	run := func(x Executor, step Step) { RunStep(t, ctx, x, b.Recorder, step) }
	for _, ns := range []string{"identity_first", "identity_second", "identity_moved"} {
		run(c, Exec("CREATE SCHEMA "+ns, 0))
	}
	create := func(name string) string {
		if family == "COLLATION" {
			return "CREATE COLLATION " + name + ` FROM pg_catalog."C"`
		}
		return "CREATE CONVERSION " + name + " FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1"
	}
	run(c, Exec(create("identity_first.item"), 0))
	run(c, Exec(create("identity_second.item"), 0))
	run(c, Exec("CREATE TABLE identity_unrelated(v integer)", 0))
	run(c, Exec("SET search_path=identity_first,identity_second,pg_catalog", 0))
	catalog, nameColumn, namespaceColumn, class := "pg_collation", "collname", "collnamespace", int64(3456)
	if family == "CONVERSION" {
		catalog, nameColumn, namespaceColumn, class = "pg_conversion", "conname", "connamespace", 2607
	}
	var original, fallback, movedNS int64
	if err := c.QueryRowContext(ctx, "SELECT oid::bigint FROM pg_namespace WHERE nspname='identity_moved'").Scan(&movedNS); err != nil {
		t.Fatal(err)
	}
	for i, ns := range []string{"identity_first", "identity_second"} {
		var oid int64
		if err := c.QueryRowContext(ctx, "SELECT d.oid::bigint FROM "+catalog+" d JOIN pg_namespace n ON n.oid=d."+namespaceColumn+" WHERE d."+nameColumn+"='item' AND n.nspname=$1", ns).Scan(&oid); err != nil || oid <= 0 {
			t.Fatal(oid, err)
		}
		if i == 0 {
			original = oid
		} else {
			fallback = oid
		}
	}
	if original == fallback {
		t.Fatal("distinct namespace definitions share identity")
	}
	snapshot := func(x Executor, oid int64) []map[string]any {
		return DefinitionIdentityRows(t, ctx, x, "SELECT * FROM "+catalog+" WHERE oid=$1", oid)
	}
	dependencies := func(x Executor, oid int64) []map[string]any {
		return DefinitionIdentityRows(t, ctx, x, "SELECT classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype FROM pg_depend WHERE classid=$1 AND objid=$2 ORDER BY refclassid,refobjid,refobjsubid,deptype", class, oid)
	}
	beforeOriginal, beforeFallback := snapshot(c, original), snapshot(c, fallback)
	originalDeps, fallbackDeps := dependencies(c, original), dependencies(c, fallback)
	nonNamespaceDependencies := func(x Executor) []map[string]any {
		return DefinitionIdentityRows(t, ctx, x, "SELECT classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype FROM pg_depend WHERE classid=$1 AND objid=$2 AND refclassid<>2615 ORDER BY refclassid,refobjid,refobjsubid,deptype", class, original)
	}
	originalNonNamespaceDeps := nonNamespaceDependencies(c)
	q := "ALTER " + family + " item RENAME TO waiter_name"
	var stmt *sql.Stmt
	if held {
		var err error
		stmt, err = c.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal("quiet Prepare", err)
		}
		defer stmt.Close()
	}
	run(holder, Exec("BEGIN", 0))
	t.Cleanup(func() { holder.ExecContext(context.Background(), "ROLLBACK") })
	mutation := "RENAME TO holder_name"
	if action == "move_commit" {
		mutation = "SET SCHEMA identity_moved"
	}
	run(holder, Exec("ALTER "+family+" identity_first.item "+mutation, 0))
	var waiter Executor = c
	var tx *sql.Tx
	if route == "go" {
		var err error
		tx, err = c.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		waiter = tx
		defer func() { tx.Rollback() }()
	} else {
		run(c, Exec("BEGIN", 0))
		defer c.ExecContext(context.Background(), "ROLLBACK")
	}
	run(waiter, Exec("SAVEPOINT waiting", 0))
	var execute Executor = waiter
	if stmt != nil {
		if tx != nil {
			stmt = tx.StmtContext(ctx, stmt)
			defer stmt.Close()
		}
		execute = HeldStatement{Executor: waiter, Statement: stmt, SQL: q}
	}
	join := DefinitionLockCall(t, ctx, p, b, c, holder, q, "object", "AccessExclusiveLock", class, original, func(worker context.Context) RelationOutcome {
		result, err := execute.ExecContext(worker, q)
		return RelationOutcome{Result: result, Err: err}
	})
	run(peer, Exec("INSERT INTO public.identity_unrelated VALUES(42)", 1))
	end := "COMMIT"
	if action == "holder_rollback" {
		end = "ROLLBACK"
	}
	run(holder, Exec(end, 0))
	outcome := join()
	succeeds := b.PG || action == "holder_rollback"
	if succeeds {
		if outcome.Err != nil {
			t.Fatal("Execute", outcome.Err)
		}
		LockAffected(t, outcome.Result, 0)
	} else {
		TransactionOrdinary(t, outcome.Err, "stuntgres: catalog replaced before publication")
		if outcome.Result != nil {
			t.Fatal("rejected ALTER returned result")
		}
		run(waiter, ErrorDiagnostic("SELECT 1", TransactionAborted()))
		// Native replacement fences discard the transaction's savepoint marks.
		// End that aborted owner before exercising fresh qualified recovery.
		if tx != nil {
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			var err error
			tx, err = c.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			waiter = tx
		} else {
			run(c, Exec("ROLLBACK", 0))
			run(c, Exec("BEGIN", 0))
		}
		run(waiter, Exec("SAVEPOINT waiting", 0))
	}
	expectedOriginal, expectedFallback := snapshot(peer, original), snapshot(peer, fallback)
	// The committed holder must change only its original row's name or namespace.
	wantOriginal := make([]map[string]any, len(beforeOriginal))
	for i, row := range beforeOriginal {
		wantOriginal[i] = map[string]any{}
		for k, v := range row {
			wantOriginal[i][k] = v
		}
	}
	if action == "rename_commit" {
		wantOriginal[0][nameColumn] = "holder_name"
	}
	if action == "move_commit" {
		// Keep the backend's OID representation, independently asserting its value.
		var actualNS int64
		rows, err := peer.QueryContext(ctx, "SELECT "+namespaceColumn+"::bigint FROM "+catalog+" WHERE oid=$1", original)
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal("original missing")
		}
		if err := rows.Scan(&actualNS); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if actualNS != movedNS {
			t.Fatal("original moved to wrong namespace", actualNS, movedNS)
		}
		wantOriginal[0][namespaceColumn] = expectedOriginal[0][namespaceColumn]
	}
	DefinitionIdentityEqual(t, expectedOriginal, wantOriginal)
	DefinitionIdentityEqual(t, expectedFallback, beforeFallback)
	wantWaiterOriginal, wantWaiterFallback := snapshot(peer, original), snapshot(peer, fallback)
	if succeeds {
		if action == "holder_rollback" {
			wantWaiterOriginal[0][nameColumn] = "waiter_name"
		} else {
			wantWaiterFallback[0][nameColumn] = "waiter_name"
		}
	}
	DefinitionIdentityEqual(t, snapshot(waiter, original), wantWaiterOriginal)
	DefinitionIdentityEqual(t, snapshot(waiter, fallback), wantWaiterFallback)
	DefinitionIdentityEqual(t, dependencies(waiter, fallback), fallbackDeps)
	if action != "move_commit" {
		DefinitionIdentityEqual(t, dependencies(waiter, original), originalDeps)
	}
	if action == "move_commit" {
		DefinitionIdentityEqual(t, nonNamespaceDependencies(waiter), originalNonNamespaceDeps)
		run(waiter, Select("SELECT refobjid::bigint,deptype FROM pg_depend WHERE classid=$1 AND objid=$2 AND refclassid=2615", []string{"refobjid", "deptype"}, []string{"INT8", "CHAR"}, [][]any{{movedNS, "n"}}, class, original))
	}
	run(waiter, Exec("ROLLBACK TO waiting", 0))
	DefinitionIdentityEqual(t, snapshot(waiter, original), expectedOriginal)
	DefinitionIdentityEqual(t, snapshot(waiter, fallback), beforeFallback)
	// Recovery targets the holder's actual identity explicitly, then rolls it back.
	current := "identity_first.item"
	if action == "rename_commit" {
		current = "identity_first.holder_name"
	}
	if action == "move_commit" {
		current = "identity_moved.item"
	}
	run(waiter, Exec("ALTER "+family+" "+current+" RENAME TO recovered", 0))
	run(waiter, Exec("ROLLBACK TO waiting", 0))
	DefinitionIdentityEqual(t, snapshot(waiter, original), expectedOriginal)
	DefinitionIdentityEqual(t, snapshot(waiter, fallback), beforeFallback)
	DefinitionIdentityEqual(t, dependencies(waiter, original), dependencies(peer, original))
	DefinitionIdentityEqual(t, dependencies(waiter, fallback), fallbackDeps)
	if tx != nil {
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	} else {
		run(c, Exec("ROLLBACK", 0))
	}
	run(c, Select("SELECT v FROM public.identity_unrelated", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
	DefinitionIdentityEqual(t, snapshot(c, fallback), beforeFallback)
	DefinitionIdentityEqual(t, dependencies(c, fallback), fallbackDeps)
}

package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pgConfigRetained(t *testing.T, rows *sql.Rows, b fixture.Backend) {
	t.Helper()
	defer rows.Close()
	pgConfigMetadata(t, rows, b.PG)
	var got [][]any
	for rows.Next() {
		var name, setting string
		if err := rows.Scan(&name, &setting); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{name, setting})
	}
	if err := rows.Err(); err != nil || !reflect.DeepEqual(got, pgConfigVersion(b)) {
		t.Fatal("retained configuration rows", got, err)
	}
}

func pgConfigRoundTrip(t *testing.T, p *fixture.Pair) string {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	return dump.String()
}

func pgConfigLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	exec := func(e fixture.Executor, query string, count int64) {
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(query, count))
	}
	exec(b.DB, "CREATE TABLE pgconfig_sentinel(n integer)", 0)
	exec(b.DB, "INSERT INTO pgconfig_sentinel VALUES(42)", 1)
	conn, other := fixture.LockConnections(t, p.Ctx, b.DB)
	query := pgConfigSelect + " WHERE name='VERSION'"
	stmt, err := conn.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
	read := func() {
		pgConfigRead(t, p, b, conn)
		pgConfigRead(t, p, b, held)
		pgConfigRead(t, p, b, other)
		s := pgConfigStep(query, pgConfigVersion(b))
		s.Prepared = true
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgConfigMetadata(t, rows, b.PG) }
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
	}
	read()
	exec(other, "CREATE TABLE pgconfig_unrelated(n integer)", 0)
	exec(other, "INSERT INTO pgconfig_unrelated VALUES(7)", 1)
	read()
	tx, err := other.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exec(tx, "INSERT INTO pgconfig_sentinel VALUES(99)", 1)
	pgConfigRead(t, p, b, tx)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	pgConfigSentinel(t, p, b, conn)
	exec(other, "DROP TABLE pgconfig_unrelated", 0)
	read()
	if b.PG {
		return // Native replacement/control APIs have no PostgreSQL counterpart.
	}
	retained, err := b.DB.QueryContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	pgConfigMetadata(t, retained, false)
	savedTypes, err := retained.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	var baseline bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &baseline); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(baseline.String(), "pg_config") {
		t.Fatal("default build metadata/authority serialized as user state", baseline.String())
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT relname::text FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='pgconfig_sentinel'", []string{"relname"}, []string{"TEXT"}, nil))
	read()
	pgConfigInitialACL(t, p, b, conn, true)
	pgConfigRoutineMetadata(t, p, b, conn)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(baseline.String())); err != nil {
		t.Fatal(err)
	}
	read()
	pgConfigSentinel(t, p, b, conn)
	pgConfigRetained(t, retained, b)
	for i, typ := range savedTypes {
		if typ.Name() != pgConfigColumns[i] || typ.DatabaseTypeName() != "TEXT" || typ.ScanType() != reflect.TypeOf("") {
			t.Fatal("saved metadata changed after replacement", i, typ)
		}
		if nullable, known := typ.Nullable(); !nullable || !known {
			t.Fatal("saved view nullability changed", i, nullable, known)
		}
	}
	// A peer pool closing and reopening retains the same exact-DSN engine.
	for range 2 {
		pool, recorder := fixture.RecordedNativePool(t, p.NativeConnector)
		peer := fixture.Backend{Name: "reopened", DB: pool, Recorder: recorder}
		pgConfigRead(t, p, peer, pool)
		pgConfigSentinel(t, p, peer, pool)
		if err := pool.Close(); err != nil {
			t.Fatal(err)
		}
	}
	connector, err := stuntgres.NewDriver().OpenConnector(p.NativeDSN)
	if err != nil {
		t.Fatal(err)
	}
	isolated, recorder := fixture.RecordedNativePool(t, connector)
	independent := fixture.Backend{Name: "independent-driver", DB: isolated, Recorder: recorder}
	pgConfigRead(t, p, independent, isolated)
	fixture.RunStep(t, p.Ctx, isolated, recorder, fixture.Select("SELECT relname::text FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='pgconfig_sentinel'", []string{"relname"}, []string{"TEXT"}, nil))
	pgConfigACLControls(t, p, b, conn, held)
}

func pgConfigACLControls(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, held fixture.HeldStatement) {
	t.Helper()
	exec := func(query string) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(query, 0)) }
	defer conn.ExecContext(context.Background(), "RESET ROLE")
	actorRead := func(allowed, functionDenied bool) {
		exec("SET ROLE pg_signal_backend")
		if allowed {
			pgConfigRead(t, p, b, conn)
			pgConfigRead(t, p, b, held)
		} else if functionDenied {
			pgConfigFunctionDenied(t, p, b, conn, held.SQL)
			pgConfigFunctionDenied(t, p, b, held, held.SQL)
		} else {
			pgConfigDenied(t, p, b, conn, held.SQL)
			pgConfigDenied(t, p, b, held, held.SQL)
		}
		exec("RESET ROLE")
	}
	actorRead(false, false)
	exec("GRANT SELECT ON pg_catalog.pg_config TO pg_signal_backend")
	pgConfigRoundTrip(t, p)
	actorRead(false, true)
	exec("GRANT EXECUTE ON FUNCTION pg_catalog.pg_config() TO pg_signal_backend")
	custom := pgConfigRoundTrip(t, p)
	actorRead(true, false)
	for _, tc := range []struct{ query, column, acl string }{
		{"SELECT relacl::text FROM pg_class WHERE oid='pg_catalog.pg_config'::regclass", "relacl", "{postgres=arwdDxtm/postgres,pg_signal_backend=r/postgres}"},
		{"SELECT proacl::text FROM pg_proc WHERE oid=3400", "proacl", "{postgres=X/postgres,pg_signal_backend=X/postgres}"},
	} {
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select(tc.query, []string{tc.column}, []string{"TEXT"}, [][]any{{tc.acl}}))
	}
	pgConfigInitialACL(t, p, b, conn, false)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(custom+"CREATE TABLE pgconfig_restore_prefix(n integer); not valid SQL;")); err == nil {
		t.Fatal("invalid restore published a valid prefix")
	}
	pgConfigSentinel(t, p, b, conn)
	actorRead(true, false)
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT relname::text FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='pgconfig_restore_prefix'", []string{"relname"}, []string{"TEXT"}, nil))
	exec("REVOKE SELECT ON pg_catalog.pg_config FROM pg_signal_backend")
	exec("GRANT SELECT(name) ON pg_catalog.pg_config TO pg_signal_backend")
	pgConfigRoundTrip(t, p)
	actorRead(false, false)
	exec("SET ROLE pg_signal_backend")
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT name FROM pg_catalog.pg_config WHERE name='VERSION'", []string{"name"}, []string{"TEXT"}, [][]any{{"VERSION"}}))
	exec("RESET ROLE")
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT attacl::text FROM pg_attribute WHERE attrelid='pg_catalog.pg_config'::regclass AND attname='name'", []string{"attacl"}, []string{"TEXT"}, [][]any{{"{pg_signal_backend=r/postgres}"}}))
	exec("REVOKE SELECT(name) ON pg_catalog.pg_config FROM pg_signal_backend")
	exec("REVOKE ALL ON pg_catalog.pg_config FROM postgres,pg_signal_backend")
	exec("REVOKE ALL ON FUNCTION pg_catalog.pg_config() FROM postgres,pg_signal_backend")
	pgConfigRoundTrip(t, p)
	for _, tc := range []struct{ query, column string }{
		{"SELECT relacl::text FROM pg_class WHERE oid='pg_catalog.pg_config'::regclass", "relacl"},
		{"SELECT proacl::text FROM pg_proc WHERE oid=3400", "proacl"},
	} {
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select(tc.query, []string{tc.column}, []string{"TEXT"}, [][]any{{"{}"}}))
	}
	actorRead(false, false)
	pgConfigInitialACL(t, p, b, conn, false)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	pgConfigInitialACL(t, p, b, conn, true)
	pgConfigRoutineMetadata(t, p, b, conn)
	pgConfigRead(t, p, b, held)
	actorRead(false, false)
}

func pgConfigCancellation(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	if b.PG {
		return // Ordinary pre-canceled host errors are a native adapter contract.
	}
	stmt, err := b.DB.PrepareContext(p.Ctx, pgConfigSelect)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, expired := range []bool{false, true} {
		ctx, cancel := context.WithCancel(p.Ctx)
		want := error(context.Canceled)
		if expired {
			cancel()
			ctx, cancel = context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
			want = context.DeadlineExceeded
		} else {
			cancel()
		}
		check := func(err error) {
			t.Helper()
			var state interface{ SQLState() string }
			if !errors.Is(err, want) || errors.As(err, &state) {
				t.Fatal("ordinary context error", err, "want", want)
			}
		}
		rows, err := b.DB.QueryContext(ctx, pgConfigSelect)
		if rows != nil {
			rows.Close()
			t.Fatal("canceled query published rows")
		}
		check(err)
		prepared, err := b.DB.PrepareContext(ctx, pgConfigSelect)
		if prepared != nil {
			prepared.Close()
			t.Fatal("canceled prepare published statement")
		}
		check(err)
		rows, err = stmt.QueryContext(ctx)
		if rows != nil {
			rows.Close()
			t.Fatal("canceled held query published rows")
		}
		check(err)
		cancel()
	}
	pgConfigRead(t, p, b, b.DB)
}

func TestReferencePGConfigLifecycle(t *testing.T) {
	t.Run("replacement", func(t *testing.T) { pgConfigScenario(t, pgConfigLifecycle) })
	t.Run("cancellation", func(t *testing.T) { pgConfigScenario(t, pgConfigCancellation) })
}

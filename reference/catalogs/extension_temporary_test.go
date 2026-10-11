package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"sync"
	"testing"
	"time"
)

// A separate physical owner is essential: Conn.Close alone returns a pool lease.
// The existing Joined fixture boundary keeps a failing close witness bounded;
// every successful close joins its worker before inspecting the peer catalog.
type extensionTempOwner struct {
	db       *sql.DB
	conn     *sql.Conn
	recorder *fixture.Recorder
	notices  []fixture.Diagnostic
	once     sync.Once
	done     chan struct{}
	timedOut bool // Test-owner thread only; fixture fallback is failure-only.
	err      error
}

func extensionTemporaryOwner(t *testing.T, p *fixture.Pair, pg bool) *extensionTempOwner {
	t.Helper()
	o := &extensionTempOwner{done: make(chan struct{})}
	o.db, o.conn, o.recorder = fixture.NoticeReferencePool(t, p, pg, &o.notices, fixture.NoticeReferenceOption{Joined: func() bool { return !o.timedOut }})
	t.Cleanup(func() { o.close(t) })
	return o
}
func (o *extensionTempOwner) close(t *testing.T) bool {
	t.Helper()
	if o.timedOut {
		return false
	}
	o.once.Do(func() {
		go func() {
			err := o.conn.Close()
			if errors.Is(err, sql.ErrConnDone) {
				err = nil
			}
			o.err = errors.Join(err, o.db.Close())
			close(o.done)
		}()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-o.done:
		if o.err != nil {
			t.Error("physical TEMP owner close", o.err)
			return false
		}
		return true
	case <-ctx.Done():
		o.timedOut = true
		t.Error("physical TEMP owner close exceeded finite cleanup deadline", ctx.Err())
		return false
	}
}
func extensionTempIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
func extensionTempSeed(t *testing.T, p *fixture.Pair, o *extensionTempOwner) string {
	t.Helper()
	fixture.RunStep(t, p.Ctx, o.conn, o.recorder, fixture.Exec("CREATE TEMP TABLE ext_temp_seed(id INT)", 0))
	var name string
	if err := o.conn.QueryRowContext(p.Ctx, "SELECT schemaname FROM pg_tables WHERE tablename='ext_temp_seed'").Scan(&name); err != nil || !strings.HasPrefix(name, "pg_temp_") {
		t.Fatal("actual TEMP namespace", name, err)
	}
	return name
}

type extensionTempIdentity struct {
	extension, namespace int64
	members              []int64
	classes              map[int64]int64
}

func extensionTempCapture(t *testing.T, p *fixture.Pair, c *sql.Conn) extensionTempIdentity {
	t.Helper()
	extensionInventory(t, p, c)
	var id extensionTempIdentity
	if err := c.QueryRowContext(p.Ctx, "SELECT oid,extnamespace FROM pg_extension WHERE extname='unaccent'").Scan(&id.extension, &id.namespace); err != nil {
		t.Fatal(err)
	}
	id.members = extensionMemberIDs(t, p, c, id.extension)
	id.classes = map[int64]int64{}
	rows, err := c.QueryContext(p.Ctx, "SELECT objid,classid FROM pg_depend WHERE refclassid=3079 AND refobjid=$1 AND deptype='e'", id.extension)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var member, class int64
		if err := rows.Scan(&member, &class); err != nil {
			t.Fatal(err)
		}
		id.classes[member] = class
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return id
}
func extensionTempPresent(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, id extensionTempIdentity) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT oid::bigint AS oid,extnamespace::bigint AS extnamespace FROM pg_extension WHERE extname='unaccent'", []string{"oid", "extnamespace"}, []string{"INT8", "INT8"}, [][]any{{id.extension, id.namespace}}))
	if got := extensionMemberIDs(t, p, c, id.extension); !reflect.DeepEqual(got, id.members) {
		t.Fatal("TEMP installation identities changed", got, id.members)
	}
	extensionInventory(t, p, c)
}
func extensionNamespaceDependency(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	var ext, ns int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid,extnamespace FROM pg_extension WHERE extname='unaccent'").Scan(&ext, &ns); err != nil {
		t.Fatal(err)
	}
	query := "SELECT classid::bigint AS classid,objid::bigint AS objid,objsubid,refclassid::bigint AS refclassid,refobjid::bigint AS refobjid,refobjsubid,deptype FROM pg_depend WHERE classid=3079 AND objid=$1 AND refclassid=2615"
	step := fixture.Select(query, fixture.PgDependColumns, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"}, [][]any{fixture.PgDependEdge(3079, ext, 0, 2615, ns, 0, "n")}, ext)
	fixture.RunStep(t, p.Ctx, c, r, step)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(step))
}
func extensionTempGone(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, id extensionTempIdentity) {
	t.Helper()
	extensionCount(t, p, c, r, 0)
	for member, class := range id.classes {
		table := map[int64]string{1255: "pg_proc", 3764: "pg_ts_template", 3600: "pg_ts_dict"}[class]
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM "+table+" WHERE oid=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, member))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pg_depend WHERE (classid=$1 AND objid=$2) OR (refclassid=$1 AND refobjid=$2)", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, class, member))
	}
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pg_depend WHERE (classid=3079 AND objid=$1) OR (refclassid=3079 AND refobjid=$1)", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, id.extension))
}
func extensionTempCall(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, name string) {
	t.Helper()
	step := fixture.Select("SELECT "+extensionTempIdentifier(name)+".unaccent($1::text)", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}}, "Hôtel")
	fixture.RunStep(t, p.Ctx, c, r, step)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(step))
	step.Args = []any{nil}
	step.Rows = [][]any{{nil}}
	fixture.RunStep(t, p.Ctx, c, r, step)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(step))
}

func TestReferenceExtensionNamespaceDependency(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE SCHEMA ext_addon", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_addon", 0))
		extensionInventory(t, p, c)
		extensionNamespaceDependency(t, p, c, r)
	})
}
func TestReferenceExtensionTemporaryLookup(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, peer *sql.Conn, r *fixture.Recorder, pg bool) {
		o := extensionTemporaryOwner(t, p, pg)
		defer o.close(t)
		name := extensionTempSeed(t, p, o)
		installer := extensionTemporaryOwner(t, p, pg)
		defer installer.close(t)
		fixture.RunStep(t, p.Ctx, installer.conn, installer.recorder, fixture.Select("SELECT pg_advisory_lock(30790002)", []string{"pg_advisory_lock"}, []string{"VOID"}, [][]any{{""}}))
		fixture.RunStep(t, p.Ctx, installer.conn, installer.recorder, fixture.Exec("CREATE EXTENSION unaccent SCHEMA "+extensionTempIdentifier(name), 0))
		if !installer.close(t) {
			t.FailNow()
		}
		extensionTempExitBarrier(t, p, peer, r, 30790002)
		id := extensionTempCapture(t, p, o.conn)
		extensionTempPresent(t, p, peer, r, id)
		extensionTempCall(t, p, o.conn, o.recorder, name)
		extensionTempCall(t, p, peer, r, name)
		fixture.RunStep(t, p.Ctx, o.conn, o.recorder, fixture.Exec("SET search_path TO "+extensionTempIdentifier(name), 0))
		fixture.RunStep(t, p.Ctx, o.conn, o.recorder, extensionMissing("SELECT unaccent('Hôtel'::text)", "unaccent", "text"))
		extensionTempCall(t, p, o.conn, o.recorder, name)
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("SET search_path TO "+extensionTempIdentifier(name), 0))
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Select("SELECT unaccent('Hôtel'::text)", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("RESET search_path", 0))
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("CREATE USER ext_installer", 0))
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("SET SESSION AUTHORIZATION ext_installer", 0))
		query := "SELECT " + extensionTempIdentifier(name) + ".unaccent('Hôtel'::text)"
		fixture.RunStep(t, p.Ctx, peer, r, fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema " + name, Position: "8"}))
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("SET search_path TO "+extensionTempIdentifier(name), 0))
		fixture.RunStep(t, p.Ctx, peer, r, extensionMissing("SELECT unaccent('Hôtel'::text)", "unaccent", "text"))
		extensionCount(t, p, peer, r, 1)
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("RESET search_path", 0))
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		extensionNamespaceDependency(t, p, o.conn, o.recorder)
		fixture.RunStep(t, p.Ctx, o.conn, o.recorder, fixture.Exec("DISCARD TEMP", 0))
		extensionTempGone(t, p, peer, r, id)
		if len(o.notices) != 0 {
			t.Fatal("TEMP retirement notices", o.notices)
		}
	})
}
func extensionTempConsumers(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, name string) {
	t.Helper()
	function := extensionTempIdentifier(name) + ".unaccent"
	for _, q := range []string{
		"CREATE TABLE public.ext_temp_consumer(id INT,txt TEXT DEFAULT " + function + "('Hôtel'::text),CONSTRAINT ext_temp_check CHECK(" + function + "(txt)=txt))",
		"CREATE VIEW public.ext_temp_view AS SELECT " + function + "('Hôtel'::text) AS v",
		"CREATE VIEW public.ext_temp_chain AS SELECT v FROM public.ext_temp_view",
	} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO public.ext_temp_consumer(id) VALUES(1)", 1))
	extensionTempConsumerState(t, p, c, r, true)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT schemaname FROM pg_views WHERE viewname='ext_temp_view'", []string{"schemaname"}, []string{"NAME"}, [][]any{{"public"}}))
	var procedure int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_proc WHERE proname='unaccent' AND pronargs=1 AND pronamespace=(SELECT extnamespace FROM pg_extension WHERE extname='unaccent')").Scan(&procedure); err != nil {
		t.Fatal(err)
	}
	for _, class := range []int64{2604, 2606, 2618} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=$1 AND refclassid=1255 AND refobjid=$2", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, class, procedure))
	}
}
func extensionTempConsumerCatalog(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, present bool) {
	t.Helper()
	n := int64(0)
	if present {
		n = 1
	}
	for _, query := range []string{
		"SELECT count(*) FROM pg_attrdef WHERE adrelid=(SELECT oid FROM pg_class WHERE relname='ext_temp_consumer')",
		"SELECT count(*) FROM pg_constraint WHERE conrelid=(SELECT oid FROM pg_class WHERE relname='ext_temp_consumer') AND conname='ext_temp_check'",
	} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select(query, []string{"count"}, []string{"INT8"}, [][]any{{n}}))
	}
	for _, view := range []string{"ext_temp_view", "ext_temp_chain"} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pg_views WHERE schemaname='public' AND viewname=$1", []string{"count"}, []string{"INT8"}, [][]any{{n}}, view))
	}
}
func extensionTempConsumerState(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, present bool) {
	t.Helper()
	extensionTempConsumerCatalog(t, p, c, r, present)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT id,txt FROM public.ext_temp_consumer ORDER BY id", []string{"id", "txt"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "Hotel"}}))
	if present {
		for _, view := range []string{"ext_temp_view", "ext_temp_chain"} {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT v FROM public."+view, []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
		}
	}
}
func extensionTempMissingViews(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	for _, view := range []string{"ext_temp_view", "ext_temp_chain"} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.ImplicitError("SELECT v FROM public."+view, fixture.Diagnostic{Code: "42P01", Message: fmt.Sprintf(`relation "public.%s" does not exist`, view), Position: "15"}))
	}
}
func extensionTempSurvivingWrites(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO public.ext_temp_consumer(id,txt) VALUES(2,'Hôtel')", 1))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO public.ext_temp_consumer(id) VALUES(3)", 1))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT id,txt FROM public.ext_temp_consumer ORDER BY id", []string{"id", "txt"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "Hotel"}, {int64(2), "Hôtel"}, {int64(3), nil}}))
}
func TestReferenceExtensionTemporaryDiscard(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, peer *sql.Conn, r *fixture.Recorder, pg bool) {
		o := extensionTemporaryOwner(t, p, pg)
		name := extensionTempSeed(t, p, o)
		defer o.close(t)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, o.conn, o.recorder, s) }
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("CREATE EXTENSION unaccent SCHEMA "+extensionTempIdentifier(name), 0))
		id := extensionTempCapture(t, p, o.conn)
		extensionTempConsumers(t, p, o.conn, o.recorder, name)
		// Qualified function dependency does not make these persistent views TEMP.
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Select("SELECT v FROM public.ext_temp_chain", []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.ImplicitError("DISCARD ALL", fixture.Diagnostic{Code: "25001", Message: "DISCARD ALL cannot run inside a transaction block"}))
		run(fixture.Exec("ROLLBACK", 0))
		for _, savepoint := range []bool{false, true} {
			run(fixture.Exec("BEGIN", 0))
			if savepoint {
				run(fixture.Exec("SAVEPOINT before_temp", 0))
			}
			run(fixture.Exec("DISCARD TEMP", 0))
			extensionTempGone(t, p, o.conn, o.recorder, id)
			extensionTempConsumerState(t, p, o.conn, o.recorder, false)
			extensionTempPresent(t, p, peer, r, id)
			extensionTempConsumerCatalog(t, p, peer, r, true)
			if savepoint {
				run(fixture.Exec("ROLLBACK TO before_temp", 0))
				extensionTempPresent(t, p, o.conn, o.recorder, id)
				extensionTempConsumerState(t, p, o.conn, o.recorder, true)
			}
			run(fixture.Exec("ROLLBACK", 0))
			extensionTempPresent(t, p, o.conn, o.recorder, id)
			extensionTempCall(t, p, o.conn, o.recorder, name)
			extensionTempConsumerState(t, p, o.conn, o.recorder, true)
			extensionTempConsumerState(t, p, peer, r, true)
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DISCARD TEMP", 0))
		extensionTempPresent(t, p, peer, r, id)
		run(fixture.Exec("COMMIT", 0))
		extensionTempGone(t, p, peer, r, id)
		extensionTempConsumerState(t, p, peer, r, false)
		extensionTempMissingViews(t, p, peer, r)
		extensionTempSurvivingWrites(t, p, peer, r)
		// DISCARD retains its namespace; a new install must not resurrect old IDs.
		nextName := extensionTempSeed(t, p, o)
		if nextName != name {
			t.Fatal("DISCARD changed owned TEMP namespace", name, nextName)
		}
		fixture.RunStep(t, p.Ctx, peer, r, fixture.Exec("CREATE EXTENSION unaccent SCHEMA "+extensionTempIdentifier(nextName), 0))
		fresh := extensionTempCapture(t, p, o.conn)
		if fresh.extension == id.extension {
			t.Fatal("reinstallation resurrected extension identity")
		}
		for _, member := range fresh.members {
			if _, old := id.classes[member]; old {
				t.Fatal("reinstallation resurrected member identity", member)
			}
		}
		extensionTempCall(t, p, o.conn, o.recorder, nextName)
		run(fixture.Exec("DISCARD ALL", 0))
		extensionTempGone(t, p, peer, r, fresh)
		if len(o.notices) != 0 {
			t.Fatal("TEMP cleanup notices", o.notices)
		}
	})
}
func TestReferenceExtensionTemporaryRollback(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, peer *sql.Conn, r *fixture.Recorder, pg bool) {
		o := extensionTemporaryOwner(t, p, pg)
		defer o.close(t)
		ownerRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, o.conn, o.recorder, s) }
		// A private namespace cannot be seen by a foreign installer. Roll back its
		// initialization separately, then exercise installation in a committed one.
		ownerRun(fixture.Exec("BEGIN", 0))
		extensionTempSeed(t, p, o)
		ownerRun(fixture.Exec("ROLLBACK", 0))
		ownerRun(fixture.Select("SELECT count(*) FROM pg_tables WHERE tablename='ext_temp_seed'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		name := extensionTempSeed(t, p, o)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, r, s) }
		run(fixture.Exec("CREATE TABLE public.ext_temp_before(v INT)", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA "+extensionTempIdentifier(name), 0))
		first := extensionTempCapture(t, p, peer)
		extensionTempCall(t, p, peer, r, name)
		extensionCount(t, p, o.conn, o.recorder, 0)
		run(fixture.Exec("ROLLBACK", 0))
		extensionTempGone(t, p, peer, r, first)
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO public.ext_temp_before VALUES(9)", 1))
		run(fixture.Exec("SAVEPOINT before_install", 0))
		run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA "+extensionTempIdentifier(name), 0))
		second := extensionTempCapture(t, p, peer)
		extensionTempCall(t, p, peer, r, name)
		extensionCount(t, p, o.conn, o.recorder, 0)
		run(fixture.Exec("ROLLBACK TO before_install", 0))
		extensionTempGone(t, p, peer, r, second)
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Select("SELECT v FROM public.ext_temp_before", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
		ownerRun(fixture.Exec("DISCARD TEMP", 0))
	})
}

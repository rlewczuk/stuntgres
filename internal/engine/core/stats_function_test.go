package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
)

func statsFunctionKey(t *testing.T, e *core.Engine, name string) statistics.Key {
	t.Helper()
	read := e.CatalogRead()
	for _, descriptor := range read.Descriptors() {
		if d, ok := descriptor.(*catalog.Procedure); ok && d.Name() == name {
			return statistics.Key{Object: read.Owner().Domain.Ref(read.Epoch(), d.Address()), Category: statistics.Function}
		}
	}
	t.Fatalf("missing actual procedure %s", name)
	return statistics.Key{}
}

// Consume real command results before releasing the SQL lease. Publication is
// ordinary idle/terminal publication, never a test-only writer flush.
func statsFunctionSQL(t *testing.T, s *session.Session, q string) error {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil { return err }
	defer op.Done()
	r, err := core.EngineOf(s).Execute(op, enginetest.Statement(t, q), 0, nil)
	if err != nil { return err }
	if r.Rows != nil {
		enginetest.Tuples(r)
		return r.Rows.Close()
	}
	return nil
}

func statsFunctionExec(t *testing.T, s *session.Session, queries ...string) {
	t.Helper()
	for _, q := range queries {
		if err := statsFunctionSQL(t, s, q); err != nil { t.Fatalf("%s: %v", q, err) }
	}
}

func statsFunctionRetire(t *testing.T, s *session.Session) {
	t.Helper()
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil { t.Fatal(err) }
}

func TestStatsSnapshotFunctionInvocation(t *testing.T) {
	for _, mode := range []string{"none", "pl", "all"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, s,
				"SET track_functions = '"+mode+"'",
				"CREATE TABLE stats_calls(id integer, stamp timestamp)",
				"CREATE FUNCTION stats_before() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
				"CREATE FUNCTION stats_after() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
				"CREATE FUNCTION stats_statement() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$",
				"CREATE FUNCTION stats_false() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
				"CREATE FUNCTION stats_disabled() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
				"CREATE FUNCTION stats_columns() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
				"CREATE FUNCTION stats_null_when() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
				"CREATE TRIGGER a BEFORE INSERT ON stats_calls FOR EACH ROW EXECUTE FUNCTION stats_before()",
				"CREATE TRIGGER b AFTER INSERT ON stats_calls FOR EACH ROW EXECUTE FUNCTION stats_after()",
				"CREATE TRIGGER c AFTER INSERT ON stats_calls FOR EACH STATEMENT EXECUTE FUNCTION stats_statement()",
				"CREATE TRIGGER d BEFORE INSERT ON stats_calls FOR EACH ROW WHEN (false) EXECUTE FUNCTION stats_false()",
				"CREATE TRIGGER e BEFORE INSERT ON stats_calls FOR EACH ROW EXECUTE FUNCTION stats_disabled()",
				"ALTER TABLE stats_calls DISABLE TRIGGER e",
				"CREATE TRIGGER f BEFORE UPDATE OF stamp ON stats_calls FOR EACH ROW EXECUTE FUNCTION stats_columns()",
				"CREATE TRIGGER j BEFORE INSERT ON stats_calls FOR EACH ROW WHEN (NULL::boolean) EXECUTE FUNCTION stats_null_when()",
				"INSERT INTO stats_calls(id) VALUES(1),(2)",
				"UPDATE stats_calls SET id=id+10",
				"CREATE EXTENSION moddatetime",
				"CREATE TRIGGER g BEFORE UPDATE ON stats_calls FOR EACH ROW EXECUTE FUNCTION moddatetime('stamp')",
				"CREATE TRIGGER h BEFORE UPDATE ON stats_calls FOR EACH ROW EXECUTE FUNCTION suppress_redundant_updates_trigger()",
				"UPDATE stats_calls SET id=id+10",
				"CREATE TABLE stats_suppressed(id integer)",
				"CREATE FUNCTION stats_suppress() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$",
				"CREATE TRIGGER a BEFORE INSERT ON stats_suppressed FOR EACH ROW EXECUTE FUNCTION stats_suppress()",
				"INSERT INTO stats_suppressed VALUES(1)",
				"CREATE EXTENSION tcn",
				"CREATE TABLE stats_native_queue(id integer PRIMARY KEY)",
				"CREATE TRIGGER a AFTER INSERT ON stats_native_queue FOR EACH ROW EXECUTE FUNCTION triggered_change_notification()",
				"INSERT INTO stats_native_queue VALUES(1),(2)",
			)
			keys := make(map[string]statistics.Key)
			for _, name := range []string{"stats_before", "stats_after", "stats_statement", "stats_false", "stats_null_when", "stats_disabled", "stats_columns", "moddatetime", "triggered_change_notification", "suppress_redundant_updates_trigger", "stats_suppress"} {
				keys[name] = statsFunctionKey(t, e, name)
			}
			statsFunctionRetire(t, s)
			for name, key := range keys {
				want := int64(0)
				if mode != "none" {
					switch name {
					case "stats_before", "stats_after": want = 2
					case "stats_statement", "stats_suppress": want = 1
					}
				}
				if (name == "moddatetime" || name == "triggered_change_notification") && mode == "all" { want = 2 }
				if f := e.Statistics.Read(key); f.Calls != want {
					t.Fatalf("%s calls=%d, want %d (%+v)", name, f.Calls, want, f)
				}
			}
		})
	}
	t.Run("effective_body_environment", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, s,
			"SET track_functions=none",
			"CREATE TABLE stats_environment(id integer)",
			"CREATE FUNCTION stats_enabled() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET track_functions=pl AS $$BEGIN IF pg_trigger_depth() <> 1 THEN RAISE EXCEPTION 'depth'; END IF; RETURN NEW; END$$",
			"CREATE TRIGGER a BEFORE INSERT ON stats_environment FOR EACH ROW EXECUTE FUNCTION stats_enabled()",
			"CREATE FUNCTION stats_enabled_scalar() RETURNS integer LANGUAGE plpgsql SET track_functions=pl AS $$BEGIN RETURN 7; END$$",
			"INSERT INTO stats_environment VALUES(1)",
			"SELECT stats_enabled_scalar()",
			"SET track_functions='all'",
			"CREATE FUNCTION stats_off() RETURNS trigger LANGUAGE plpgsql SET track_functions=none AS $$BEGIN RETURN NEW; END$$",
			"CREATE TRIGGER b AFTER INSERT ON stats_environment FOR EACH ROW EXECUTE FUNCTION stats_off()",
			"CREATE FUNCTION stats_off_scalar() RETURNS integer LANGUAGE plpgsql SET track_functions=none AS $$BEGIN RETURN 7; END$$",
			"INSERT INTO stats_environment VALUES(2)",
			"SELECT stats_off_scalar()",
			"CREATE FUNCTION stats_restored() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$",
			"SELECT stats_restored()",
		)
		keys := map[string]statistics.Key{}
		for _, name := range []string{"stats_enabled", "stats_enabled_scalar", "stats_off", "stats_off_scalar", "stats_restored"} { keys[name] = statsFunctionKey(t, e, name) }
		statsFunctionRetire(t, s)
		for name, want := range map[string]int64{"stats_enabled": 2, "stats_enabled_scalar": 1, "stats_off": 0, "stats_off_scalar": 0, "stats_restored": 1} {
			if f := e.Statistics.Read(keys[name]); f.Calls != want { t.Fatal(name, f, want) }
		}
	})
	t.Run("strict_and_inline", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, s,
			"SET track_functions='all'",
			"CREATE FUNCTION stats_strict(integer) RETURNS integer LANGUAGE plpgsql STRICT AS $$BEGIN RETURN $1; END$$",
			// Configuration prevents SQL inlining; volatility also prevents
			// whole-call constant evaluation. This is a real runtime body.
			"CREATE FUNCTION stats_runtime_sql(integer) RETURNS integer LANGUAGE sql VOLATILE SET track_functions='all' AS 'SELECT $1'",
			"SELECT stats_strict(NULL::integer), stats_runtime_sql(7)",
			"CREATE EXTENSION citext",
			"CREATE TABLE stats_inline_inputs(s citext,p citext)",
			"INSERT INTO stats_inline_inputs VALUES('AbA','a')",
			// exec.preflightEvaluate admits the existing no-flags scalar
			// citext SQL wrapper's InlineSQLBody only after constant folding
			// declines. Table-dependent operands force that actual branch.
			"SELECT public.regexp_match(s,p) FROM stats_inline_inputs",
		)
		strictKey := statsFunctionKey(t,e,"stats_strict")
		runtimeKey := statsFunctionKey(t,e,"stats_runtime_sql")
		read := e.CatalogRead()
		// ArgumentNames is fixed-capacity declaration storage, not arity.
		// Resolve the exact no-flags member through actual installation
		// ownership, then authenticate it with the binder's provider authority.
		var inlineDeclaration *catalog.Procedure
		for _, descriptor := range read.Descriptors() {
			if installed,ok := descriptor.(*catalog.Extension); ok && installed.Name()=="citext" {
				inlineDeclaration,_=read.Get(installed.Members[extension.CitextRegexpProcedureStart]).(*catalog.Procedure)
			}
		}
		if inlineDeclaration==nil {t.Fatal("missing installed two-argument citext SQL wrapper")}
		if err:=extension.StockCitext.ValidateProcedure(read,inlineDeclaration); err!=nil {t.Fatal("invalid installed inline wrapper",err)}
		inlineKey:=statistics.Key{Object:read.Owner().Domain.Ref(read.Epoch(),inlineDeclaration.Address()),Category:statistics.Function}
		statsFunctionRetire(t,s)
		for name,key := range map[string]statistics.Key{"strict NULL":strictKey,"inlined citext SQL body":inlineKey} {
			if f:=e.Statistics.Read(key); f.Calls!=0 {t.Fatal(name,key,f)}
		}
		if f:=e.Statistics.Read(runtimeKey); f.Calls!=1 {t.Fatal("non-inlined SQL runtime body",runtimeKey,f)}
	})
	t.Run("discarded_queue_and_rejected_admission", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,s,
			"SET track_functions=pl",
			"CREATE TABLE stats_discard(id integer CHECK (id>0))",
			"CREATE FUNCTION stats_discarded() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
			"CREATE TRIGGER a AFTER INSERT ON stats_discard FOR EACH ROW EXECUTE FUNCTION stats_discarded()",
		)
		key := statsFunctionKey(t,e,"stats_discarded")
		// Row one enqueues AFTER work, but row two fails before the command's
		// drain boundary. Neither enqueue nor discarded work is an invocation.
		if err:=statsFunctionSQL(t,s,"INSERT INTO stats_discard VALUES(1),(-1)");err==nil {t.Fatal("accepted failed command")}
		ctx,cancel:=context.WithCancel(context.Background())
		op,err:=s.Acquire(ctx);if err!=nil {t.Fatal(err)}
		cancel()
		r,err:=e.Execute(op,enginetest.Statement(t,"INSERT INTO stats_discard VALUES(2)"),0,nil)
		if r.Rows!=nil {r.Rows.Close()}
		op.Done()
		if err==nil {t.Fatal("accepted canceled command")}
		statsFunctionRetire(t,s)
		if f:=e.Statistics.Read(key);f.Calls!=0 {t.Fatal("discarded/rejected body counted",f)}
	})
}

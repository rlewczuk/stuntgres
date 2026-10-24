package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

type transactionalRoutineRouteOptions struct {
	AggregateMetadata bool
	Setup             []string
}

// Each route owns a fresh fixture and retains the actual utility and consumer
// handles. A fresh fixture.Prepared step alone would not prove reuse.
func transactionalRoutineRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.Conn, fixture.Executor, func(string), func(string, string, int64), func(), bool, bool), options ...transactionalRoutineRouteOptions) {
	t.Helper()
	var option transactionalRoutineRouteOptions
	if len(options) != 0 {
		option = options[0]
	}
	for _, mode := range []string{"autocommit", "sql", "go"} {
		for _, held := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				if mode == "autocommit" && !commit {
					continue
				}
				t.Run(fmt.Sprintf("%s/held=%v/commit=%v", mode, held, commit), func(t *testing.T) {
					fixture.SchemaReferenceCleanup(t, "txr_dest")
					fixture.RoleReferenceCleanup(t, []string{"txr_owner"})
					routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						peer, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer peer.Close()
						routineExec(t, p, b, c, option.Setup...)
						var e fixture.Executor = c
						var tx *sql.Tx
						if mode == "sql" {
							routineExec(t, p, b, c, "BEGIN")
							defer c.ExecContext(p.Ctx, "ROLLBACK")
						}
						if mode == "go" {
							tx, err = c.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							e = tx
						}
						statements := map[string]*sql.Stmt{}
						defer func() {
							for _, s := range statements {
								s.Close()
							}
						}()
						executor := func(q string) fixture.Executor {
							if !held {
								return e
							}
							s := statements[q]
							if s == nil {
								if tx != nil {
									s, err = tx.PrepareContext(p.Ctx, q)
								} else {
									s, err = c.PrepareContext(p.Ctx, q)
								}
								if err != nil {
									t.Fatal("prepare", q, err)
								}
								statements[q] = s
							}
							return fixture.HeldStatement{Executor: e, Statement: s, SQL: q}
						}
						exec := func(q string) { fixture.RunStep(t, p.Ctx, executor(q), b.Recorder, fixture.Exec(q, 0)) }
						value := func(q, typ string, n int64) {
							s := fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{n}})
							nullable := option.AggregateMetadata
							s.CheckMetadata = routineScalarMetadata(b, "v", typ, nullable, nullable)
							fixture.RunStep(t, p.Ctx, executor(q), b.Recorder, s)
						}
						finish := func() {
							if mode == "autocommit" {
								return
							}
							if tx != nil {
								if commit {
									err = tx.Commit()
								} else {
									err = tx.Rollback()
								}
								if err != nil {
									t.Fatal(err)
								}
							} else if commit {
								routineExec(t, p, b, c, "COMMIT")
							} else {
								routineExec(t, p, b, c, "ROLLBACK")
							}
						}
						body(t, p, b, c, peer, e, exec, value, finish, mode != "autocommit", commit)
					})
				})
			}
		}
	}
}

func transactionalRoutineCount(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, n int64) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{n}}))
}

func TestReferenceTransactionalRoutinesLifecycle(t *testing.T) {
	t.Run("held-recovery", transactionalRoutineHeldRecovery)
	transactionalRoutineRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, e fixture.Executor, exec func(string), value func(string, string, int64), finish func(), explicit, commit bool) {
		for _, q := range []string{
			"CREATE FUNCTION public.txr_add(integer) RETURNS integer RETURN $1+1",
			"CREATE FUNCTION public.txr_quoted(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$",
			"CREATE FUNCTION public.txr_atomic(integer) RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT $1+1; END",
			"CREATE FUNCTION public.txr_pl(integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN $1+1; END$$",
			"CREATE FUNCTION txr_named(x integer DEFAULT 4) RETURNS integer RETURN x+1",
			"CREATE FUNCTION txr_out(IN x integer,OUT y integer) LANGUAGE SQL AS 'SELECT $1+1'",
			"CREATE FUNCTION txr_variadic(VARIADIC xs integer[]) RETURNS integer LANGUAGE SQL AS 'SELECT $1[1]+$1[2]'",
		} {
			exec(q)
		}
		for _, name := range []string{"txr_add", "txr_quoted", "txr_atomic", "txr_pl"} {
			value("SELECT public."+name+"(4) AS v", "INT4", 5)
		}
		for _, q := range []string{"SELECT txr_named() AS v", "SELECT txr_named(x=>4) AS v", "SELECT txr_out(x=>4) AS v", "SELECT txr_variadic(2,3) AS v"} {
			value(q, "INT4", 5)
		}
		transactionalRoutineCount(t, p, b, e, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('txr_add','txr_quoted','txr_atomic','txr_pl')", 4)
		wantPrivate := int64(4)
		if explicit {
			wantPrivate = 0
		}
		transactionalRoutineCount(t, p, b, peer, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('txr_add','txr_quoted','txr_atomic','txr_pl')", wantPrivate)
		wantParameters := int64(3)
		if explicit {
			wantParameters = 0
		}
		transactionalRoutineCount(t, p, b, peer, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('txr_named','txr_out','txr_variadic')", wantParameters)
		if explicit {
			d := fixture.ControlsReferenceDiagnostic("42883", "function public.txr_add(integer) does not exist")
			d.Position, d.Hint = "8", "No function matches the given name and argument types. You might need to add explicit type casts."
			s := fixture.ErrorDiagnostic("SELECT public.txr_add(4) AS v", d)
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, s)
		}
		finish()
		wantFinal := int64(0)
		if commit {
			wantFinal = 4
		}
		transactionalRoutineCount(t, p, b, peer, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('txr_add','txr_quoted','txr_atomic','txr_pl')", wantFinal)
		wantParameters = 0
		if commit {
			wantParameters = 3
		}
		transactionalRoutineCount(t, p, b, peer, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('txr_named','txr_out','txr_variadic')", wantParameters)
		if commit {
			routineValue(t, p, b, peer, "SELECT public.txr_add(4) AS v", "v", "INT4", int64(5))
		}
	})
}

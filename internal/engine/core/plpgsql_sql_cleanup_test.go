package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Keep the established foreign planner, scan End accounting and transaction
// fault seams; only Iterate supplies the late primary fault. Empty foreign rows
// match the empty attribute plan: the real SQL projection is the constant 1.
type sqlCommandCleanupProvider struct {
	fdwUnitScanWrapper
	scan        *sqlCommandCleanupScan
	transaction *plExceptionFaultTx
	begins      int
	observe     func(string)
}

func (*sqlCommandCleanupProvider) GetForeignPaths(context.Context, catalog.ForeignRequest, catalog.ForeignRelSize) ([]catalog.ForeignPath, error) {
	return []catalog.ForeignPath{{}}, nil
}

func (p *sqlCommandCleanupProvider) BeginForeignTransaction(context.Context, catalog.ForeignRequest) (catalog.ForeignTransaction, error) {
	return p.transaction, nil
}

func (p *sqlCommandCleanupProvider) BeginForeignScan(context.Context, catalog.ForeignRequest, catalog.ForeignPlan) (catalog.ForeignScan, error) {
	p.begins++
	p.observe("begin")
	return p.scan, nil
}

type sqlCommandCleanupScan struct {
	fdwUnitScan
	primary error
	observe func(string)
}

func (s *sqlCommandCleanupScan) IterateForeignScan(context.Context) (catalog.ForeignRow, bool, error) {
	s.observe("next")
	s.position++
	if s.position == 3 && s.primary != nil {
		return nil, false, s.primary
	}
	return catalog.ForeignRow{}, s.position <= 3, nil
}

func TestPLpgSQLSQLExecutionCommandCleanup(t *testing.T) {
	for _, command := range []string{"SELECT 1 FROM command_cleanup_target", "PERFORM 1 FROM command_cleanup_target", "EXECUTE 'SELECT 1 FROM command_cleanup_target'", "EXECUTE 'SELECT 1 FROM command_cleanup_target' INTO STRICT x"} {
		for _, lateFailure := range []bool{false, true} {
			name := command + "/close_only"
			if lateFailure {
				name = command + "/late_primary_and_close"
			}
			t.Run(name, func(t *testing.T) {
				e := engine.New()
				primary := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero", Detail: "late command provider fault"}
				secondary := errors.New("command scan End failure")
				scan := &sqlCommandCleanupScan{}
				if lateFailure {
					scan.primary = primary
				}
				tx := &plExceptionFaultTx{}
				provider := &sqlCommandCleanupProvider{scan: scan, transaction: tx}
				definition, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Schema: "public", Name: "command_cleanup_handler"}, Wrapper: provider})
				if err != nil {
					t.Fatal(err)
				}
				if err := e.InstallForeignDefinitions([]fdw.WrapperDefinition{definition}, nil,
					func(_ context.Context, value driver.Value, usage sqltype.Usage, _ sqltype.InputEnvironment) (sqltype.Value, error) {
						if value == nil {
							return sqltype.Null(usage.Type), nil
						}
						return sqltype.Integer(usage.Type, value.(int64))
					},
					func(_ context.Context, value sqltype.Value, _ sqltype.OutputEnvironment) (driver.Value, error) {
						if value.IsNull() {
							return nil, nil
						}
						return value.Integer()
					},
				); err != nil {
					t.Fatal(err)
				}
				sess := enginetest.StartSession(t, e, nil)
				for _, query := range []string{
					"CREATE FOREIGN DATA WRAPPER command_cleanup HANDLER command_cleanup_handler",
					"CREATE SERVER command_cleanup_server FOREIGN DATA WRAPPER command_cleanup",
					"CREATE USER MAPPING FOR CURRENT_USER SERVER command_cleanup_server",
					"CREATE FOREIGN TABLE command_cleanup_target(v integer) SERVER command_cleanup_server",
				} {
					enginetest.SessionSQL(t, sess, query)
				}
				enginetest.TransactionBegin(t, sess)
				op := enginetest.TransactionOperation(t, sess)
				defer op.Done()
				s, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Finish(context.Canceled)
				// Admit a real enclosing statement, including its transaction lock
				// ticket, snapshot and accepted description. Merely copying TxRef
				// does not attach the statement's lock/catalog authority.
				enclosing := enginetest.Statement(t, "SELECT 1")
				enclosingDescription, err := s.Describe(enclosing)
				if err != nil {
					t.Fatal(err)
				}
				s.Expected = &enclosingDescription
				enclosingResult, err := s.Execute(enclosing, 0, nil)
				if err != nil {
					t.Fatal(err)
				}
				if enclosingResult.Rows == nil {
					t.Fatal("enclosing statement did not produce rows")
				}
				row := make([]sqltype.Value, len(enclosingResult.Rows.Schema()))
				if ok, err := enclosingResult.Rows.Next(op.Context(), row); err != nil || !ok {
					t.Fatal("enclosing statement first row", ok, err)
				}
				if ok, err := enclosingResult.Rows.Next(op.Context(), row); err != nil || ok {
					t.Fatal("enclosing statement EOF", ok, err)
				}
				if err := enclosingResult.Rows.Close(); err != nil {
					t.Fatal(err)
				}
				read := e.CatalogRead()
				integer, _ := read.LookupType(sqltype.Int4)
				language, err := catalog.LookupLanguage(read, "plpgsql")
				if err != nil {
					t.Fatal(err)
				}
				// Both real exception owners must reject the fatal cleanup, rather
				// than turn either the primary or End failure into a handled SQL error.
				source := "DECLARE x integer; BEGIN BEGIN " + command + "; EXCEPTION WHEN OTHERS THEN RAISE EXCEPTION 'inner handler ran'; END; RETURN 7; EXCEPTION WHEN OTHERS THEN RAISE EXCEPTION 'outer handler ran'; END"
				raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
				if err != nil {
					t.Fatal(err)
				}
				d := &catalog.Procedure{ObjectName: "command_cleanup", Language: language.Address(), SQL: &proc.Definition{Kind: proc.PLpgSQLBody, Name: "command_cleanup", Source: source, Result: integer.Usage(), PLSyntax: raw}}
				e.Mu.Lock()
				auth, err := e.AuthorizationLocked(op)
				if err == nil {
					err = e.CompilePLRoutineLocked(op, auth, d)
				}
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				input := proc.NewFrame(nil)
				defer input.Close()
				frame := proc.NewPLFrame(d.SQL.PL, d.SQL, input, sqltype.CollationRef{})
				defer frame.Close()

				// Distinguishing nonzero enclosing scratch, not just nil-equals-nil.
				outerLexical := &proc.PLFrame{}
				core.SetRoutineDemandLexical(core.ScopeSQLRoutineRef(s), outerLexical)
				core.SetRoutineDemandClosedParameters(core.ScopeSQLRoutineRef(s), true)
				outerAuthority := core.ScopeAuthority(s)
				if outerAuthority == nil || outerAuthority.Host == nil {
					t.Fatal("enclosing statement did not retain actual authority")
				}
				outerOwner := &exec.ForeignScanOwner{}
				core.SetForeignTableScopeOwner(core.ScopeForeignTables(s), outerOwner)
				s.RoutineAuth = auth
				s.Completed = true
				core.SetScopeEffectCommand(s, 71)
				demand, expected, identity := core.ScopeSQLRoutine(s), s.Expected, s.Identity
				depth, sessionCompleted := core.ScopeActiveSQLRoutineDepth(s), core.ScopeSessionCompleted(s)
				identityText := core.ScopePLRoutineDiagnosticIdentity(s, read, d)
				var events []string
				var activeAuthority *exec.Authority
				var activeOwner *exec.ForeignScanOwner
				unlocked := plExceptionUnlocked(t, e, s)
				tx.check = unlocked
				observe := func(event string) {
					unlocked()
					events = append(events, event)
					authority := core.ScopeAuthority(s)
					owner := core.ForeignTableScopeOwner(core.ScopeForeignTablesOrZero(s))
					if event == "begin" {
						activeAuthority, activeOwner = authority, owner
					}
					lexical := frame
					if command[0] == 'E' {
						lexical = nil
					}
					if core.RoutineDemandClosedParameters(core.ScopeSQLRoutineRef(s)) != (command[0] == 'E') {
						t.Error("producer inherited or lost its parameter namespace", event)
					}
					if core.RoutineDemandLexical(core.ScopeSQLRoutineRef(s)) != lexical || authority == nil || authority == outerAuthority || authority != activeAuthority || authority.Host == nil || owner == nil || owner == outerOwner || owner != activeOwner || s.Expected == expected || s.Completed {
						t.Error("producer callback escaped actual command lexical/authority/resource/completion activation", event)
					}
				}
				provider.observe, scan.observe = observe, observe
				scan.onEnd = func(ctx context.Context) error {
					observe("end")
					if ctx.Err() != nil {
						t.Error("producer End inherited cancellation")
					}
					return secondary
				}

				// Dispatch the real checked instruction through its host, retaining
				// actual cleanup ownership rather than manually draining a result.
				_, flow, err := core.ScopeNewPLRun(s, d, frame, read).Block(d.SQL.PL.Root)
				var fatal *proc.PLExceptionCleanupError
				if !errors.As(err, &fatal) || !errors.Is(err, secondary) || lateFailure && !errors.Is(err, primary) || flow.Kind != proc.PLNormal {
					t.Fatal("command lost fatal cleanup/primary/secondary identity or entered an exception handler", flow, err)
				}
				if !errors.Is(fatal.Cleanup, secondary) || lateFailure && !errors.Is(fatal.Primary, primary) {
					t.Fatal("fatal wrapper lost its primary/cleanup ownership", fatal)
				}
				if lateFailure {
					var diagnostic *sqlstate.Error
					want := *primary
					action := "at SQL statement"
					query := command
					if command[0] == 'P' {
						action = "at PERFORM"
						query = "SELECT" + command[len("PERFORM"):]
					}
					if command[0] == 'E' {
						action = "at EXECUTE"
						query = "SELECT 1 FROM command_cleanup_target"
					}
					want.Context = "SQL statement \"" + query + "\"\nPL/pgSQL function " + identityText + " line 1 " + action
					if !errors.As(err, &diagnostic) || *diagnostic != want {
						t.Fatalf("command cleanup changed complete primary diagnostic: got %#v want %#v", diagnostic, want)
					}
				}
				wantEvents := []string{"begin", "next", "next", "next", "end"}
				if !lateFailure {
					wantEvents = []string{"begin", "next", "next", "next", "next", "end"}
				}
				if provider.begins != 1 || scan.ends != 1 || scan.rescans != 0 || scan.cleanupCanceled || !reflect.DeepEqual(events, wantEvents) {
					t.Fatal("command did not fully drain/close each admitted producer exactly once in order", provider.begins, scan.ends, scan.rescans, events)
				}
				if !core.SameSQLRoutineDemand(core.ScopeSQLRoutine(s), demand) || core.ScopeAuthority(s) != outerAuthority || outerAuthority.Host == nil || activeAuthority.Host != nil || core.ForeignTableScopeOwner(core.ScopeForeignTablesOrZero(s)) != outerOwner || s.Expected != expected || !s.Completed || core.ScopeSessionCompleted(s) != sessionCompleted || core.ScopeActiveSQLRoutineDepth(s) != depth || core.ScopeEffectCommand(s) != 71 || !reflect.DeepEqual(s.RoutineAuth, auth) || s.Identity != identity || len(frame.Scopes) != 0 || frame.HandledError() != nil {
					t.Fatal("fatal command did not restore actual enclosing scratch/authority/resource/completion state")
				}
			})
		}
	}
}

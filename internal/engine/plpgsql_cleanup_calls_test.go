package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type plExceptionCleanupProvider struct {
	fdwUnitLifecycle
	transaction *plExceptionFaultTx
}

func (p *plExceptionCleanupProvider) BeginForeignTransaction(context.Context, catalog.ForeignRequest) (catalog.ForeignTransaction, error) {
	return p.transaction, nil
}

func TestPLExceptionCleanupCalledRoutines(t *testing.T) {
	for _, wrapper := range []bool{false, true} {
		for _, site := range []string{"assignment", "partial_assignment", "RETURN", "initializer", "simple_CASE_WHEN"} {
			name := "called_pl/" + site
			if wrapper {
				name = "sql_wrapper/" + site
			}
			t.Run(name, func(t *testing.T) {
				e := New()
				original := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero", Detail: "provider original detail"}
				failure := errors.New("modification End failure")
				tx := &plExceptionFaultTx{}
				provider := &plExceptionCleanupProvider{fdwUnitLifecycle: fdwUnitLifecycle{
					fdwUnitSingle: fdwUnitSingle{fdwUnitInsert: &fdwUnitInsert{present: true}},
					beginError:    original, endError: failure,
				}, transaction: tx}
				definition, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Schema: "public", Name: "ex_cleanup_handler"}, Wrapper: provider})
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
						integer, err := value.Integer()
						return integer, err
					},
				); err != nil {
					t.Fatal(err)
				}
				sess := sessionStart(t, e, nil)
				for _, query := range []string{
					"CREATE FOREIGN DATA WRAPPER ex_cleanup HANDLER ex_cleanup_handler",
					"CREATE SERVER ex_cleanup_server FOREIGN DATA WRAPPER ex_cleanup",
					"CREATE USER MAPPING FOR CURRENT_USER SERVER ex_cleanup_server",
					"CREATE FOREIGN TABLE ex_cleanup_target(v integer) SERVER ex_cleanup_server",
					"CREATE FUNCTION ex_cleanup_write() RETURNS integer LANGUAGE SQL AS $$INSERT INTO ex_cleanup_target VALUES(1); SELECT 1$$",
					"CREATE FUNCTION ex_cleanup_called() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN ex_cleanup_write(); EXCEPTION WHEN OTHERS THEN RETURN 91; END$$",
					"CREATE FUNCTION ex_cleanup_wrapper() RETURNS integer LANGUAGE SQL AS $$SELECT 1; SELECT ex_cleanup_called()$$",
				} {
					sessionSQL(t, sess, query)
				}
				callee := "ex_cleanup_called()"
				if wrapper {
					callee = "ex_cleanup_wrapper()"
				}
				body := "DECLARE x integer; BEGIN x := " + callee + "; RETURN x; END"
				if site == "partial_assignment" {
					body = "DECLARE x integer[] := ARRAY[1]; BEGIN x[1] := " + callee + "; RETURN x[1]; END"
				} else if site == "RETURN" {
					body = "BEGIN RETURN " + callee + "; END"
				} else if site == "initializer" {
					body = "DECLARE x integer := " + callee + "; BEGIN RETURN x; END"
				} else if site == "simple_CASE_WHEN" {
					body = "BEGIN CASE 1 WHEN " + callee + " THEN RETURN 1; ELSE RETURN 2; END CASE; END"
				}
				sessionSQL(t, sess, "CREATE FUNCTION ex_cleanup_middle() RETURNS integer LANGUAGE plpgsql AS $$"+body+"$$")
				sessionSQL(t, sess, "CREATE FUNCTION ex_cleanup_outer() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN ex_cleanup_middle(); EXCEPTION WHEN OTHERS THEN RETURN 99; END$$")
				op := transactionOperation(t, sess)
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(context.Canceled)
				provider.check = plExceptionUnlocked(t, e, scope)
				tx.check = provider.check
				result, err := scope.Execute(statement(t, "SELECT ex_cleanup_outer()"), 0, nil)
				if result.Rows != nil {
					defer result.Rows.Close()
					if err == nil {
						_, err = result.Rows.Next(op.Context(), make([]sqltype.Value, len(result.Rows.Schema())))
					}
				}
				var cleanup *proc.PLExceptionCleanupError
				var diagnostic *sqlstate.Error
				if !errors.As(err, &cleanup) || !errors.Is(err, failure) || !errors.Is(err, original) {
					t.Fatalf("called routine lost fatal cleanup identity/evidence or ran OTHERS: %v", err)
				}
				action := "at " + site
				if site == "partial_assignment" {
					action = "at assignment"
				}
				if site == "initializer" {
					action = "during statement block local variable initialization"
				} else if site == "simple_CASE_WHEN" {
					action = "at CASE"
				}
				want := *original
				want.Context = "SQL function \"ex_cleanup_write\" statement 1\nPL/pgSQL function ex_cleanup_called() line 1 at RETURN\n"
				if wrapper {
					want.Context += "SQL function \"ex_cleanup_wrapper\" statement 2\n"
				}
				want.Context += "PL/pgSQL function ex_cleanup_middle() line 1 " + action + "\nPL/pgSQL function ex_cleanup_outer() line 1 at RETURN"
				if !errors.As(err, &diagnostic) || *diagnostic != want {
					t.Fatalf("complete diagnostic changed across cleanup calls: got %+v want %+v", diagnostic, want)
				}
				rollbacks := 0
				for _, call := range tx.calls {
					if strings.HasPrefix(call, "rollback:") {
						rollbacks++
					} else if call == "rollback" {
						t.Fatal("successful provider RollbackTo disposed participant", tx.calls)
					}
				}
				if provider.begins != 1 || provider.ends != 1 || rollbacks < 2 || tx.rollbackError != nil {
					t.Fatal("fault did not exercise Modification.End plus successful inner/outer RollbackTo", provider.begins, provider.ends, tx.calls)
				}
			})
		}
	}
}

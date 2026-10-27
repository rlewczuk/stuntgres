package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func transactionUnitDiagnostic(t *testing.T, err error, message, action string) {
	t.Helper()
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2D000", Message: message, Context: "PL/pgSQL function transaction_unit() line 1 at " + action}
	var got *sqlstate.Error
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("transaction diagnostic = %+v (%v), want %+v", got, err, want)
	}
}

func transactionUnitRun(t *testing.T, source string) (*core.StatementScope, *proc.PLRun, proc.PLInstruction) {
	t.Helper()
	_, s := plUnitScope(t)
	if err := s.Gate(&parser.Do{}); err != nil {
		t.Fatal(err)
	}
	return transactionUnitRunOnScope(t, s, source)
}

func transactionUnitRunOnScope(t *testing.T, s *core.StatementScope, source string) (*core.StatementScope, *proc.PLRun, proc.PLInstruction) {
	t.Helper()
	e := s.Engine()
	read := e.CatalogRead()
	integer, _ := read.LookupType(sqltype.Int4)
	language, err := catalog.LookupLanguage(read, "plpgsql")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	d := &catalog.Procedure{ObjectName: "transaction_unit", Language: language.Address(), RoutineKind: catalog.FunctionRoutine, SQL: &proc.Definition{Kind: proc.PLpgSQLBody, Name: "transaction_unit", Source: source, Result: integer.Usage(), PLSyntax: raw}}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err == nil {
		err = e.CompilePLRoutineLocked(s.Op, auth, d)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	input := proc.NewFrame(nil)
	t.Cleanup(input.Close)
	frame := proc.NewPLFrame(d.SQL.PL, d.SQL, input, sqltype.CollationRef{})
	t.Cleanup(frame.Close)
	run := core.ScopeNewPLRun(s, d, frame, read)
	return s, run, d.SQL.PL.Root.Statements[0].Block.Statements[0]
}

func TestPLpgSQLTransactionRotationOwnership(t *testing.T) {
	e := engine.New()
	setup := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, setup, "CREATE TABLE tx_rotation(v integer)")
	relation := enginetest.RoleTableRef(t, e, "tx_rotation")
	backing := e.Storage.Relations[relation.Address]
	generation := backing.Generation
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(done)
	s, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Finish(errors.New("owner cleanup")) })
	if err := s.Gate(&parser.Do{}); err != nil {
		t.Fatal(err)
	}
	_, run, rollback := transactionUnitRunOnScope(t, s, "BEGIN ROLLBACK;COMMIT;RETURN 7;END")
	commit := run.Frame.Program.Root.Statements[0].Block.Statements[1]
	owner, start := s.Owner, core.ScopeStatementStart(s)
	if _, err := s.Execute(enginetest.Statement(t, "INSERT INTO tx_rotation VALUES(11)"), 0, nil); err != nil {
		t.Fatal(err)
	}
	if len(s.Stage.Rows[relation.Address]) != 1 || backing.Generation != generation || len(backing.Versions) != 0 {
		t.Fatal("staging changed committed backing")
	}
	ref, ticket := s.TxRef, s.LockTicket
	if err := run.Host.Transaction(commit); err != nil {
		t.Fatal(err)
	}
	if s.Op != op || s.Owner != owner || s.TxRef == ref || s.TxRef.Session() != ref.Session() || s.LockTicket == ticket || owner.State != session.TxImplicit || len(s.Stage.Rows) != 0 || len(owner.Writes) != 0 || core.ScopeStatementStart(s) != start {
		t.Fatal("rotation changed root or retained old incarnation")
	}
	if err := owner.Validate(ref); err == nil {
		t.Fatal("old identity owns successor")
	}
	current := e.Storage.Relations[relation.Address]
	if current.Generation <= generation || len(current.Versions) != 1 {
		t.Fatal("commit did not publish its stage")
	}
	publishedGeneration := current.Generation
	if _, err := s.Execute(enginetest.Statement(t, "INSERT INTO tx_rotation VALUES(22)"), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := run.Host.Transaction(rollback); err != nil {
		t.Fatal(err)
	}
	if len(s.Stage.Rows) != 0 || len(owner.Writes) != 0 || current.Generation != publishedGeneration || len(current.Versions) != 1 {
		t.Fatal("rollback published or retained current segment")
	}
	for _, control := range []proc.PLInstruction{commit, rollback, commit} {
		old := s.TxRef
		if err := run.Host.Transaction(control); err != nil {
			t.Fatal(err)
		}
		if old == s.TxRef || owner.State != session.TxImplicit || current.Generation != publishedGeneration || len(current.Versions) != 1 {
			t.Fatal("empty boundary republished old stage")
		}
	}
	if err := s.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if owner.State != session.TxIdle || current.Generation != publishedGeneration {
		t.Fatal("final Finish retained or republished successor")
	}
	if err := s.Finish(nil); err != nil {
		t.Fatal("idempotent Finish", err)
	}
}

// A nested PL expression bypasses the public StatementScope.Execute wrapper.
// Its first admission after CHAIN must establish the new owner's snapshot,
// without closing the characteristics window at the transaction boundary.
func TestPLpgSQLTransactionChainSnapshotOwnership(t *testing.T) {
	for _, command := range []string{"COMMIT", "ROLLBACK"} {
		for _, isolation := range []struct {
			name string
			mode parser.TransactionIsolation
		}{{"repeatable_read", parser.IsolationRepeatableRead}, {"serializable", parser.IsolationSerializable}} {
			t.Run(command+"/"+isolation.name, func(t *testing.T) {
				s, run, control := transactionUnitRun(t, "BEGIN "+command+" AND CHAIN;RETURN 7;END")
				modes := &parser.TransactionCharacteristics{Modes: []parser.TransactionMode{
					{Isolation: isolation.mode},
					{AccessSet: true, ReadOnly: true},
					{DeferrableSet: true, Deferrable: true},
				}}
				if _, err := s.Execute(modes, 0, nil); err != nil {
					t.Fatal(err)
				}
				if err := core.ScopeSnapshotDataPhase(s); err != nil {
					t.Fatal(err)
				}
				owner, op := s.Owner, s.Op
				expectedIsolation := owner.Isolation
				start, deadline := core.ScopeStatementStart(s), op.StatementDeadline
				statement := enginetest.Statement(t, "SELECT 7").(*parser.Select)
				for range 2 {
					oldRef, oldTicket, oldSnapshot := s.TxRef, s.LockTicket, owner.CurrentSnapshot
					outer := s.ActivateSQLRoutine(nil, s.Engine().CatalogRead())
					inner := s.ActivateSQLRoutine(nil, s.Engine().CatalogRead())
					if err := run.Host.Transaction(control); err != nil {
						t.Fatal(err)
					}
					if s.Op != op || s.Owner != owner || s.TxRef == oldRef || s.LockTicket == oldTicket || owner.State != session.TxImplicit {
						t.Fatal("CHAIN did not attach a fresh implicit owner incarnation")
					}
					if owner.SnapshotSet || owner.CurrentSnapshot != nil || core.ScopeCurrentSnapshot(s) != nil || owner.Roots != nil {
						t.Fatal("successor inherited or eagerly acquired a snapshot")
					}
					if owner.Isolation != expectedIsolation || !owner.ReadOnly || !owner.Deferrable {
						t.Fatal("CHAIN lost effective characteristics")
					}
					// Deferrable assignment refuses even the same value after
					// snapshot acquisition, so this proves lazy restart admission.
					if _, err := s.Execute(modes, 0, nil); err != nil {
						t.Fatal("fresh successor closed SET TRANSACTION window", err)
					}
					value, _, err := run.Host.Evaluate(run.Frame, statement, proc.PLOrigin{})
					if err != nil {
						t.Fatal("first nested expression in chained successor", err)
					}
					if integer, err := value.Integer(); err != nil || integer != 7 {
						t.Fatal("nested expression result", value)
					}
					snapshot := core.ScopeCurrentSnapshot(s)
					frontier := core.ScopeSnapshotFrontier(s)
					if !owner.SnapshotSet || snapshot == nil || snapshot != owner.CurrentSnapshot || snapshot == oldSnapshot || frontier != owner.Frontier {
						t.Fatal("nested admission did not retain the successor's snapshot/frontier pair")
					}
					if err := inner(nil); err != nil {
						t.Fatal(err)
					}
					if err := outer(nil); err != nil {
						t.Fatal(err)
					}
					if core.ScopeCurrentSnapshot(s) != snapshot || owner.CurrentSnapshot != snapshot || core.ScopeSnapshotFrontier(s) != frontier || s.Input.TransactionStart != owner.Start {
						t.Fatal("nested restoration revived the prior snapshot or transaction start")
					}
					if core.ScopeStatementStart(s) != start || op.StatementDeadline != deadline {
						t.Fatal("CHAIN refreshed the original statement budget")
					}
					if err := owner.Validate(oldRef); err == nil {
						t.Fatal("old transaction identity owns chained successor")
					}
				}
				if err := s.Finish(nil); err != nil {
					t.Fatal(err)
				}
				if owner.State != session.TxIdle || owner.CurrentSnapshot != nil {
					t.Fatal("final Finish retained successor snapshot ownership")
				}
			})
		}
	}
}

func TestPLpgSQLTransactionScratchRebase(t *testing.T) {
	t.Run("retained_definition", transactionPreparedOwnership)
	s, run, in := transactionUnitRun(t, "BEGIN ROLLBACK;RETURN 7;END")
	read := s.Engine().CatalogRead()
	old := s.TxRef
	start := core.ScopeStatementStart(s)
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90002}
	savedWrites := map[ids.ObjectAddress][]storage.RowVersion{a: {{Row: 1, Version: 1}}}
	s.OwnWrites = savedWrites
	s.Completed = true
	outer := s.ActivateSQLRoutine(nil, read)
	inner := s.ActivateSQLRoutine(nil, read)
	s.Completed = true
	if err := run.Host.Transaction(in); err != nil {
		t.Fatal(err)
	}
	if err := inner(nil); err != nil {
		t.Fatal(err)
	}
	if err := outer(nil); err != nil {
		t.Fatal(err)
	}
	if s.TxRef == old || s.Input.TransactionStart != s.Owner.Start || core.ScopeStatementStart(s) != start || s.Completed || s.OwnWrites != nil || s.OwnDeletes != nil {
		t.Fatal("saved activation resurrected transaction scratch")
	}
	if core.RoutineDemandLexical(core.ScopeSQLRoutineRef(s)) != nil {
		t.Fatal("lexical frame leaked")
	}
}

func TestPLpgSQLTransactionContextPrecedence(t *testing.T) {
	s, run, in := transactionUnitRun(t, "BEGIN COMMIT;RETURN 7;END")
	mark, err := s.BeginPLException()
	if err != nil {
		t.Fatal(err)
	}
	err = run.Host.Transaction(in)
	transactionUnitDiagnostic(t, err, "cannot commit while a subtransaction is active", "COMMIT")
	ref := s.TxRef
	if err := s.EndPLException(mark, true, nil); err != nil {
		t.Fatal(err)
	}
	if s.TxRef != ref {
		t.Fatal("refusal ended protected segment")
	}
	if err := run.Host.Transaction(in); err != nil {
		t.Fatal("consumed mark still forbids termination", err)
	}
	_, explicit := plExceptionUnitScope(t)
	_, nested, control := transactionUnitRunOnScope(t, explicit, "BEGIN ROLLBACK;RETURN 7;END")
	protected, err := explicit.BeginPLException()
	if err != nil {
		t.Fatal(err)
	}
	transactionUnitDiagnostic(t, nested.Host.Transaction(control), "invalid transaction termination", "ROLLBACK")
	if err := explicit.EndPLException(protected, true, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPLpgSQLTransactionSQLBodyPolicyOwnership(t *testing.T) {
	for _, command := range []string{"COMMIT", "ROLLBACK"} {
		for _, terminate := range []string{"false", "true"} {
			t.Run(command+"/"+terminate, func(t *testing.T) {
				e := engine.New()
				setup := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, setup, "CREATE PROCEDURE tx_owner_inner(IN terminate boolean) LANGUAGE plpgsql AS $$BEGIN IF terminate THEN "+command+";END IF;END$$")
				enginetest.SessionSQL(t, setup, "CREATE PROCEDURE tx_owner_sql() LANGUAGE SQL AS 'CALL tx_owner_inner("+terminate+")'")
				op, done, err := enginetest.Operation(e, context.Background())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(done)
				s, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = s.Finish(errors.New("owner cleanup")) })
				if err := s.Gate(&parser.Do{}); err != nil {
					t.Fatal(err)
				}
				_, run, control := transactionUnitRunOnScope(t, s, "BEGIN "+command+";RETURN 7;END")
				ref, ticket, owner := s.TxRef, s.LockTicket, s.Owner
				depth := core.ScopeActiveSQLRoutineDepth(s)
				_, err = s.Execute(enginetest.Statement(t, "CALL tx_owner_sql()"), 0, nil)
				if terminate == "true" {
					want := sqlstate.Error{
						Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2D000",
						Message: "invalid transaction termination",
						Context: "PL/pgSQL function tx_owner_inner(boolean) line 1 at " + command + "\nSQL function \"tx_owner_sql\" statement 1",
					}
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || *diagnostic != want {
						t.Fatalf("SQL body diagnostic = %+v (%v), want %+v", diagnostic, err, want)
					}
				} else if err != nil {
					t.Fatal("unreached control was rejected", err)
				}
				if s.Op != op || s.Owner != owner || s.TxRef != ref || s.LockTicket != ticket || owner.State != session.TxImplicit || core.ScopeActiveSQLRoutineDepth(s) != depth {
					t.Fatal("SQL body ended its borrowed incarnation or leaked invocation depth")
				}
				// Exercise the original eligible host before Finish or another
				// command admission can reset any invocation-private policy.
				if err := run.Host.Transaction(control); err != nil {
					t.Fatal("SQL body did not restore the live caller's eligibility", err)
				}
				if s.Op != op || s.Owner != owner || s.TxRef == ref || s.LockTicket == ticket || owner.State != session.TxImplicit {
					t.Fatal("restored caller did not rotate its original owner")
				}
				if err := s.Finish(nil); err != nil {
					t.Fatal(err)
				}
				if owner.State != session.TxIdle {
					t.Fatal("final Finish retained the restored caller's successor")
				}
			})
		}
	}
}

type transactionProducerOwner struct {
	exec.RowResult
	closes     int
	closeError error
}

func (p *transactionProducerOwner) Close() error {
	p.closes++
	err := p.RowResult.Close()
	if err != nil {
		return err
	}
	return p.closeError
}

func TestPLpgSQLTransactionLoopPromotion(t *testing.T) {
	t.Run("bound_cursor", transactionBoundOwnership)
	for _, query := range []string{"SELECT x AS i FROM generate_series(1,3) x", "SELECT FROM generate_series(1,3) x"} {
		for _, command := range []string{"COMMIT", "ROLLBACK"} {
			t.Run(command+"/"+query, func(t *testing.T) {
				s, run, in := transactionUnitRun(t, "DECLARE r record;BEGIN FOR r IN "+query+" LOOP "+command+";END LOOP;RETURN 7;END")
				run.Frame.Enter(run.Frame.Program.Root)
				defer run.Frame.Leave(run.Frame.Program.Root)
				block := run.Frame.Program.Root.Statements[0].Block
				run.Frame.Enter(block)
				defer run.Frame.Leave(block)
				rows, err := run.Host.OpenQuery(run.Frame, in)
				if err != nil {
					t.Fatal(err)
				}
				closed := false
				defer func() {
					if !closed {
						_ = rows.Close(nil)
					}
				}()
				producer := &transactionProducerOwner{RowResult: core.PLQueryRowsResultRef(rows).Rows}
				core.PLQueryRowsResultRef(rows).Rows = producer
				dst := make([]sqltype.Value, rows.Width())
				if ok, err := rows.Next(context.Background(), dst); !ok || err != nil {
					t.Fatal(ok, err)
				}
				if len(dst) != 0 {
					if value, _ := dst[0].Integer(); value != 1 {
						t.Fatal(value)
					}
				}
				old := s.TxRef
				control := in.QueryLoop.Body.Statements[0]
				if err := run.Host.Transaction(control); err != nil {
					t.Fatal(err)
				}
				if old == s.TxRef || producer.closes != 1 || core.PLQueryRowsResultRef(rows).Rows != nil {
					t.Fatal("promotion retained transaction producer")
				}
				for _, want := range []int64{2, 3} {
					if ok, err := rows.Next(context.Background(), dst); !ok || err != nil {
						t.Fatal(ok, err)
					}
					if len(dst) != 0 {
						if got, _ := dst[0].Integer(); got != want {
							t.Fatal(got, want)
						}
					}
				}
				if ok, err := rows.Next(context.Background(), dst); ok || err != nil {
					t.Fatal(ok, err)
				}
				closed = true
				if err := rows.Close(nil); err != nil {
					t.Fatal(err)
				}
				if producer.closes != 1 {
					t.Fatal("loop cleanup closed promoted producer twice")
				}
			})
		}
	}
}

func TestPLpgSQLTransactionCleanupOwnership(t *testing.T) {
	t.Run("bound_cursor", transactionBoundCleanup)
	t.Run("drain_close_failure_before_publication", func(t *testing.T) {
		s, run, in := transactionUnitRun(t, "DECLARE r record;BEGIN FOR r IN SELECT x FROM generate_series(1,3) x LOOP COMMIT;END LOOP;RETURN 7;END")
		run.Frame.Enter(run.Frame.Program.Root)
		defer run.Frame.Leave(run.Frame.Program.Root)
		block := run.Frame.Program.Root.Statements[0].Block
		run.Frame.Enter(block)
		defer run.Frame.Leave(block)
		rows, err := run.Host.OpenQuery(run.Frame, in)
		if err != nil {
			t.Fatal(err)
		}
		closed := false
		defer func() {
			if !closed {
				_ = rows.Close(nil)
			}
		}()
		failure := errors.New("producer close failure")
		producer := &transactionProducerOwner{RowResult: core.PLQueryRowsResultRef(rows).Rows, closeError: failure}
		core.PLQueryRowsResultRef(rows).Rows = producer
		dst := make([]sqltype.Value, rows.Width())
		if present, err := rows.Next(context.Background(), dst); !present || err != nil {
			t.Fatal(present, err)
		}
		ref := s.TxRef
		err = run.Host.Transaction(in.QueryLoop.Body.Statements[0])
		if !errors.Is(err, failure) || s.TxRef != ref || producer.closes != 1 || core.PLQueryRowsResultRef(rows).Rows != nil {
			t.Fatal("failed holding published or retained producer", err)
		}
		err = rows.Close(err)
		closed = true
		if !errors.Is(err, failure) || producer.closes != 1 {
			t.Fatal("terminal cleanup repeated producer close", err)
		}
		if err = s.Finish(err); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		if s.Owner.State != session.TxIdle || s.Owner.Ticket != nil {
			t.Fatal("failed holding retained transaction")
		}
	})
	for _, primary := range []error{nil, errors.New("body failure"), context.Canceled} {
		t.Run(func() string {
			if primary == nil {
				return "success"
			}
			return primary.Error()
		}(), func(t *testing.T) {
			s, run, in := transactionUnitRun(t, "BEGIN ROLLBACK;RETURN 7;END")
			if err := run.Host.Transaction(in); err != nil {
				t.Fatal(err)
			}
			if err := s.Finish(primary); !errors.Is(err, primary) {
				t.Fatal(err, primary)
			}
			if s.Owner.State != session.TxIdle || s.Owner.Ticket != nil || s.LockTicket != nil || s.Stage.Rows != nil || s.View != nil {
				t.Fatal("terminal cleanup retained successor resources")
			}
		})
	}
}

func TestPLpgSQLTransactionCommandContext(t *testing.T) {
	t.Run("original_message_after_end", func(t *testing.T) {
		_, scope := plUnitScope(t)
		if err := scope.RetainMessage(); err != nil {
			t.Fatal(err)
		}
		messageScope := scope
		defer func() { _ = messageScope.FinishMessage(errors.New("message owner cleanup")) }()
		_, err := scope.Execute(&parser.TransactionControl{Kind: parser.KindCommit}, 0, nil)
		if err = scope.Finish(err); err != nil {
			t.Fatal(err)
		}
		next, err := scope.NextStatement()
		if err != nil {
			t.Fatal(err)
		}
		messageScope = next
		if err := next.Gate(&parser.Do{}); err != nil {
			t.Fatal(err)
		}
		_, run, in := transactionUnitRunOnScope(t, next, "BEGIN ROLLBACK;RETURN 7;END")
		err = run.Host.Transaction(in)
		transactionUnitDiagnostic(t, err, "invalid transaction termination", "ROLLBACK")
		err = next.Finish(err)
		_ = next.FinishMessage(err)
		if next.Owner.RetainedMessage || next.Owner.State != session.TxIdle {
			t.Fatal("message end retained incarnation")
		}
	})
	e, s := plUnitScope(t)
	if err := s.Finish(nil); err != nil {
		t.Fatal(err)
	}
	s.Op.Done()
	for _, limit := range []int{-1, 0, 1} {
		t.Run(map[int]string{-1: "bound_only", 0: "completed", 1: "suspended"}[limit], func(t *testing.T) {
			sess := enginetest.StartSession(t, e, nil)
			cycle := sess.NewProtocolCycle()
			command := func(fn func(*core.StatementScope) error) {
				op, _, err := sess.AcquireCommand(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := e.StartProtocolCommand(op, cycle)
				if err != nil {
					op.Done()
					t.Fatal(err)
				}
				defer scope.Finish(errors.New("command owner cleanup"))
				err = scope.Finish(fn(scope))
				op.Done()
				if err != nil {
					t.Fatal(err)
				}
			}
			command(func(scope *core.StatementScope) error {
				_, err := scope.ParseProtocol("prior", "SELECT x FROM generate_series(1,2) x", nil)
				return err
			})
			command(func(scope *core.StatementScope) error {
				_, err := scope.BindProtocolPortal("prior", "prior", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
				return err
			})
			command(func(scope *core.StatementScope) error { _, err := scope.DescribeProtocolPortal("prior"); return err })
			if cycle.Executed {
				t.Fatal("preparation executed")
			}
			if limit >= 0 {
				command(func(scope *core.StatementScope) error {
					_, err := scope.ExecuteProtocolPortal("prior", uint32(limit), func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
					return err
				})
			}
			op, _, err := sess.AcquireCommand(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartProtocolCommand(op, cycle)
			if err != nil {
				op.Done()
				t.Fatal(err)
			}
			defer scope.Finish(errors.New("command owner cleanup"))
			_, run, in := transactionUnitRunOnScope(t, scope, "BEGIN ROLLBACK;RETURN 7;END")
			old := scope.TxRef
			err = run.Host.Transaction(in)
			if limit < 0 {
				if err != nil || scope.TxRef == old || scope.Owner.State != session.TxImplicit {
					t.Fatal("first execution was atomic", err)
				}
			} else {
				transactionUnitDiagnostic(t, err, "invalid transaction termination", "ROLLBACK")
				if scope.TxRef != old {
					t.Fatal("refused control ended incarnation")
				}
			}
			_ = scope.Finish(err)
			op.Done()
			op, _, err = sess.AcquireCommand(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			if err := e.SyncProtocolCycle(op, cycle); err != nil {
				t.Fatal(err)
			}
			op.Done()
			if sess.Transactions.State != session.TxIdle || cycle.Active || cycle.Executed {
				t.Fatal("Sync retained pipeline context")
			}
		})
	}
}

func TestPLpgSQLTransactionInstruction(t *testing.T) {
	_, run, in := transactionUnitRun(t, "BEGIN COMMIT AND CHAIN;RETURN 7;END")
	definition, program := run.Definition, run.Frame.Program
	if err := program.ValidateDefinition(definition); err != nil {
		t.Fatal(err)
	}
	if in.Transaction == in.Syntax.Transaction || in.Transaction.Kind != parser.KindCommit || !in.Transaction.Chain {
		t.Fatal("checked payload borrowed mutable syntax")
	}
	saved := *in.Transaction
	in.Transaction.Chain = false
	if err := program.ValidateDefinition(definition); err == nil {
		t.Fatal("forged checked option accepted")
	}
	*in.Transaction = saved
	if err := program.ValidateDefinition(definition); err != nil {
		t.Fatal(err)
	}
	if run.Frame.Program != program || run.Definition != definition {
		t.Fatal("frame lost immutable definition")
	}
}

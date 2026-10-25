package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

func truncateRestartUnitAST(names ...string) *parser.Truncate {
	s := truncateUnitAST(names...)
	s.RestartIdentity = true
	return s
}

func truncateRestartUnitFixture(t *testing.T) (*core.Engine, *session.Session, ids.ObjectRef, ids.ObjectRef, ids.ObjectRef, ids.ObjectRef) {
	t.Helper()
	e := engine.New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id SERIAL PRIMARY KEY)", "CREATE TABLE q(id INT GENERATED ALWAYS AS IDENTITY(START 44))", "INSERT INTO p DEFAULT VALUES", "INSERT INTO p DEFAULT VALUES", "INSERT INTO q DEFAULT VALUES"} {
		sessionSQL(t, s, q)
	}
	return e, s, roleTableRef(t, e, "p"), roleTableRef(t, e, "q"), roleTableRef(t, e, "p_id_seq"), roleTableRef(t, e, "q_id_seq")
}

func truncateRestartUnitStage(t *testing.T, e *core.Engine, s *session.Session, names ...string) *core.StatementScope {
	t.Helper()
	scope, _ := truncateUnitScope(t, e, s, context.Background())
	if _, err := scope.Execute(truncateRestartUnitAST(names...), 0, nil); err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestTruncateRestartGeneration(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "Rollback", true: "Commit"}[commit], func(t *testing.T) {
			e, s, p, q, ps, qs := truncateRestartUnitFixture(t)
			oldP, oldQ := e.Storage.Relations[p.Address], e.Storage.Relations[q.Address]
			oldPS, oldQS := e.Storage.Sequences[ps.Address], e.Storage.Sequences[qs.Address]
			psValue, qsValue := *oldPS, *oldQS
			oldPD, oldQD := e.Catalog.State.MustRelation(ps.Address), e.Catalog.State.MustRelation(qs.Address)
			frontier := e.Visibility.CommitSeq()
			scope := truncateRestartUnitStage(t, e, s, "p", "q", "p")
			if len(core.ScopeTruncateOrZero(scope).Sequences) != 2 || len(scope.Catalog.Generators) != 2 {
				t.Fatal("owned sequences not deduplicated")
			}
			for _, test := range []struct {
				ref   ids.ObjectRef
				old   *storage.SequenceGenerator
				d     *catalog.Relation
				start int64
			}{{ps, oldPS, oldPD, 1}, {qs, oldQS, oldQD, 44}} {
				replacement := scope.Catalog.Generators[test.ref.Address]
				d := scope.Catalog.Descriptors[test.ref.Address].(*catalog.Relation)
				if replacement == nil || replacement == test.old || replacement.Last != test.start || replacement.Called || replacement.Generation != d.Revision() || d.SequencePlanStamp != d.Revision() || d.SequencePlanStamp == test.d.SequencePlanStamp || d.Sequence != test.d.Sequence || d.Owner != test.d.Owner || d.Address() != test.d.Address() {
					t.Fatal("not a fresh configured-start binding with preserved definition")
				}
				if scope.Catalog.Originals[test.ref.Address] != test.d {
					t.Fatal("lost committed descriptor fence")
				}
			}
			if e.Storage.Sequences[ps.Address] != oldPS || e.Storage.Sequences[qs.Address] != oldQS || e.Storage.Relations[p.Address] != oldP || e.Storage.Relations[q.Address] != oldQ || e.Visibility.CommitSeq() != frontier {
				t.Fatal("staging published a prefix")
			}
			var veto error
			if !commit {
				veto = context.Canceled
			}
			if err := scope.Finish(veto); !errors.Is(err, veto) {
				t.Fatal(err)
			}
			scope.Op.Done()
			if *oldPS != psValue || *oldQS != qsValue || len(oldP.Versions) != 2 || len(oldQ.Versions) != 1 {
				t.Fatal("predecessor mutated")
			}
			if commit {
				if e.Storage.Sequences[ps.Address] != scope.Catalog.Generators[ps.Address] || e.Storage.Sequences[qs.Address] != scope.Catalog.Generators[qs.Address] || e.Storage.Relations[p.Address] == oldP || e.Storage.Relations[q.Address] == oldQ || e.Visibility.CommitSeq() != frontier+1 {
					t.Fatal("table and generator set not installed at one frontier")
				}
			} else if e.Storage.Sequences[ps.Address] != oldPS || e.Storage.Sequences[qs.Address] != oldQS || e.Catalog.State.Get(ps.Address) != oldPD || e.Catalog.State.Get(qs.Address) != oldQD || e.Storage.Relations[p.Address] != oldP || e.Storage.Relations[q.Address] != oldQ || e.Visibility.CommitSeq() != frontier {
				t.Fatal("veto leaked staged state")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestTruncateRestartPublication(t *testing.T) {
	e, s, p, q, ps, qs := truncateRestartUnitFixture(t)
	peer := sessionStart(t, e, nil)
	oldP, oldQ, oldPS, oldQS := e.Storage.Relations[p.Address], e.Storage.Relations[q.Address], e.Storage.Sequences[ps.Address], e.Storage.Sequences[qs.Address]
	oldD := e.Catalog.State.Get(ps.Address)
	tx := transactionBegin(t, s)
	sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 3)
	truncateUnitExecute(t, e, s, truncateRestartUnitAST("p", "q"))
	first := s.Transactions.Catalog
	sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 1)
	truncateUnitExecute(t, e, s, truncateRestartUnitAST("p", "q"))
	second := s.Transactions.Catalog
	if first.Generators[ps.Address] == second.Generators[ps.Address] || second.Originals[ps.Address] != oldD || second.Storage[p.Address].Original != oldP || second.Storage[q.Address].Original != oldQ || e.Storage.Sequences[ps.Address] != oldPS || oldPS.Last != 3 || e.Storage.Sequences[qs.Address] != oldQS {
		t.Fatal("repeat lost originals or mutated live binding")
	}
	frontier := e.Visibility.CommitSeq()
	transactionEnd(t, s, tx, true)
	if e.Visibility.CommitSeq() != frontier+1 || e.Storage.Sequences[ps.Address] != second.Generators[ps.Address] || e.Storage.Sequences[qs.Address] != second.Generators[qs.Address] || e.Storage.Relations[p.Address] != second.Storage[p.Address].Replacement || e.Storage.Relations[q.Address] != second.Storage[q.Address].Replacement {
		t.Fatal("explicit commit did not install combined candidate once")
	}
	sequenceScalar(t, peer, "SELECT nextval('p_id_seq')", 1)
	sequenceScalar(t, peer, "SELECT nextval('q_id_seq')", 44)
	enginetest.LockEmpty(t, e)
}

func TestTruncateRestartFinalVeto(t *testing.T) {
	for _, kind := range []string{"Context", "Authorization", "Roles", "SequenceRevision", "SequenceACL", "GeneratorBinding", "Storage"} {
		t.Run(kind, func(t *testing.T) {
			e, s, p, q, ps, qs := truncateRestartUnitFixture(t)
			tx := transactionBegin(t, s)
			sessionSQL(t, s, "SAVEPOINT preserve")
			oldP, oldQ, oldPS, oldQS := e.Storage.Relations[p.Address], e.Storage.Relations[q.Address], e.Storage.Sequences[ps.Address], e.Storage.Sequences[qs.Address]
			oldPD, oldQD := e.Catalog.State.Get(ps.Address), e.Catalog.State.Get(qs.Address)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			scope, _ := truncateUnitScope(t, e, s, ctx)
			if _, err := scope.Execute(truncateRestartUnitAST("p", "q"), 0, nil); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "Context":
				cancel()
			case "Authorization":
				s.Mu.Lock()
				s.Authorization.Revision++
				s.Mu.Unlock()
			case "Roles":
				peer := sessionStart(t, e, nil)
				sessionSQL(t, peer, "CREATE ROLE restart_changed_authority")
			case "SequenceRevision":
				truncateUnitReplace(t, e, ps, func(d *catalog.Relation) { d.SequencePlanStamp++ })
			case "SequenceACL":
				if err := enginetest.SetObjectACL(e, context.Background(), ps, authorization.ACLValue{Explicit: true}); err != nil {
					t.Fatal(err)
				}
			case "GeneratorBinding":
				stage := e.BeginCatalogStage(e.CatalogRead())
				replacement := *oldPS
				stage.SequenceState().ResetGenerators(1)
				stage.SequenceState().SetGenerator(ps.Address, &replacement)
				truncateUnitPublish(t, e, stage)
			case "Storage":
				base := e.CatalogRead()
				stage := e.BeginCatalogStage(base)
				next := *base.MustRelation(p.Address)
				next.ObjectRevision++
				replacement := &storage.Relation{Versions: oldP.Versions, Ends: oldP.Ends, Generation: oldP.Generation + 1}
				if err := stage.AlterRelation(context.Background(), p, &next, oldP, replacement, len(oldP.Versions), oldP.Generation, oldP.Ends); err != nil {
					t.Fatal(err)
				}
				truncateUnitPublish(t, e, stage)
			}
			err := scope.Finish(nil)
			scope.Op.Done()
			if kind == "Context" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else if err != (catalog.Conflict{}) {
				t.Fatal("missing final veto", err)
			}
			if len(oldP.Versions) != 2 || len(oldQ.Versions) != 1 || oldPS.Last != 2 || oldQS.Last != 44 || oldP.TruncateLineage != nil || oldQ.TruncateLineage != nil || e.Storage.Relations[q.Address] != oldQ || e.Storage.Sequences[qs.Address] != oldQS || e.Catalog.State.Get(qs.Address) != oldQD {
				t.Fatal("veto changed a committed prefix")
			}
			if kind != "Storage" && e.Storage.Relations[p.Address] != oldP {
				t.Fatal("table replaced on veto")
			}
			if kind != "GeneratorBinding" && e.Storage.Sequences[ps.Address] != oldPS {
				t.Fatal("generator replaced on veto")
			}
			if kind != "SequenceRevision" && kind != "SequenceACL" && e.Catalog.State.Get(ps.Address) != oldPD {
				t.Fatal("descriptor replaced on veto")
			}
			sessionSQL(t, s, "ROLLBACK TO preserve")
			if s.Transactions.Catalog != nil {
				t.Fatal("failed statement leaked overlay past savepoint")
			}
			transactionEnd(t, s, tx, false)
			enginetest.LockEmpty(t, e)
		})
	}
	t.Run("RevisionExhaustion", func(t *testing.T) {
		e, s, p, q, ps, qs := truncateRestartUnitFixture(t)
		oldP, oldQ, oldPS, oldQS := e.Storage.Relations[p.Address], e.Storage.Relations[q.Address], e.Storage.Sequences[ps.Address], e.Storage.Sequences[qs.Address]
		oldPD, oldQD := e.Catalog.State.Get(ps.Address), e.Catalog.State.Get(qs.Address)
		// First reset can reserve a fresh revision, but the second cannot.
		// Neither partial candidate nor local source/cache event may escape.
		e.Catalog.InstallRevisionFrontierLocked(ids.DefinitionRevision(^uint64(0)) - 1)
		scope, _ := truncateUnitScope(t, e, s, context.Background())
		_, err := scope.Execute(truncateRestartUnitAST("p", "q"), 0, nil)
		if err == nil || err.Error() != "stuntgres: catalog definition revision exhausted" {
			t.Fatal("revision exhaustion", err)
		}
		if got := scope.Finish(err); got != err {
			t.Fatal(got)
		}
		scope.Op.Done()
		if e.Storage.Relations[p.Address] != oldP || e.Storage.Relations[q.Address] != oldQ || e.Storage.Sequences[ps.Address] != oldPS || e.Storage.Sequences[qs.Address] != oldQS || e.Catalog.State.Get(ps.Address) != oldPD || e.Catalog.State.Get(qs.Address) != oldQD || oldPS.Last != 2 || oldQS.Last != 44 || core.PayloadOf(s).Sequences.Memory(ps.Address).SourceStamp() != 0 || core.PayloadOf(s).Sequences.Memory(qs.Address).SourceStamp() != 0 {
			t.Fatal("partial exhausted reset escaped")
		}
		enginetest.LockEmpty(t, e)
	})
}

func TestTruncateRestartOwnershipFence(t *testing.T) {
	for _, change := range []string{"Incoming", "Removed", "Reassigned"} {
		t.Run(change, func(t *testing.T) {
			e, s, p, q, ps, _ := truncateRestartUnitFixture(t)
			sessionSQL(t, s, "CREATE SEQUENCE incoming START 90")
			incoming := roleTableRef(t, e, "incoming")
			tx := transactionBegin(t, s)
			scope := truncateRestartUnitStage(t, e, s, "p")
			oldStore, oldGenerator := e.Storage.Relations[p.Address], e.Storage.Sequences[ps.Address]
			if change == "Incoming" {
				owned := e.Catalog.State.MustRelation(ps.Address).Sequence.Owned
				truncateUnitReplace(t, e, incoming, func(d *catalog.Relation) { options := *d.Sequence; options.Owned = owned; d.Sequence = &options })
			} else {
				truncateUnitReplace(t, e, ps, func(d *catalog.Relation) {
					options := *d.Sequence
					if change == "Removed" {
						options.Owned = ids.ObjectAddress{}
					} else {
						options.Owned = q.Address
						options.Owned.SubID = 1
					}
					d.Sequence = &options
				})
			}
			if err := scope.Finish(nil); err != (catalog.Conflict{}) {
				t.Fatalf("host ownership did not veto: %v", err)
			}
			scope.Op.Done()
			if e.Storage.Relations[p.Address] != oldStore || e.Storage.Sequences[ps.Address] != oldGenerator || oldGenerator.Last != 2 || len(oldStore.Versions) != 2 {
				t.Fatal("ownership veto published reset prefix")
			}
			transactionEnd(t, s, tx, false)
			enginetest.LockEmpty(t, e)
		})
	}
}

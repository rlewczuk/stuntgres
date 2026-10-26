package core_test

import (
	"stuntgres/internal/engine/core"
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

func TestTruncateRestartSavepoints(t *testing.T) {
	e, s, p, _, ps, _ := truncateRestartUnitFixture(t)
	peer := enginetest.StartSession(t, e, nil)
	original, descriptor := e.Storage.Sequences[ps.Address], e.Catalog.State.Get(ps.Address)
	tx := transactionBegin(t, s)
	sessionSQL(t, s, "INSERT INTO p VALUES(9)")
	sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 3)
	sessionSQL(t, s, "SAVEPOINT before_reset")
	writes, catalog := s.Transactions.Writes[p.Address], s.Transactions.Catalog
	truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
	first := s.Transactions.Catalog.Generators[ps.Address]
	sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 1)
	if first.Last != 1 || original.Last != 3 || e.Storage.Sequences[ps.Address] != original {
		t.Fatal("replacement mutated original counter")
	}
	sessionSQL(t, s, "ROLLBACK TO before_reset")
	if s.Transactions.Catalog != catalog || !storage.SameVersionRoot(s.Transactions.Writes[p.Address], writes) || e.Catalog.State.Get(ps.Address) != descriptor || !currvalIs(core.PayloadOf(s).Sequences.Memory(ps.Address), 1) || core.PayloadOf(s).Sequences.Last() != ps.Address {
		t.Fatal("savepoint failed binding/root/memory separation")
	}
	lockState := e.Locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: ps}]
	mode := lock.Mode(0)
	if entry != nil {
		mode = entry.Holders[s.Transactions.Ticket].Mode
	}
	if mode&lock.AccessExclusive != 0 {
		t.Fatal("savepoint retained reset AX")
	}
	sequenceScalar(t, peer, "SELECT nextval('p_id_seq')", 4)
	truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
	second := s.Transactions.Catalog.Generators[ps.Address]
	sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 1)
	sessionSQL(t, s, "SAVEPOINT after_reset")
	sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 2)
	sessionSQL(t, s, "ROLLBACK TO after_reset")
	if s.Transactions.Catalog.Generators[ps.Address] != second || second.Last != 2 {
		t.Fatal("savepoint copied/undid private generator counter")
	}
	truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
	third := s.Transactions.Catalog.Generators[ps.Address]
	if third == second || third.Last != 1 || third.Called || third.Generation == second.Generation || s.Transactions.Catalog.Originals[ps.Address] != descriptor {
		t.Fatal("repeat lost binding identity/first original")
	}
	sessionSQL(t, s, "ROLLBACK TO after_reset")
	if s.Transactions.Catalog.Generators[ps.Address] != second || second.Last != 2 {
		t.Fatal("savepoint did not restore prior private binding")
	}
	sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 3)
	transactionEnd(t, s, tx, false)
	if e.Storage.Sequences[ps.Address] != original || original.Last != 4 || !currvalIs(core.PayloadOf(s).Sequences.Memory(ps.Address), 3) {
		t.Fatal("outer rollback undid ordinary allocation or local observation")
	}
	enginetest.LockEmpty(t, e)
}

func TestTruncateRestartCache(t *testing.T) {
	t.Run("DiscardWithoutDemand", func(t *testing.T) {
		e, s, _, _, ps, _ := truncateRestartUnitFixture(t)
		sessionSQL(t, s, "ALTER SEQUENCE p_id_seq CACHE 4")
		sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 3)
		original := e.Storage.Sequences[ps.Address]
		if original.Last != 6 || core.PayloadOf(s).Sequences.Memory(ps.Address).CachedTail() != 3 {
			t.Fatal("cache fixture not primed")
		}
		tx := transactionBegin(t, s)
		truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
		if core.PayloadOf(s).Sequences.Memory(ps.Address).CachedTail() != 0 || !currvalIs(core.PayloadOf(s).Sequences.Memory(ps.Address), 3) || core.PayloadOf(s).Sequences.Last() != ps.Address {
			t.Fatal("reset did not discard just unused tail")
		}
		transactionEnd(t, s, tx, false) // No sequence operation after reset.
		if e.Storage.Sequences[ps.Address] != original || original.Last != 6 {
			t.Fatal("rollback changed committed binding/highwater")
		}
		sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 7)
		enginetest.LockEmpty(t, e)
	})
	t.Run("PeerGeneration", func(t *testing.T) {
		e, s, _, _, ps, _ := truncateRestartUnitFixture(t)
		peer := enginetest.StartSession(t, e, nil)
		sessionSQL(t, s, "ALTER SEQUENCE p_id_seq CACHE 4")
		sequenceScalar(t, peer, "SELECT nextval('p_id_seq')", 3)
		original := e.Storage.Sequences[ps.Address]
		tx := transactionBegin(t, s)
		truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
		transactionEnd(t, s, tx, false)
		sequenceScalar(t, peer, "SELECT nextval('p_id_seq')", 4)
		if e.Storage.Sequences[ps.Address] != original || core.PayloadOf(peer).Sequences.Memory(ps.Address).CachedTail() != 2 {
			t.Fatal("rollback invalidated untouched peer tail")
		}
		truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
		if e.Storage.Sequences[ps.Address] == original || !currvalIs(core.PayloadOf(peer).Sequences.Memory(ps.Address), 4) {
			t.Fatal("commit did not replace binding/preserved local observation")
		}
		sequenceScalar(t, peer, "SELECT currval('p_id_seq')", 4)
		sequenceScalar(t, peer, "SELECT nextval('p_id_seq')", 1)
		if core.PayloadOf(peer).Sequences.Memory(ps.Address).Generation() != e.Storage.Sequences[ps.Address].Generation {
			t.Fatal("peer did not adopt committed generation")
		}
		enginetest.LockEmpty(t, e)
	})
	for _, end := range []string{"Rollback", "Savepoint", "DiscardSequences"} {
		t.Run("LocalAnalysis/"+end, func(t *testing.T) {
			e := engine.New()
			s, peer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE TABLE b.t(id INT)", "CREATE SEQUENCE b.s START 10 OWNED BY b.t.id", "CREATE SEQUENCE b.other START 20", "SET search_path TO a,b"} {
				sessionSQL(t, s, q)
			}
			sessionSQL(t, peer, "SET search_path TO a,b")
			query := "SELECT nextval('s'),nextval('other')"
			callerAST, caller := sequencePreparedDescription(t, e, s, query)
			peerAST, other := sequencePreparedDescription(t, e, peer, query)
			ref := caller.Sequences.Constants[0].Ref
			original := e.Catalog.State.MustRelation(ref.Address)
			sessionSQL(t, s, "CREATE SEQUENCE a.s START 100")
			sessionSQL(t, s, "CREATE SEQUENCE a.other START 200")
			tx := transactionBegin(t, s)
			if end == "Savepoint" {
				sessionSQL(t, s, "SAVEPOINT before_reset")
			}
			stmt := truncateRestartUnitAST()
			stmt.Targets = append(stmt.Targets, parser.LockTarget{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "b"}, {Text: "t"}}}})
			truncateUnitExecute(t, e, s, stmt)
			if end == "Savepoint" {
				sessionSQL(t, s, "ROLLBACK TO before_reset")
			} else {
				transactionEnd(t, s, tx, false)
			}
			if end == "DiscardSequences" {
				sessionSQL(t, s, "DISCARD SEQUENCES")
			}
			memory := core.PayloadOf(s).Sequences.Memory(ref.Address)
			if memory == nil || memory.SourceStamp() <= original.SequencePlanStamp || currvalValid(memory) || currvalValue(memory) != 0 || memory.CachedTail() != 0 || core.PayloadOf(s).Sequences.Last() != (ids.ObjectAddress{}) || core.PayloadOf(peer).Sequences.Memory(ref.Address) != nil || e.Catalog.State.Get(ref.Address) != original {
				t.Fatal("local source event minted an observation, touched peer, mutated descriptor, or was undone")
			}
			e.Mu.Lock()
			localStamp, peerStamp := core.DefinitionStamp(original, core.PreparedEnvironment{Sequences: &core.PayloadOf(s).Sequences}), core.DefinitionStamp(original, core.PreparedEnvironment{Sequences: &core.PayloadOf(peer).Sequences})
			e.Mu.Unlock()
			if localStamp != memory.SourceStamp() || peerStamp != original.SequencePlanStamp {
				t.Fatal("source stamp authority not caller-local")
			}
			for _, test := range []struct {
				s             *session.Session
				ast           parser.Statement
				description   *core.Description
				first, second int64
			}{
				{peer, peerAST, &other, 10, 20},
				{s, callerAST, &caller, 100, 200},
			} {
				result, err := sequencePreparedExecute(e, test.s, test.ast, test.description)
				if err != nil {
					t.Fatal(err)
				}
				got := enginetest.Tuples(result)
				result.Rows.Close()
				if len(got) != 1 || len(got[0]) != 2 || got[0][0] != test.first || got[0][1] != test.second {
					t.Fatal("local/peer whole-group invalidation", got)
				}
			}
			if end == "Savepoint" {
				transactionEnd(t, s, tx, false)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestTruncateRestartClosure(t *testing.T) {
	t.Run("PartitionRootAndLeaf", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"CREATE TABLE r(id INT) PARTITION BY RANGE(id)", "CREATE TABLE low PARTITION OF r FOR VALUES FROM(0) TO(10)", "CREATE TABLE high PARTITION OF r FOR VALUES FROM(10) TO(20)", "CREATE SEQUENCE r_s START 30 OWNED BY r.id", "CREATE SEQUENCE low_s START 40 OWNED BY low.id", "CREATE SEQUENCE high_s START 50 OWNED BY high.id", "INSERT INTO r VALUES(1),(11)"} {
			sessionSQL(t, s, q)
		}
		root, low, high := roleTableRef(t, e, "r"), roleTableRef(t, e, "low"), roleTableRef(t, e, "high")
		rs, ls, hs := roleTableRef(t, e, "r_s"), roleTableRef(t, e, "low_s"), roleTableRef(t, e, "high_s")
		scope := truncateRestartUnitStage(t, e, s, "low")
		if len(scope.Catalog.Generators) != 1 || scope.Catalog.Generators[ls.Address] == nil || scope.Catalog.Generators[rs.Address] != nil || scope.Catalog.Generators[hs.Address] != nil || scope.Catalog.Storage[high.Address].Replacement != nil {
			t.Fatal("direct leaf widened ownership closure")
		}
		if err := scope.Finish(context.Canceled); err != context.Canceled {
			t.Fatal(err)
		}
		scope.Op.Done()
		scope = truncateRestartUnitStage(t, e, s, "r")
		if len(scope.Catalog.Generators) != 3 || len(scope.Catalog.Storage) != 2 || e.Storage.Relations[root.Address] != nil || scope.Catalog.Storage[low.Address].Replacement == nil || scope.Catalog.Storage[high.Address].Replacement == nil {
			t.Fatal("virtual root omitted its sequence/descendant")
		}
		for _, ref := range []ids.ObjectRef{rs, ls, hs} {
			lockState := e.Locks.Inspect()
			entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
			valid := entry != nil && entry.Holders[scope.LockTicket].Mode == lock.AccessExclusive
			if !valid {
				t.Fatal("missing owned AX", ref)
			}
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		scope.Op.Done()
		enginetest.LockEmpty(t, e)
	})
	t.Run("ForeignTemporaryBranch", func(t *testing.T) {
		e, s, _, _ := truncateUnitFixture(t)
		peer := enginetest.StartSession(t, e, nil)
		sessionSQL(t, s, "CREATE TEMP TABLE own_child() INHERITS(p)")
		sessionSQL(t, s, "CREATE TEMP SEQUENCE own_s OWNED BY own_child.id")
		sessionSQL(t, peer, "CREATE TEMP TABLE peer_child() INHERITS(p)")
		sessionSQL(t, peer, "CREATE TEMP SEQUENCE peer_s OWNED BY peer_child.id")
		catalog := e.CatalogRead()
		own := catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: s.TemporaryNamespace, Name: "own_s"})
		foreign := catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: peer.TemporaryNamespace, Name: "peer_s"})
		original := e.Storage.Sequences[foreign]
		scope := truncateRestartUnitStage(t, e, s, "p")
		if scope.Catalog.Generators[own] == nil || scope.Catalog.Generators[foreign] != nil || len(core.ScopeTruncateOrZero(scope).Sequences) != 1 {
			t.Fatal("TEMP ownership not isolated")
		}
		lockState := e.Locks.Inspect(scope.LockTicket)
		foreignRef := e.Catalog.Domain.Ref(catalog.Epoch(), foreign)
		_, journaled := lockState.Journals[scope.LockTicket][lock.Resource{ObjectRef: foreignRef}]
		excluded := lockState.Resources[lock.Resource{ObjectRef: foreignRef}] == nil && !journaled
		if !excluded {
			t.Fatal("foreign TEMP sequence was locked/journaled")
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		scope.Op.Done()
		if e.Storage.Sequences[foreign] != original {
			t.Fatal("foreign TEMP generator replaced")
		}
		enginetest.LockEmpty(t, e)
	})
	t.Run("OwnSubsequentDDL", func(t *testing.T) {
		e, s, _, _, ps, _ := truncateRestartUnitFixture(t)
		tx := transactionBegin(t, s)
		truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
		replacement := s.Transactions.Catalog.Generators[ps.Address]
		sessionSQL(t, s, "ALTER SEQUENCE p_id_seq OWNED BY NONE")
		sessionSQL(t, s, "CREATE SEQUENCE later START 90 OWNED BY p.id")
		transactionEnd(t, s, tx, true)
		if e.Storage.Sequences[ps.Address] != replacement || e.Catalog.State.MustRelation(ps.Address).Sequence.Owned != (ids.ObjectAddress{}) {
			t.Fatal("legitimate own later DDL rejected or reset discarded")
		}
		sequenceScalar(t, s, "SELECT nextval('later')", 90)
		enginetest.LockEmpty(t, e)
	})
}

// currvalValue is the remembered currval value, valid or not.
func currvalValue(m *sequence.Memory) int64 {
	v, _ := m.Current()
	return v
}

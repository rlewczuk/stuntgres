package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"testing"
)

func TestTruncateRestartSavepoints(t *testing.T) {
	e, s, p, _, ps, _ := truncateRestartUnitFixture(t)
	peer := sessionStart(t, e, nil)
	original, descriptor := e.storage.Sequences[ps.Address], e.catalog.state.Get(ps.Address)
	tx := transactionBegin(t, s)
	sessionSQL(t, s, "INSERT INTO p VALUES(9)")
	sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 3)
	sessionSQL(t, s, "SAVEPOINT before_reset")
	writes, catalog := s.Transactions.Writes[p.Address], s.Transactions.Catalog
	truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
	first := s.Transactions.Catalog.Generators[ps.Address]
	sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 1)
	if first.Last != 1 || original.Last != 3 || e.storage.Sequences[ps.Address] != original {
		t.Fatal("replacement mutated original counter")
	}
	sessionSQL(t, s, "ROLLBACK TO before_reset")
	if s.Transactions.Catalog != catalog || !sameVersionRoot(s.Transactions.Writes[p.Address], writes) || e.catalog.state.Get(ps.Address) != descriptor || !currvalIs(payloadOf(s).sequences.Memory(ps.Address), 1) || payloadOf(s).sequences.Last() != ps.Address {
		t.Fatal("savepoint failed binding/root/memory separation")
	}
	lockState := e.locks.Inspect()
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
	if e.storage.Sequences[ps.Address] != original || original.Last != 4 || !currvalIs(payloadOf(s).sequences.Memory(ps.Address), 3) {
		t.Fatal("outer rollback undid ordinary allocation or local observation")
	}
	lockEmpty(t, e)
}

func TestTruncateRestartCache(t *testing.T) {
	t.Run("DiscardWithoutDemand", func(t *testing.T) {
		e, s, _, _, ps, _ := truncateRestartUnitFixture(t)
		sessionSQL(t, s, "ALTER SEQUENCE p_id_seq CACHE 4")
		sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 3)
		original := e.storage.Sequences[ps.Address]
		if original.Last != 6 || payloadOf(s).sequences.Memory(ps.Address).CachedTail() != 3 {
			t.Fatal("cache fixture not primed")
		}
		tx := transactionBegin(t, s)
		truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
		if payloadOf(s).sequences.Memory(ps.Address).CachedTail() != 0 || !currvalIs(payloadOf(s).sequences.Memory(ps.Address), 3) || payloadOf(s).sequences.Last() != ps.Address {
			t.Fatal("reset did not discard just unused tail")
		}
		transactionEnd(t, s, tx, false) // No sequence operation after reset.
		if e.storage.Sequences[ps.Address] != original || original.Last != 6 {
			t.Fatal("rollback changed committed binding/highwater")
		}
		sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 7)
		lockEmpty(t, e)
	})
	t.Run("PeerGeneration", func(t *testing.T) {
		e, s, _, _, ps, _ := truncateRestartUnitFixture(t)
		peer := sessionStart(t, e, nil)
		sessionSQL(t, s, "ALTER SEQUENCE p_id_seq CACHE 4")
		sequenceScalar(t, peer, "SELECT nextval('p_id_seq')", 3)
		original := e.storage.Sequences[ps.Address]
		tx := transactionBegin(t, s)
		truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
		transactionEnd(t, s, tx, false)
		sequenceScalar(t, peer, "SELECT nextval('p_id_seq')", 4)
		if e.storage.Sequences[ps.Address] != original || payloadOf(peer).sequences.Memory(ps.Address).CachedTail() != 2 {
			t.Fatal("rollback invalidated untouched peer tail")
		}
		truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
		if e.storage.Sequences[ps.Address] == original || !currvalIs(payloadOf(peer).sequences.Memory(ps.Address), 4) {
			t.Fatal("commit did not replace binding/preserved local observation")
		}
		sequenceScalar(t, peer, "SELECT currval('p_id_seq')", 4)
		sequenceScalar(t, peer, "SELECT nextval('p_id_seq')", 1)
		if payloadOf(peer).sequences.Memory(ps.Address).Generation() != e.storage.Sequences[ps.Address].Generation {
			t.Fatal("peer did not adopt committed generation")
		}
		lockEmpty(t, e)
	})
	for _, end := range []string{"Rollback", "Savepoint", "DiscardSequences"} {
		t.Run("LocalAnalysis/"+end, func(t *testing.T) {
			e := New()
			s, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE TABLE b.t(id INT)", "CREATE SEQUENCE b.s START 10 OWNED BY b.t.id", "CREATE SEQUENCE b.other START 20", "SET search_path TO a,b"} {
				sessionSQL(t, s, q)
			}
			sessionSQL(t, peer, "SET search_path TO a,b")
			query := "SELECT nextval('s'),nextval('other')"
			callerAST, caller := sequencePreparedDescription(t, e, s, query)
			peerAST, other := sequencePreparedDescription(t, e, peer, query)
			ref := caller.sequences.Constants[0].Ref
			original := e.catalog.state.MustRelation(ref.Address)
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
			memory := payloadOf(s).sequences.Memory(ref.Address)
			if memory == nil || memory.SourceStamp() <= original.SequencePlanStamp || currvalValid(memory) || currvalValue(memory) != 0 || memory.CachedTail() != 0 || payloadOf(s).sequences.Last() != (ids.ObjectAddress{}) || payloadOf(peer).sequences.Memory(ref.Address) != nil || e.catalog.state.Get(ref.Address) != original {
				t.Fatal("local source event minted an observation, touched peer, mutated descriptor, or was undone")
			}
			e.mu.Lock()
			localStamp, peerStamp := preparedDefinitionStamp(original, s), preparedDefinitionStamp(original, peer)
			e.mu.Unlock()
			if localStamp != memory.SourceStamp() || peerStamp != original.SequencePlanStamp {
				t.Fatal("source stamp authority not caller-local")
			}
			for _, test := range []struct {
				s             *session.Session
				ast           parser.Statement
				description   *StatementDescription
				first, second int64
			}{
				{peer, peerAST, &other, 10, 20},
				{s, callerAST, &caller, 100, 200},
			} {
				result, err := sequencePreparedExecute(e, test.s, test.ast, test.description)
				if err != nil {
					t.Fatal(err)
				}
				got := tuples(result)
				result.Rows.Close()
				if len(got) != 1 || len(got[0]) != 2 || got[0][0] != test.first || got[0][1] != test.second {
					t.Fatal("local/peer whole-group invalidation", got)
				}
			}
			if end == "Savepoint" {
				transactionEnd(t, s, tx, false)
			}
			lockEmpty(t, e)
		})
	}
}

func TestTruncateRestartClosure(t *testing.T) {
	t.Run("PartitionRootAndLeaf", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		for _, q := range []string{"CREATE TABLE r(id INT) PARTITION BY RANGE(id)", "CREATE TABLE low PARTITION OF r FOR VALUES FROM(0) TO(10)", "CREATE TABLE high PARTITION OF r FOR VALUES FROM(10) TO(20)", "CREATE SEQUENCE r_s START 30 OWNED BY r.id", "CREATE SEQUENCE low_s START 40 OWNED BY low.id", "CREATE SEQUENCE high_s START 50 OWNED BY high.id", "INSERT INTO r VALUES(1),(11)"} {
			sessionSQL(t, s, q)
		}
		root, low, high := roleTableRef(t, e, "r"), roleTableRef(t, e, "low"), roleTableRef(t, e, "high")
		rs, ls, hs := roleTableRef(t, e, "r_s"), roleTableRef(t, e, "low_s"), roleTableRef(t, e, "high_s")
		scope := truncateRestartUnitStage(t, e, s, "low")
		if len(scope.catalog.Generators) != 1 || scope.catalog.Generators[ls.Address] == nil || scope.catalog.Generators[rs.Address] != nil || scope.catalog.Generators[hs.Address] != nil || scope.catalog.Storage[high.Address].Replacement != nil {
			t.Fatal("direct leaf widened ownership closure")
		}
		if err := scope.Finish(context.Canceled); err != context.Canceled {
			t.Fatal(err)
		}
		scope.op.Done()
		scope = truncateRestartUnitStage(t, e, s, "r")
		if len(scope.catalog.Generators) != 3 || len(scope.catalog.Storage) != 2 || e.storage.Relations[root.Address] != nil || scope.catalog.Storage[low.Address].Replacement == nil || scope.catalog.Storage[high.Address].Replacement == nil {
			t.Fatal("virtual root omitted its sequence/descendant")
		}
		for _, ref := range []ids.ObjectRef{rs, ls, hs} {
			lockState := e.locks.Inspect()
			entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
			valid := entry != nil && entry.Holders[scope.ticket].Mode == lock.AccessExclusive
			if !valid {
				t.Fatal("missing owned AX", ref)
			}
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		scope.op.Done()
		lockEmpty(t, e)
	})
	t.Run("ForeignTemporaryBranch", func(t *testing.T) {
		e, s, _, _ := truncateUnitFixture(t)
		peer := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TEMP TABLE own_child() INHERITS(p)")
		sessionSQL(t, s, "CREATE TEMP SEQUENCE own_s OWNED BY own_child.id")
		sessionSQL(t, peer, "CREATE TEMP TABLE peer_child() INHERITS(p)")
		sessionSQL(t, peer, "CREATE TEMP SEQUENCE peer_s OWNED BY peer_child.id")
		catalog := e.CatalogRead()
		own := catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: s.TemporaryNamespace, Name: "own_s"})
		foreign := catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: peer.TemporaryNamespace, Name: "peer_s"})
		original := e.storage.Sequences[foreign]
		scope := truncateRestartUnitStage(t, e, s, "p")
		if scope.catalog.Generators[own] == nil || scope.catalog.Generators[foreign] != nil || len(scope.truncateOrZero().sequences) != 1 {
			t.Fatal("TEMP ownership not isolated")
		}
		lockState := e.locks.Inspect(scope.ticket)
		foreignRef := e.catalog.Domain.Ref(catalog.Epoch(), foreign)
		_, journaled := lockState.Journals[scope.ticket][lock.Resource{ObjectRef: foreignRef}]
		excluded := lockState.Resources[lock.Resource{ObjectRef: foreignRef}] == nil && !journaled
		if !excluded {
			t.Fatal("foreign TEMP sequence was locked/journaled")
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		scope.op.Done()
		if e.storage.Sequences[foreign] != original {
			t.Fatal("foreign TEMP generator replaced")
		}
		lockEmpty(t, e)
	})
	t.Run("OwnSubsequentDDL", func(t *testing.T) {
		e, s, _, _, ps, _ := truncateRestartUnitFixture(t)
		tx := transactionBegin(t, s)
		truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
		replacement := s.Transactions.Catalog.Generators[ps.Address]
		sessionSQL(t, s, "ALTER SEQUENCE p_id_seq OWNED BY NONE")
		sessionSQL(t, s, "CREATE SEQUENCE later START 90 OWNED BY p.id")
		transactionEnd(t, s, tx, true)
		if e.storage.Sequences[ps.Address] != replacement || e.catalog.state.MustRelation(ps.Address).Sequence.Owned != (ids.ObjectAddress{}) {
			t.Fatal("legitimate own later DDL rejected or reset discarded")
		}
		sequenceScalar(t, s, "SELECT nextval('later')", 90)
		lockEmpty(t, e)
	})
}

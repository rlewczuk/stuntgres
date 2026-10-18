package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
)

func TestTruncateRestartSavepoints(t *testing.T) {
	e, s, p, _, ps, _ := truncateRestartUnitFixture(t)
	peer := sessionStart(t, e, nil)
	original, descriptor := e.sequences[ps.Address], e.state.descriptors[ps.Address]
	tx := transactionBegin(t, s)
	sessionSQL(t, s, "INSERT INTO p VALUES(9)")
	sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 3)
	sessionSQL(t, s, "SAVEPOINT before_reset")
	writes, catalog := s.transactions.writes[p.Address], s.transactions.catalog
	truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
	first := s.transactions.catalog.generators[ps.Address]
	sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 1)
	if first.last != 1 || original.last != 3 || e.sequences[ps.Address] != original {
		t.Fatal("replacement mutated original counter")
	}
	sessionSQL(t, s, "ROLLBACK TO before_reset")
	if s.transactions.catalog != catalog || !sameVersionRoot(s.transactions.writes[p.Address], writes) || e.state.descriptors[ps.Address] != descriptor || s.sequences[ps.Address].value != 1 || !s.sequences[ps.Address].valid || s.lastSequence != ps.Address {
		t.Fatal("savepoint failed binding/root/memory separation")
	}
	e.locks.mu.Lock()
	entry := e.locks.entries[lockResource{ObjectRef: ps}]
	mode := relationLockMode(0)
	if entry != nil {
		mode = entry.holders[s.transactions.ticket].mode
	}
	e.locks.mu.Unlock()
	if mode&accessExclusive != 0 {
		t.Fatal("savepoint retained reset AX")
	}
	sequenceScalar(t, peer, "SELECT nextval('p_id_seq')", 4)
	truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
	second := s.transactions.catalog.generators[ps.Address]
	sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 1)
	sessionSQL(t, s, "SAVEPOINT after_reset")
	sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 2)
	sessionSQL(t, s, "ROLLBACK TO after_reset")
	if s.transactions.catalog.generators[ps.Address] != second || second.last != 2 {
		t.Fatal("savepoint copied/undid private generator counter")
	}
	truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
	third := s.transactions.catalog.generators[ps.Address]
	if third == second || third.last != 1 || third.called || third.generation == second.generation || s.transactions.catalog.originals[ps.Address] != descriptor {
		t.Fatal("repeat lost binding identity/first original")
	}
	sessionSQL(t, s, "ROLLBACK TO after_reset")
	if s.transactions.catalog.generators[ps.Address] != second || second.last != 2 {
		t.Fatal("savepoint did not restore prior private binding")
	}
	sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 3)
	transactionEnd(t, s, tx, false)
	if e.sequences[ps.Address] != original || original.last != 4 || s.sequences[ps.Address].value != 3 || !s.sequences[ps.Address].valid {
		t.Fatal("outer rollback undid ordinary allocation or local observation")
	}
	lockEmpty(t, e)
}

func TestTruncateRestartCache(t *testing.T) {
	t.Run("DiscardWithoutDemand", func(t *testing.T) {
		e, s, _, _, ps, _ := truncateRestartUnitFixture(t)
		sessionSQL(t, s, "ALTER SEQUENCE p_id_seq CACHE 4")
		sequenceScalar(t, s, "SELECT nextval('p_id_seq')", 3)
		original := e.sequences[ps.Address]
		if original.last != 6 || s.sequences[ps.Address].remaining != 3 {
			t.Fatal("cache fixture not primed")
		}
		tx := transactionBegin(t, s)
		truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
		if s.sequences[ps.Address].remaining != 0 || s.sequences[ps.Address].value != 3 || !s.sequences[ps.Address].valid || s.lastSequence != ps.Address {
			t.Fatal("reset did not discard just unused tail")
		}
		transactionEnd(t, s, tx, false) // No sequence operation after reset.
		if e.sequences[ps.Address] != original || original.last != 6 {
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
		original := e.sequences[ps.Address]
		tx := transactionBegin(t, s)
		truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
		transactionEnd(t, s, tx, false)
		sequenceScalar(t, peer, "SELECT nextval('p_id_seq')", 4)
		if e.sequences[ps.Address] != original || peer.sequences[ps.Address].remaining != 2 {
			t.Fatal("rollback invalidated untouched peer tail")
		}
		truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
		if e.sequences[ps.Address] == original || peer.sequences[ps.Address].value != 4 {
			t.Fatal("commit did not replace binding/preserved local observation")
		}
		sequenceScalar(t, peer, "SELECT currval('p_id_seq')", 4)
		sequenceScalar(t, peer, "SELECT nextval('p_id_seq')", 1)
		if peer.sequences[ps.Address].generation != e.sequences[ps.Address].generation {
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
			ref := caller.sequences.constants[0].ref
			original := e.state.descriptors[ref.Address].(*relationDescriptor)
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
			memory := s.sequences[ref.Address]
			if memory == nil || memory.analysisStamp <= original.sequencePlanStamp || memory.valid || memory.value != 0 || memory.remaining != 0 || s.lastSequence != (ids.ObjectAddress{}) || peer.sequences[ref.Address] != nil || e.state.descriptors[ref.Address] != original {
				t.Fatal("local source event minted an observation, touched peer, mutated descriptor, or was undone")
			}
			e.mu.Lock()
			localStamp, peerStamp := preparedDefinitionStamp(original, s), preparedDefinitionStamp(original, peer)
			e.mu.Unlock()
			if localStamp != memory.analysisStamp || peerStamp != original.sequencePlanStamp {
				t.Fatal("source stamp authority not caller-local")
			}
			for _, test := range []struct {
				s             *Session
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
		if len(scope.catalog.generators) != 1 || scope.catalog.generators[ls.Address] == nil || scope.catalog.generators[rs.Address] != nil || scope.catalog.generators[hs.Address] != nil || scope.catalog.storage[high.Address].replacement != nil {
			t.Fatal("direct leaf widened ownership closure")
		}
		if err := scope.Finish(context.Canceled); err != context.Canceled {
			t.Fatal(err)
		}
		scope.op.Done()
		scope = truncateRestartUnitStage(t, e, s, "r")
		if len(scope.catalog.generators) != 3 || len(scope.catalog.storage) != 2 || e.storage[root.Address] != nil || scope.catalog.storage[low.Address].replacement == nil || scope.catalog.storage[high.Address].replacement == nil {
			t.Fatal("virtual root omitted its sequence/descendant")
		}
		for _, ref := range []ids.ObjectRef{rs, ls, hs} {
			e.locks.mu.Lock()
			entry := e.locks.entries[lockResource{ObjectRef: ref}]
			valid := entry != nil && entry.holders[scope.ticket].mode == accessExclusive
			e.locks.mu.Unlock()
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
		own := catalog.state.names[ids.NameKey{ClassID: RelationClassID, Namespace: s.temporaryNamespace, Name: "own_s"}]
		foreign := catalog.state.names[ids.NameKey{ClassID: RelationClassID, Namespace: peer.temporaryNamespace, Name: "peer_s"}]
		original := e.sequences[foreign]
		scope := truncateRestartUnitStage(t, e, s, "p")
		if scope.catalog.generators[own] == nil || scope.catalog.generators[foreign] != nil || len(scope.truncateSequences) != 1 {
			t.Fatal("TEMP ownership not isolated")
		}
		e.locks.mu.Lock()
		foreignRef := e.domain.Ref(catalog.Epoch(), foreign)
		_, journaled := scope.ticket.resources[lockResource{ObjectRef: foreignRef}]
		excluded := e.locks.entries[lockResource{ObjectRef: foreignRef}] == nil && !journaled
		e.locks.mu.Unlock()
		if !excluded {
			t.Fatal("foreign TEMP sequence was locked/journaled")
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		scope.op.Done()
		if e.sequences[foreign] != original {
			t.Fatal("foreign TEMP generator replaced")
		}
		lockEmpty(t, e)
	})
	t.Run("OwnSubsequentDDL", func(t *testing.T) {
		e, s, _, _, ps, _ := truncateRestartUnitFixture(t)
		tx := transactionBegin(t, s)
		truncateUnitExecute(t, e, s, truncateRestartUnitAST("p"))
		replacement := s.transactions.catalog.generators[ps.Address]
		sessionSQL(t, s, "ALTER SEQUENCE p_id_seq OWNED BY NONE")
		sessionSQL(t, s, "CREATE SEQUENCE later START 90 OWNED BY p.id")
		transactionEnd(t, s, tx, true)
		if e.sequences[ps.Address] != replacement || e.state.descriptors[ps.Address].(*relationDescriptor).sequence.owned != (ids.ObjectAddress{}) {
			t.Fatal("legitimate own later DDL rejected or reset discarded")
		}
		sequenceScalar(t, s, "SELECT nextval('later')", 90)
		lockEmpty(t, e)
	})
}

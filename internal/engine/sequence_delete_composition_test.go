package engine

import (
	"context"
	"errors"
	"testing"
)

func TestSequenceDeleteCompositionPublicationFailure(t *testing.T) {
	for _, phase := range []string{"frontier", "cancel", "predecessor"} {
		t.Run(phase,func(t *testing.T) {
			e := New()
			s := sessionStart(t,e,nil)
			sessionSQL(t,s,"CREATE TABLE t(v INT)")
			sessionSQL(t,s,"INSERT INTO t VALUES(1),(1),(2)")
			sessionSQL(t,s,"CREATE SEQUENCE s")
			sequenceScalar(t,s,"SELECT nextval('s')",1)
			table := catalogRef(t,e,e.CatalogRead(),"t")
			sequence := catalogRef(t,e,e.CatalogRead(),"s")
			state, store, generator := e.state,e.storage[table.Address],e.sequences[sequence.Address]
			root, ends, frontier := store.versions,store.ends,e.commitSeq
			ref := transactionBegin(t,s)
			sessionSQL(t,s,"ALTER SEQUENCE s RESTART 100")
			if r := sessionSQL(t,s,"DELETE FROM t WHERE v=1"); r.Count!=2 { t.Fatal(r) }
			sessionSQL(t,s,"INSERT INTO t VALUES(3)")
			op := transactionOperation(t,s)
			if phase=="frontier" { e.commitSeq=CommitSeq(^uint64(0)); frontier=e.commitSeq }
			if phase=="cancel" {
				ctx,cancel := context.WithCancel(op.Context())
				op.ctx=ctx
				cancel()
			}
			if phase=="predecessor" {
				for id,candidate := range s.transactions.deletes[table.Address].rows {
					candidate.version++
					s.transactions.deletes[table.Address].rows[id]=candidate
					break
				}
			}
			err := e.publishWrites(op,ref,s.transactions.writes,s.transactions.deletes)
			if err==nil || phase=="cancel" && !errors.Is(err,context.Canceled) { t.Fatal(phase,err) }
			if e.state!=state || e.storage[table.Address]!=store || store.ends!=ends || len(store.versions)!=len(root) || &store.versions[0]!=&root[0] || e.sequences[sequence.Address]!=generator || generator.last!=1 || e.commitSeq!=frontier {
				t.Fatal("failed coupled publication changed catalog, rows, deletion root, generator or frontier")
			}
		})
	}
}

func TestSequenceDeleteCompositionSingleFrontier(t *testing.T) {
	e := New()
	s := sessionStart(t,e,nil)
	sessionSQL(t,s,"CREATE TABLE t(v INT)")
	sessionSQL(t,s,"INSERT INTO t VALUES(1),(1),(2)")
	sessionSQL(t,s,"CREATE SEQUENCE s")
	sequenceScalar(t,s,"SELECT nextval('s')",1)
	table := catalogRef(t,e,e.CatalogRead(),"t")
	frontier := e.commitSeq
	ref := transactionBegin(t,s)
	sessionSQL(t,s,"ALTER SEQUENCE s RESTART 100")
	sessionSQL(t,s,"DELETE FROM t WHERE v=1")
	sessionSQL(t,s,"INSERT INTO t VALUES(3),(5)")
	sessionSQL(t,s,"DELETE FROM t WHERE v=5")
	op := transactionOperation(t,s)
	if _,err := e.Commit(op,ref); err!=nil { t.Fatal(err) }
	store := e.storage[table.Address]
	if e.commitSeq!=frontier+1 || len(store.versions)!=5 || store.ends==nil || len(store.ends.rows)!=3 {
		t.Fatal("mixed publication did not install one frontier",e.commitSeq,frontier,len(store.versions),store.ends)
	}
	for _,version := range store.versions[3:] {
		if version.born!=frontier+1 { t.Fatal("append frontier",version.born) }
	}
	for _,end := range store.ends.rows {
		if end.end!=frontier+1 { t.Fatal("deletion frontier",end.end) }
	}
	peer := sessionStart(t,e,nil)
	transactionRows(t,peer,"SELECT v FROM t ORDER BY v",2,3)
	sequenceScalar(t,peer,"SELECT nextval('s')",100)
}

package engine

import (
	"context"
	"maps"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func domainAlterMessagePromotion(t *testing.T) {
	for _, latePeer := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "peer_veto"}[latePeer], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE SEQUENCE public.calls", "CREATE DOMAIN public.d AS integer", "CREATE TABLE public.stored(v public.d)", "INSERT INTO public.stored VALUES(1)"} {
				sessionSQL(t, s, q)
			}
			message := batchUnitStart(t, s, context.Background())
			defer message.end(context.Canceled)
			if _, err := message.step(statement(t, "INSERT INTO public.stored VALUES(2)")); err != nil {
				t.Fatal(err)
			}
			ref, ticket, start := s.Transactions.Ref, s.Transactions.Ticket, s.Transactions.Start
			if ref == (ids.TxRef{}) || ticket == nil || s.Transactions.State != session.TxImplicit {
				t.Fatal("retained prefix did not acquire actual transaction ownership")
			}
			for _, q := range []string{"ALTER DOMAIN public.d ADD CONSTRAINT counted CHECK(nextval('public.calls')>0) NOT VALID", "ALTER DOMAIN public.d VALIDATE CONSTRAINT counted", "BEGIN", "SAVEPOINT keep_prefix"} {
				if _, err := message.step(statement(t, q)); err != nil {
					t.Fatal(q, err)
				}
			}
			if s.Transactions.Ref != ref || s.Transactions.Ticket != ticket || s.Transactions.Start != start || s.Transactions.State != session.TxActive || len(domainJournalOf(s.Transactions.Catalog.Domain).ValidationFences()) != 1 {
				t.Fatal("promotion lost original ownership/observation")
			}
			observation := domainJournalOf(s.Transactions.Catalog.Domain).ValidationFences()[0]
			clone := s.Transactions.Catalog.Clone()
			journal := domainWritable(clone)
			journal.Validations = append(journal.Validations, observation)
			if len(domainJournalOf(s.Transactions.Catalog.Domain).ValidationFences()) != 1 {
				t.Fatal("clone mutated sibling observation slice")
			}
			_, failure := message.step(statement(t, "SELECT missing"))
			if failure == nil || message.end(failure) != failure {
				t.Fatal("promoted primary/recovery disposition", failure)
			}
			sessionSQL(t, s, "ROLLBACK TO keep_prefix")
			if len(domainJournalOf(s.Transactions.Catalog.Domain).ValidationFences()) != 1 || domainJournalOf(s.Transactions.Catalog.Domain).ValidationFences()[0] != observation {
				t.Fatal("savepoint recovery lost passive observation")
			}
			sessionSQL(t, s, "INSERT INTO public.stored VALUES(3)")
			sequence := catalogRef(t, e, e.CatalogRead(), "calls")
			if e.storage.Sequences[sequence.Address].Last != 3 {
				t.Fatal("recovery replayed historical CHECK truth")
			}
			if latePeer {
				op, done := roleOperation(t, e, "postgres")
				domainUnitTable(t, e, op, "CREATE TABLE public.peer(v public.d)")
				done()
			}
			before, stores, roots := e.catalog.state, maps.Clone(e.storage.Relations), maps.Clone(e.storage.IndexRoots)
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Execute(op, statement(t, "COMMIT"), 0, nil)
			op.Done()
			if latePeer {
				if err == nil || e.catalog.state != before || !maps.Equal(e.storage.Relations, stores) || !maps.Equal(e.storage.IndexRoots, roots) {
					t.Fatal("promoted final fence lost", err)
				}
			} else {
				if err != nil || domainUnitType(t, e.CatalogRead(), "d").DomainChecks[0].NotValid {
					t.Fatal("promoted private validity not installed", err)
				}
				table := catalogRef(t, e, e.CatalogRead(), "stored")
				if len(e.storage.Relations[table.Address].Versions) != 3 {
					t.Fatal("promoted pending rows lost")
				}
			}
			if e.storage.Sequences[sequence.Address].Last != 3 {
				t.Fatal("promoted final owner replayed CHECK truth")
			}
		})
	}
}

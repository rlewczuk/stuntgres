package schemacmds_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

func TestDomainAlterConstraintMessagePublication(t *testing.T) {
	for _, kind := range []string{"success", "private_create", "multiple_validations", "later_own_container", "own_drop", "unrelated", "late_use", "late_derived", "late_container", "root", "length", "generation", "ends", "cancel_before", "cancel_after"} {
		t.Run(kind, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE SEQUENCE public.calls")
			if kind != "private_create" {
				for _, q := range []string{"CREATE DOMAIN public.d AS integer", "CREATE TABLE public.stored(v public.d)", "INSERT INTO public.stored VALUES(1)"} {
					enginetest.SessionSQL(t, s, q)
				}
			}
			entry := e.Catalog.State
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			message := batchUnitStart(t, s, ctx)
			defer message.end(context.Canceled)
			step := func(q string) {
				t.Helper()
				if _, err := message.step(enginetest.Statement(t, q)); err != nil {
					t.Fatal(q, err)
				}
			}
			if kind == "private_create" {
				step("CREATE DOMAIN public.d AS integer")
				step("CREATE TABLE public.stored(v public.d)")
				step("INSERT INTO public.stored VALUES(1)")
			}
			step("INSERT INTO public.stored VALUES(2)")
			step("ALTER DOMAIN public.d ADD CONSTRAINT counted CHECK(nextval('public.calls')>0) NOT VALID")
			step("ALTER DOMAIN public.d VALIDATE CONSTRAINT counted")
			changes := s.Transactions.Catalog
			if len(typedef.DomainJournalOf(changes.Domain).ValidationFences()) != 1 || len(typedef.DomainJournalOf(changes.Clone().Domain).ValidationFences()) != 1 || typedef.DomainJournalOf(changes.Clone().Domain).ValidationFences()[0] != typedef.DomainJournalOf(changes.Domain).ValidationFences()[0] {
				t.Fatal("immutable validation observation lost during clone")
			}
			view := e.PrivateCatalogLocked(changes)
			table := enginetest.CatalogRef(t, e, view, "stored")
			domain := domainUnitType(t, view, "d")
			sequence := enginetest.CatalogRef(t, e, view, "calls")
			if e.Storage.Sequences[sequence.Address].Last != 2 || domain.DomainChecks[0].NotValid || e.Catalog.State != entry {
				t.Fatal("selected truth count or private publication", e.Storage.Sequences[sequence.Address].Last)
			}
			if kind == "private_create" && (changes.Originals[domain.Address()] != nil || changes.Storage[table.Address].Original != nil) {
				t.Fatal("private creation lacks honest committed absence")
			}
			if kind == "later_own_container" {
				step("CREATE TABLE public.local_container(v public.d[])")
			}
			expectedTruth := int64(2)
			if kind == "multiple_validations" {
				first := typedef.DomainJournalOf(changes.Domain).ValidationFences()[0]
				step("ALTER DOMAIN public.d VALIDATE CONSTRAINT counted")
				latest := s.Transactions.Catalog
				if len(typedef.DomainJournalOf(latest.Domain).ValidationFences()) != 2 || typedef.DomainJournalOf(latest.Domain).ValidationFences()[0] != first || len(typedef.DomainJournalOf(changes.Domain).ValidationFences()) != 1 {
					t.Fatal("repeated validation replaced earliest or sibling fence")
				}
				expectedTruth = 4
			}
			if kind == "own_drop" {
				step("ALTER DOMAIN public.d DROP CONSTRAINT counted")
			}
			fail := false
			switch kind {
			case "unrelated", "late_use", "late_container", "late_derived":
				op, done := enginetest.RoleOperation(t, e, "postgres")
				switch kind {
				case "unrelated":
					domainUnitTable(t, e, op, "CREATE TABLE public.peer(v integer)")
				case "late_use":
					domainUnitTable(t, e, op, "CREATE TABLE public.peer(v public.d)")
					fail = true
				case "late_container":
					domainUnitTable(t, e, op, "CREATE TABLE public.peer(v public.d[])")
					fail = true
				case "late_derived":
					domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.peer AS public.d"))
					fail = true
				}
				done()
			case "root", "length", "generation", "ends":
				store := *e.Storage.Relations[table.Address]
				switch kind {
				case "length":
					store.Versions = append(append([]storage.RowVersion(nil), store.Versions...), storage.RowVersion{Row: 200, Version: 200})
				case "generation":
					store.Generation++
				case "ends":
					store.Ends = &storage.DeletionRoot{}
				}
				e.Mu.Lock()
				e.Storage.InstallRelationLocked(table.Address, &store)
				e.Mu.Unlock()
				fail = true
			}
			before, stores, roots, frontier := e.Catalog.State, maps.Clone(e.Storage.Relations), maps.Clone(e.Storage.IndexRoots), e.Visibility.CommitSeq()
			var err error
			if kind == "cancel_before" || kind == "cancel_after" {
				barrier := &batchPublicationContext{Context: message.op.Ctx, e: e, s: s, entered: make(chan struct{}), release: make(chan struct{})}
				if kind == "cancel_after" {
					barrier.late = cancel
				}
				message.op.Ctx = barrier
				finished := make(chan error, 1)
				joined := false
				defer func() {
					cancel()
					select {
					case <-barrier.release:
					default:
						close(barrier.release)
					}
					if !joined {
						select {
						case <-finished:
						case <-time.After(5 * time.Second):
							t.Error("message publication worker did not join")
						}
					}
				}()
				go func() { finished <- message.end(nil) }()
				select {
				case <-barrier.entered:
				case err = <-finished:
					joined = true
					t.Fatal("message missed final authority barrier", err)
				case <-time.After(5 * time.Second):
					t.Fatal("message final authority deadline")
				}
				if kind == "cancel_before" {
					cancel()
					fail = true
				}
				close(barrier.release)
				select {
				case err = <-finished:
					joined = true
				case <-time.After(5 * time.Second):
					t.Fatal("message completion deadline")
				}
			} else {
				err = message.end(nil)
			}
			if fail {
				if err == nil || e.Catalog.State != before || !maps.Equal(e.Storage.Relations, stores) || !maps.Equal(e.Storage.IndexRoots, roots) || e.Visibility.CommitSeq() != frontier {
					t.Fatal("message veto installed partial roots", err)
				}
				if kind == "cancel_before" && !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation primary", err)
				}
			} else {
				if err != nil || e.Catalog.State == before {
					t.Fatal("message failed to publish", err)
				}
				current := domainUnitType(t, e.CatalogRead(), "d")
				if kind == "own_drop" {
					if len(current.DomainChecks) != 0 {
						t.Fatal("own later DROP lost")
					}
				} else if len(current.DomainChecks) != 1 || current.DomainChecks[0].NotValid {
					t.Fatal("message lost certified flag")
				}
				if len(e.Storage.Relations[table.Address].Versions) != 2 {
					t.Fatal("message did not publish old plus own rows")
				}
			}
			if e.Storage.Sequences[sequence.Address].Last != expectedTruth {
				t.Fatal("outer publication replayed selected truth")
			}
		})
	}
	t.Run("promotion_recovery", domainAlterMessagePromotion)
	t.Run("pending_index_outputs", domainAlterMessageIndexOutputs)
}

func domainAlterMessageIndexOutputs(t *testing.T) {
	domainAlterOwnedIndexOutputs(t, false)
}

func TestTransactionalTypesPrivateDomainIndexes(t *testing.T) {
	domainAlterOwnedIndexOutputs(t, true)
}

func domainAlterOwnedIndexOutputs(t *testing.T, explicit bool) {
	for _, definition := range []struct{ name, column, index string }{
		{"btree_expression", "integer", "CREATE INDEX checked_idx ON public.stored(((v::public.d)::integer))"},
		{"hash_expression", "integer", "CREATE INDEX checked_idx ON public.stored USING hash(((v::public.d)::integer))"},
		{"predicate", "integer", "CREATE INDEX checked_idx ON public.stored(v) WHERE (v::public.d)::integer>0"},
		{"domain_key", "public.d", "CREATE INDEX checked_idx ON public.stored(v)"},
	} {
		t.Run(definition.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE SEQUENCE public.calls", "CREATE DOMAIN public.d AS integer CONSTRAINT counted CHECK(nextval('public.calls')>0)", "CREATE TABLE public.stored(v " + definition.column + ")", definition.index, "INSERT INTO public.stored VALUES(1)"} {
				enginetest.SessionSQL(t, s, q)
			}
			var step func(string)
			var finish func()
			if explicit {
				ref := enginetest.TransactionBegin(t, s)
				defer func() {
					if s.TransactionState() == session.TxActive {
						enginetest.TransactionEnd(t, s, ref, false)
					}
				}()
				step = func(q string) { enginetest.SessionSQL(t, s, q) }
				finish = func() { enginetest.TransactionEnd(t, s, ref, true) }
			} else {
				message := batchUnitStart(t, s, context.Background())
				defer message.end(context.Canceled)
				step = func(q string) {
					t.Helper()
					if _, err := message.step(enginetest.Statement(t, q)); err != nil {
						t.Fatal(q, err)
					}
				}
				finish = func() {
					if err := message.end(nil); err != nil {
						t.Fatal("owner publication", err)
					}
				}
			}
			step("INSERT INTO public.stored VALUES(2)")
			parent := enginetest.CatalogRef(t, e, e.CatalogRead(), "stored").Address
			idx := enginetest.CatalogRef(t, e, e.CatalogRead(), "checked_idx").Address
			sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "calls").Address
			oldIndex := e.Catalog.State.MustRelation(idx)
			original := s.Transactions.Writes[parent][0]
			output, ok := original.IndexOutputs[idx]
			// Only actual executable conversions need frozen producer outputs;
			// loading a plain domain-valued column is not a fresh conversion.
			executable := definition.name != "domain_key"
			if executable && (!ok || !output.Member || !index.OutputMatches(output, oldIndex)) {
				t.Fatal("SQL producer lost executable key/predicate output")
			}
			if e.Storage.Sequences[sequence].Last != 2 {
				t.Fatal("setup CHECK event count")
			}
			step("ALTER DOMAIN public.d ADD CONSTRAINT never_replay CHECK(FALSE) NOT VALID")
			view := e.PrivateCatalogLocked(s.Transactions.Catalog)
			currentIndex := view.MustRelation(idx)
			pending := s.Transactions.Writes[parent]
			if len(pending) != 1 || pending[0].Row != original.Row || pending[0].Version != original.Version {
				t.Fatal("pending version identity changed")
			}
			if executable {
				current, ok := pending[0].IndexOutputs[idx]
				if !ok || !index.OutputMatches(current, currentIndex) || current.Member != output.Member || current.Key != output.Key || !slices.Equal(current.Keys, output.Keys) {
					t.Fatal("produced index key/membership was lost during pure generation transport")
				}
				if !index.OutputMatches(original.IndexOutputs[idx], oldIndex) {
					t.Fatal("retained producer output mutated")
				}
			}
			if e.Storage.Sequences[sequence].Last != 2 {
				t.Fatal("metadata truth replay")
			}
			finish()
			if e.Storage.Sequences[sequence].Last != 2 {
				t.Fatal("owner publication replayed CHECK truth")
			}
			enginetest.TransactionRows(t, s, "SELECT v::integer AS v FROM public.stored ORDER BY v", 1, 2)
			root := e.Storage.IndexRoots[idx]
			entries := len(root.Entries)
			if root.Hash != nil {
				entries = 0
				for _, bucket := range root.Hash.Buckets {
					entries += len(bucket)
				}
			}
			if entries != 2 {
				t.Fatal("transport lost index membership", entries)
			}
		})
	}
}

func batchUnitStart(t *testing.T, s *session.Session, ctx context.Context) *batchUnitMessage {
	t.Helper()
	op, _, err := s.AcquireCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := core.EngineOf(s).StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	if err = scope.RetainMessage(); err != nil {
		_ = scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	return &batchUnitMessage{scope: scope, op: op}
}

// Context.Err is the canonical cooperative publication seam. With no peer
// mutators, both mutexes are held only at the actual final live-operation check;
// this observes that boundary without depending on intermediate check counts.
type batchPublicationContext struct {
	context.Context
	e                *core.Engine
	s                *session.Session
	entered, release chan struct{}
	armed            atomic.Bool
	late             context.CancelFunc
}

func (c *batchPublicationContext) Err() error {
	if c.e.Mu.TryLock() {
		c.e.Mu.Unlock()
		return c.Context.Err()
	}
	if c.s.Mu.TryLock() {
		c.s.Mu.Unlock()
		return c.Context.Err()
	}
	if c.armed.CompareAndSwap(false, true) {
		close(c.entered)
		<-c.release
		if c.late != nil {
			c.late()
			return nil
		}
	}
	return c.Context.Err()
}

func domainAlterMessagePromotion(t *testing.T) {
	for _, latePeer := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "peer_veto"}[latePeer], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE SEQUENCE public.calls", "CREATE DOMAIN public.d AS integer", "CREATE TABLE public.stored(v public.d)", "INSERT INTO public.stored VALUES(1)"} {
				enginetest.SessionSQL(t, s, q)
			}
			message := batchUnitStart(t, s, context.Background())
			defer message.end(context.Canceled)
			if _, err := message.step(enginetest.Statement(t, "INSERT INTO public.stored VALUES(2)")); err != nil {
				t.Fatal(err)
			}
			ref, ticket, start := s.Transactions.Ref, s.Transactions.Ticket, s.Transactions.Start
			if ref == (ids.TxRef{}) || ticket == nil || s.Transactions.State != session.TxImplicit {
				t.Fatal("retained prefix did not acquire actual transaction ownership")
			}
			for _, q := range []string{"ALTER DOMAIN public.d ADD CONSTRAINT counted CHECK(nextval('public.calls')>0) NOT VALID", "ALTER DOMAIN public.d VALIDATE CONSTRAINT counted", "BEGIN", "SAVEPOINT keep_prefix"} {
				if _, err := message.step(enginetest.Statement(t, q)); err != nil {
					t.Fatal(q, err)
				}
			}
			if s.Transactions.Ref != ref || s.Transactions.Ticket != ticket || s.Transactions.Start != start || s.Transactions.State != session.TxActive || len(typedef.DomainJournalOf(s.Transactions.Catalog.Domain).ValidationFences()) != 1 {
				t.Fatal("promotion lost original ownership/observation")
			}
			observation := typedef.DomainJournalOf(s.Transactions.Catalog.Domain).ValidationFences()[0]
			clone := s.Transactions.Catalog.Clone()
			journal := typedef.DomainWritable(clone)
			journal.Validations = append(journal.Validations, observation)
			if len(typedef.DomainJournalOf(s.Transactions.Catalog.Domain).ValidationFences()) != 1 {
				t.Fatal("clone mutated sibling observation slice")
			}
			_, failure := message.step(enginetest.Statement(t, "SELECT missing"))
			if failure == nil || message.end(failure) != failure {
				t.Fatal("promoted primary/recovery disposition", failure)
			}
			enginetest.SessionSQL(t, s, "ROLLBACK TO keep_prefix")
			if len(typedef.DomainJournalOf(s.Transactions.Catalog.Domain).ValidationFences()) != 1 || typedef.DomainJournalOf(s.Transactions.Catalog.Domain).ValidationFences()[0] != observation {
				t.Fatal("savepoint recovery lost passive observation")
			}
			enginetest.SessionSQL(t, s, "INSERT INTO public.stored VALUES(3)")
			sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "calls")
			if e.Storage.Sequences[sequence.Address].Last != 3 {
				t.Fatal("recovery replayed historical CHECK truth")
			}
			if latePeer {
				op, done := enginetest.RoleOperation(t, e, "postgres")
				domainUnitTable(t, e, op, "CREATE TABLE public.peer(v public.d)")
				done()
			}
			before, stores, roots := e.Catalog.State, maps.Clone(e.Storage.Relations), maps.Clone(e.Storage.IndexRoots)
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Execute(op, enginetest.Statement(t, "COMMIT"), 0, nil)
			op.Done()
			if latePeer {
				if err == nil || e.Catalog.State != before || !maps.Equal(e.Storage.Relations, stores) || !maps.Equal(e.Storage.IndexRoots, roots) {
					t.Fatal("promoted final fence lost", err)
				}
			} else {
				if err != nil || domainUnitType(t, e.CatalogRead(), "d").DomainChecks[0].NotValid {
					t.Fatal("promoted private validity not installed", err)
				}
				table := enginetest.CatalogRef(t, e, e.CatalogRead(), "stored")
				if len(e.Storage.Relations[table.Address].Versions) != 3 {
					t.Fatal("promoted pending rows lost")
				}
			}
			if e.Storage.Sequences[sequence.Address].Last != 3 {
				t.Fatal("promoted final owner replayed CHECK truth")
			}
		})
	}
}

type batchUnitMessage struct {
	scope  *core.StatementScope
	op     *session.Operation
	ended  bool
	result error
}

func (m *batchUnitMessage) step(stmt parser.Statement) (core.Result, error) {
	if m.scope.Finished {
		next, err := m.scope.NextStatement()
		if err != nil {
			return core.Result{}, err
		}
		m.scope = next
	}
	result, err := m.scope.Execute(stmt, 0, nil)
	finish := m.scope.Finish(err)
	if err == nil {
		err = finish
	}
	return result, err
}

func (m *batchUnitMessage) end(err error) error {
	if !m.ended {
		m.result = m.scope.FinishMessage(err)
		m.ended = true
		m.op.Done()
	}
	return m.result
}

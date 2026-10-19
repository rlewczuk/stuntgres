package engine

import (
	"context"
	"errors"
	"maps"
	"slices"
	"stuntgres/internal/engine/storage"
	"testing"
	"time"
)

func TestDomainAlterConstraintMessagePublication(t *testing.T) {
	for _, kind := range []string{"success", "private_create", "multiple_validations", "later_own_container", "own_drop", "unrelated", "late_use", "late_derived", "late_container", "root", "length", "generation", "ends", "cancel_before", "cancel_after"} {
		t.Run(kind, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE SEQUENCE public.calls")
			if kind != "private_create" {
				for _, q := range []string{"CREATE DOMAIN public.d AS integer", "CREATE TABLE public.stored(v public.d)", "INSERT INTO public.stored VALUES(1)"} {
					sessionSQL(t, s, q)
				}
			}
			entry := e.catalog.state
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			message := batchUnitStart(t, s, ctx)
			defer message.end(context.Canceled)
			step := func(q string) {
				t.Helper()
				if _, err := message.step(statement(t, q)); err != nil {
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
			changes := s.transactions.catalog
			if len(changes.domainValidations) != 1 || len(cloneCatalogChanges(changes).domainValidations) != 1 || cloneCatalogChanges(changes).domainValidations[0] != changes.domainValidations[0] {
				t.Fatal("immutable validation observation lost during clone")
			}
			view := e.privateCatalogLocked(changes)
			table := catalogRef(t, e, view, "stored")
			domain := domainUnitType(t, view, "d")
			sequence := catalogRef(t, e, view, "calls")
			if e.storage.Sequences[sequence.Address].Last != 2 || domain.DomainChecks[0].NotValid || e.catalog.state != entry {
				t.Fatal("selected truth count or private publication", e.storage.Sequences[sequence.Address].Last)
			}
			if kind == "private_create" && (changes.originals[domain.Address()] != nil || changes.storage[table.Address].original != nil) {
				t.Fatal("private creation lacks honest committed absence")
			}
			if kind == "later_own_container" {
				step("CREATE TABLE public.local_container(v public.d[])")
			}
			expectedTruth := int64(2)
			if kind == "multiple_validations" {
				first := changes.domainValidations[0]
				step("ALTER DOMAIN public.d VALIDATE CONSTRAINT counted")
				latest := s.transactions.catalog
				if len(latest.domainValidations) != 2 || latest.domainValidations[0] != first || len(changes.domainValidations) != 1 {
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
				op, done := roleOperation(t, e, "postgres")
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
				store := *e.storage.Relations[table.Address]
				switch kind {
				case "length":
					store.Versions = append(append([]storage.RowVersion(nil), store.Versions...), storage.RowVersion{Row: 200, Version: 200})
				case "generation":
					store.Generation++
				case "ends":
					store.Ends = &storage.DeletionRoot{}
				}
				e.storage.Relations[table.Address] = &store
				fail = true
			}
			before, stores, roots, frontier := e.catalog.state, maps.Clone(e.storage.Relations), maps.Clone(e.storage.IndexRoots), e.visibility.CommitSeq()
			var err error
			if kind == "cancel_before" || kind == "cancel_after" {
				barrier := &batchPublicationContext{Context: message.op.ctx, e: e, s: s, entered: make(chan struct{}), release: make(chan struct{})}
				if kind == "cancel_after" {
					barrier.late = cancel
				}
				message.op.ctx = barrier
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
				if err == nil || e.catalog.state != before || !maps.Equal(e.storage.Relations, stores) || !maps.Equal(e.storage.IndexRoots, roots) || e.visibility.CommitSeq() != frontier {
					t.Fatal("message veto installed partial roots", err)
				}
				if kind == "cancel_before" && !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation primary", err)
				}
			} else {
				if err != nil || e.catalog.state == before {
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
				if len(e.storage.Relations[table.Address].Versions) != 2 {
					t.Fatal("message did not publish old plus own rows")
				}
			}
			if e.storage.Sequences[sequence.Address].Last != expectedTruth {
				t.Fatal("outer publication replayed selected truth")
			}
		})
	}
	t.Run("promotion_recovery", domainAlterMessagePromotion)
	t.Run("pending_index_outputs", domainAlterMessageIndexOutputs)
}

func domainAlterMessageIndexOutputs(t *testing.T) {
	for _, definition := range []struct{ name, column, index string }{
		{"btree_expression", "integer", "CREATE INDEX checked_idx ON public.stored(((v::public.d)::integer))"},
		{"hash_expression", "integer", "CREATE INDEX checked_idx ON public.stored USING hash(((v::public.d)::integer))"},
		{"predicate", "integer", "CREATE INDEX checked_idx ON public.stored(v) WHERE (v::public.d)::integer>0"},
		{"domain_key", "public.d", "CREATE INDEX checked_idx ON public.stored(v)"},
	} {
		t.Run(definition.name, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE SEQUENCE public.calls", "CREATE DOMAIN public.d AS integer CONSTRAINT counted CHECK(nextval('public.calls')>0)", "CREATE TABLE public.stored(v " + definition.column + ")", definition.index, "INSERT INTO public.stored VALUES(1)"} {
				sessionSQL(t, s, q)
			}
			message := batchUnitStart(t, s, context.Background())
			defer message.end(context.Canceled)
			step := func(q string) {
				t.Helper()
				if _, err := message.step(statement(t, q)); err != nil {
					t.Fatal(q, err)
				}
			}
			step("INSERT INTO public.stored VALUES(2)")
			parent := catalogRef(t, e, e.CatalogRead(), "stored").Address
			index := catalogRef(t, e, e.CatalogRead(), "checked_idx").Address
			sequence := catalogRef(t, e, e.CatalogRead(), "calls").Address
			oldIndex := e.catalog.state.MustRelation(index)
			original := s.transactions.writes[parent][0]
			output, ok := original.IndexOutputs[index]
			// Only actual executable conversions need frozen producer outputs;
			// loading a plain domain-valued column is not a fresh conversion.
			executable := definition.name != "domain_key"
			if executable && (!ok || !output.Member || !indexOutputMatches(output, oldIndex)) {
				t.Fatal("SQL producer lost executable key/predicate output")
			}
			if e.storage.Sequences[sequence].Last != 2 {
				t.Fatal("setup CHECK event count")
			}
			step("ALTER DOMAIN public.d ADD CONSTRAINT never_replay CHECK(FALSE) NOT VALID")
			view := e.privateCatalogLocked(s.transactions.catalog)
			currentIndex := view.MustRelation(index)
			pending := s.transactions.writes[parent]
			if len(pending) != 1 || pending[0].Row != original.Row || pending[0].Version != original.Version {
				t.Fatal("pending version identity changed")
			}
			if executable {
				current, ok := pending[0].IndexOutputs[index]
				if !ok || !indexOutputMatches(current, currentIndex) || current.Member != output.Member || current.Key != output.Key || !slices.Equal(current.Keys, output.Keys) {
					t.Fatal("produced index key/membership was lost during pure generation transport")
				}
				if !indexOutputMatches(original.IndexOutputs[index], oldIndex) {
					t.Fatal("retained producer output mutated")
				}
			}
			if e.storage.Sequences[sequence].Last != 2 {
				t.Fatal("metadata truth replay")
			}
			if err := message.end(nil); err != nil {
				t.Fatal("owner publication", err)
			}
			if e.storage.Sequences[sequence].Last != 2 {
				t.Fatal("owner publication replayed CHECK truth")
			}
			transactionRows(t, s, "SELECT v::integer AS v FROM public.stored ORDER BY v", 1, 2)
			root := e.storage.IndexRoots[index]
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

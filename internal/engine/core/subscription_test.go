package core_test

import (
	"context"
	"errors"
	"slices"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/subscription"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestSubscriptionCatalogAuthority(t *testing.T) {
	e, foreign := engine.New(), engine.New()
	read := e.CatalogRead()
	state := catalog.CloneState(read.State())
	names := []string{"p1", "p2"}
	d := &catalog.Subscription{ObjectAddress: ids.ObjectAddress{ClassID: ids.SubscriptionClassID, ObjectID: 900000}, ObjectRevision: 1, ObjectName: "authority", Database: e.Catalog.DatabaseAddress(), Owner: e.RoleStore.Domain.Principal(10), Streaming: 'p', TwoPhase: 'd', PasswordRequired: true, SynchronousCommit: "off", Origin: "any"}
	d.ReplicationOriginID = 1
	d.SetPublications(names)
	names[0] = "caller mutation"
	state.SetDescriptor(d.Address(), d)
	state.BindName(catalog.DescriptorNameKey(d), d.Address())
	view := read.WithState(state)
	if err := (catalog.SubscriptionProvider{}).Validate(view, d); err != nil {
		t.Fatal(err)
	}
	if d.Namespace() != (ids.ObjectAddress{}) || catalog.DescriptorNameKey(d).Namespace != d.Database {
		t.Fatal("subscription acquired schema identity")
	}
	detached := d.Publications()
	detached[0] = "reader mutation"
	if !slices.Equal(d.Publications(), []string{"p1", "p2"}) {
		t.Fatal("publications borrow mutable backing")
	}
	next := catalog.CloneState(state)
	copy := *d
	copy.ObjectRevision = 2
	copy.SetPublications([]string{"new"})
	stage := e.BeginCatalogStage(view)
	forged := copy
	forged.ReplicationOriginID++
	if err := stage.Replace(&forged); err == nil {
		t.Fatal("same-identity subscription replacement changed its registration")
	}
	e.DiscardCatalogStage(stage)
	next.SetDescriptor(d.Address(), &copy)
	if !slices.Equal(d.Publications(), []string{"p1", "p2"}) {
		t.Fatal("replacement changed old definition")
	}
	oldCount, newCount := 0, 0
	for _, old := range view.Subscriptions() {
		oldCount++
		if old != d {
			t.Fatal("old index changed")
		}
	}
	for _, n := range read.WithState(next).Subscriptions() {
		newCount++
		if n != &copy {
			t.Fatal("new index stale")
		}
	}
	if oldCount != 1 || newCount != 1 {
		t.Fatal("descriptor index", oldCount, newCount)
	}
	for _, mutate := range []func(*catalog.Subscription){
		func(n *catalog.Subscription) { n.ReplicationOriginID = 0 },
		func(n *catalog.Subscription) { n.ReplicationOriginID = 65535 },
		func(n *catalog.Subscription) { n.ReplicationOriginID = 65536 },
		func(n *catalog.Subscription) { n.Owner = foreign.RoleStore.Domain.Principal(10) },
		func(n *catalog.Subscription) {
			n.Database = ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: 999999}
		},
		func(n *catalog.Subscription) { n.ObjectRevision = 0 },
		func(n *catalog.Subscription) { n.ObjectAddress.ObjectID = 0 },
		func(n *catalog.Subscription) { n.Streaming = 'x' },
		func(n *catalog.Subscription) { n.Enabled = true; n.HasSlot = false },
		func(n *catalog.Subscription) { n.SetPublications([]string{"p", "p"}) },
	} {
		n := *d
		mutate(&n)
		candidate := catalog.CloneState(state)
		candidate.SetDescriptor(n.Address(), &n)
		candidate.BindName(catalog.DescriptorNameKey(&n), n.Address())
		if err := (catalog.SubscriptionProvider{}).Validate(read.WithState(candidate), &n); err == nil {
			t.Fatal("invalid subscription authority accepted", n)
		}
	}
	next.RemoveDescriptor(d.Address())
	count := 0
	for range read.WithState(next).Subscriptions() {
		count++
	}
	if count != 0 {
		t.Fatal("subscription deletion left index entry")
	}
	if err := (catalog.SubscriptionProvider{}).Validate(view, d); err != nil {
		t.Fatal("old root invalidated", err)
	}
}
func TestSubscriptionStaging(t *testing.T) {
	for _, tc := range []struct {
		connection    string
		options       []parser.DictionaryOption
		code, message string
	}{
		{"", nil, "0A000", "subscription publisher connections are not available"},
		{"testconn", []parser.DictionaryOption{{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "connect"}, Value: "false"}, Supplied: true}}, "42601", "invalid connection string syntax: missing \"=\" after \"testconn\" in connection info string\n"},
		{"", []parser.DictionaryOption{{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "binary"}, Value: "yes"}, Supplied: true}}, "42601", "binary requires a Boolean value"},
	} {
		t.Run(tc.message, func(t *testing.T) {
			e := engine.New()
			op, done, err := enginetest.Operation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			e.Mu.Lock()
			defer e.Mu.Unlock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				t.Fatal(err)
			}
			stage := e.BeginCatalogStage(auth.Catalog)
			defer e.DiscardCatalogStage(stage)
			stage.AuthorizeLocked(auth, op.Session)
			before := e.Catalog.State
			err = subscription.Stage(core.DDLHost{E: e, Op: op, CatalogStage: stage}, auth, &parser.Subscription{Action: "create", Name: parser.Identifier{Text: "unit"}, Connection: tc.connection, Publications: []parser.Identifier{{Text: "remote"}}, Options: tc.options})
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message {
				t.Fatal(err)
			}
			if e.Catalog.State != before || stage.DescriptorCount() != 0 || len(op.Notices) != 0 {
				t.Fatal("failed subscription stage changed authority")
			}
		})
	}
}

func TestSubscriptionReplacementCatalogFence(t *testing.T) {
	source, target := engine.New(), engine.New()
	enginetest.Run(t, source, "CREATE SUBSCRIPTION stale_import CONNECTION 'password=import_secret' PUBLICATION imported WITH (connect=false,slot_name=NONE,binary=true)")
	enginetest.Run(t, target, "CREATE SUBSCRIPTION live_retained CONNECTION 'password=live_secret' PUBLICATION retained WITH (connect=false,slot_name=NONE)")
	replacement := source.CatalogRead()
	sourceRoles, targetRoles := source.Roles(), target.Roles()
	source.Mu.Lock()
	indexes, err := core.EngineCaptureIndexSnapshot(source, context.Background(), replacement, source.Storage.Relations, source.Storage.IndexRoots, source.Visibility.CommitSeq())
	source.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	target.Mu.Lock()
	oids := target.Catalog.OIDReservationLocked()
	next, _, err := core.EngineRemapPreparedCatalog(target, catalog.CloneState(replacement.State()), sourceRoles, targetRoles, nil)
	if err != nil {
		target.Mu.Unlock()
		t.Fatal(err)
	}
	relations := make(map[ids.ObjectAddress]*storage.Relation)
	prepared, err := core.EnginePrepareForeignIndexStorage(target, context.Background(), next, relations, indexes)
	target.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// Advance real committed authority after preparation, not a synthetic token.
	enginetest.Run(t, target, "ALTER SUBSCRIPTION live_retained SET (streaming=false)")
	before := target.CatalogRead()
	beforeOID, beforeFrontier := target.Catalog.NextOID(), target.Visibility.CommitSeq()
	target.ReplacementMu.Lock()
	target.Mu.Lock()
	err = core.EngineInstallForeignCatalogLocked(target, context.Background(), next, relations, nil, target.Catalog.NextRevision(), prepared, oids)
	target.Mu.Unlock()
	target.ReplacementMu.Unlock()
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("stale prepared replacement installed", err)
	}
	if target.CatalogRead().State() != before.State() || target.Catalog.NextOID() != beforeOID || target.Visibility.CommitSeq() != beforeFrontier {
		t.Fatal("stale replacement changed published authority")
	}
	count := 0
	for _, sub := range target.CatalogRead().Subscriptions() {
		count++
		if sub.Name() != "live_retained" || sub.Connection != "password=live_secret" || sub.Streaming != 'f' || !slices.Equal(sub.Publications(), []string{"retained"}) {
			t.Fatal("stale replacement damaged current subscription", sub)
		}
	}
	if count != 1 {
		t.Fatal("stale replacement subscription count", count)
	}
}

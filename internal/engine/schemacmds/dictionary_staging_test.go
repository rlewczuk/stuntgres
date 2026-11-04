package schemacmds_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/tsearch"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/parser"
)

// A dictionary's first TEMP namespace remains candidate-owned throughout
// detached compilation, including recapture after an unrelated publication.
func TestDictionaryStagingTemporaryAuthority(t *testing.T) {
	for _, recapture := range []bool{false, true} {
		name := "detached"
		if recapture {
			name = "recaptured"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			before := e.CatalogRead()
			journal := op.Session.Transactions.Catalog.Clone()
			stmt := enginetest.Statement(t, "CREATE TEXT SEARCH DICTIONARY pg_temp.dictionary_candidate (TEMPLATE=pg_catalog.simple,accept=false)")
			var stage *core.CatalogStage
			var preparedNamespace catalog.Descriptor
			var err error
			if !recapture {
				stage, err = schemacmds.StageDictionaryDetached(e, op, stmt)
			} else {
				e.Mu.Lock()
				auth, authErr := e.AuthorizationLocked(op)
				if authErr != nil {
					e.Mu.Unlock()
					t.Fatal(authErr)
				}
				stage = e.BeginCatalogStage(auth.Catalog)
				stage.AuthorizeLocked(auth, op.Session)
				preparation, prepareErr := extension.PrepareDictionary(schemacmds.SchemaHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, stmt)
				e.Mu.Unlock()
				defer e.DiscardCatalogStage(stage)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				preparedNamespace = preparation.Namespace
				if err := preparation.Compile(context.Background(), core.DDLHost{E: e, Op: op, CatalogStage: stage}); err != nil {
					t.Fatal(err)
				}
				// Publish a real unrelated descriptor revision while the first
				// TEMP namespace exists only in the dictionary candidate.
				e.Mu.Lock()
				other := e.BeginCatalogStage(e.Catalog.Current())
				public := *before.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
				public.ObjectRevision, err = e.Catalog.RevisionLocked()
				if err == nil {
					err = other.Replace(&public)
				}
				e.Mu.Unlock()
				defer e.DiscardCatalogStage(other)
				if err != nil {
					t.Fatal(err)
				}
				extensionUnitPublish(t, e, op, other)
				e.Mu.Lock()
				stage, err = schemacmds.CompleteDictionaryLocked(e, op, stmt, stage, &preparation)
				e.Mu.Unlock()
			}
			if err != nil || stage == nil {
				t.Fatal("first TEMP dictionary staging", err)
			}
			defer e.DiscardCatalogStage(stage)
			if stage.Base.State() != e.CatalogRead().State() || (!recapture && e.CatalogRead().State() != before.State()) {
				t.Fatal("completion did not retain the current publication predecessor")
			}
			if op.Session.TemporaryNamespace != (ids.ObjectAddress{}) || !reflect.DeepEqual(op.Session.Transactions.Catalog.Clone(), journal) {
				t.Fatal("detached staging published session TEMP authority or journal")
			}
			view := e.StageRead(stage)
			var namespace, toast *catalog.Namespace
			var dictionary *catalog.TSDictionary
			descriptorCount := 0
			for address, descriptor := range stage.Descriptors() {
				descriptorCount++
				if before.Get(address) != nil || view.Get(address) != descriptor {
					t.Fatal("lost candidate-only identity", address)
				}
				switch d := descriptor.(type) {
				case *catalog.Namespace:
					if d.TemporaryToast {
						toast = d
					} else {
						namespace = d
					}
				case *catalog.TSDictionary:
					dictionary = d
				}
			}
			if descriptorCount != 3 || namespace == nil || toast == nil || dictionary == nil {
				t.Fatal("candidate must own exactly one namespace pair and dictionary")
			}
			if preparedNamespace != nil && namespace != preparedNamespace {
				t.Fatal("recapture replaced the prepared namespace identity")
			}
			for _, ns := range []*catalog.Namespace{namespace, toast} {
				if ns.Temporary != op.Session.SessionToken || ns.Owner != op.RoleState().CurrentUser || view.NameAddress(catalog.DescriptorNameKey(ns)) != ns.Address() {
					t.Fatal("candidate namespace lost actual session/owner authority", ns)
				}
			}
			if dictionary.Namespace() != namespace.Address() || dictionary.Owner != op.RoleState().CurrentUser || view.NameAddress(catalog.DescriptorNameKey(dictionary)) != dictionary.Address() {
				t.Fatal("dictionary lost candidate namespace or creator ownership", dictionary)
			}
			if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(view, dictionary); err != nil {
				t.Fatal("candidate provider validation", err)
			}
		})
	}
}

// The script's own newly labeled C initializer is not a committed predecessor.
// Its direct FHET entry is independent of SQL EXECUTE, and its compilation must
// neither publish the installation nor warm a physical query dictionary cache.
func TestDictionaryStagingSepgsqlUnaccent(t *testing.T) {
	for _, change := range []string{"publish", "peer_label_publication", "peer_role_publication", "peer_label", "validated", "discard", "ended", "cancel", "foreign_scope", "copied_helper"} {
		t.Run(change, func(t *testing.T) {
			e := engine.New()
			policy, err := sepgsql.Compile(sepgsql.SimulationConfig{InstallSQL: true, Policy: sepgsql.Policy{
				ID: "staged-unaccent", Version: "1", Contexts: []string{"A", "P", "S", "T", "U"}, InitialContext: "A", UnlabeledContext: "U",
				DefaultSpec: "db_database * P\ndb_schema * P\ndb_procedure * P\n",
				Rules: []sepgsql.Rule{
					{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}},
					{Source: "A", Target: "S", Class: "db_procedure", Deny: []string{"execute"}},
				},
				CreateTransitions:  []sepgsql.CreateTransition{{Source: "A", Parent: "P", Class: "db_procedure", Name: "unaccent_init", Result: "S"}},
				TrustedTransitions: []sepgsql.TrustedTransition{{Source: "A", Procedure: "S", Result: "T"}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			err = e.InstallSepgsqlSimulation(policy, true)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			ctx, cancel := context.WithTimeout(op.Context(), enginetest.Liveness)
			defer cancel()
			op.Ctx = ctx
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(nil)
			creation := &parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}}
			if err := scope.Gate(creation); err != nil {
				t.Fatal(err)
			}
			before := e.CatalogRead()
			e.Mu.Lock()
			stage, err := schemacmds.StageExtensionLocked(e, scope, creation)
			e.Mu.Unlock()
			defer e.DiscardCatalogStage(stage)
			if err != nil {
				t.Fatal(err)
			}
			view := e.StageRead(stage)
			installed := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
			dictionary := view.MustTSDictionary(installed.Installation.Members[5])
			template, ok := view.TSTemplate(dictionary.Template)
			if !ok {
				t.Fatal("missing actual script template")
			}
			helper, ok := view.Procedure(template.Init)
			if !ok {
				t.Fatal("missing actual script initializer")
			}
			key := catalog.SecurityLabelKey{Object: helper.Address(), Provider: "selinux"}
			label := view.State().SecurityLabel(key)
			if label == nil || *label != (catalog.SecurityLabelRecord{Label: "S", PolicyID: "staged-unaccent", PolicyVersion: "1"}) ||
				stage.Base.Get(helper.Address()) != nil || stage.Base.State().SecurityLabel(key) != nil || before.Get(helper.Address()) != nil {
				t.Fatal("candidate default label replaced the original absence predecessor")
			}
			observed := false
			for readKey, original := range stage.SecurityLabelReads() {
				if readKey == key {
					observed = true
					if original != nil {
						t.Fatal("own generated label became its original read")
					}
				}
			}
			if !observed {
				t.Fatal("script default label omitted original absence fence")
			}
			host := core.HostOf(scope).(tsearch.Host[core.AuthorizationView])
			if len(*host.DictionaryMemos()) != 0 {
				t.Fatal("script staging warmed query initialization")
			}
			// Entry must use S from the real candidate, not U from Base; only
			// S has this transition. SQL execute on S is explicitly denied.
			token, err := scope.EnterStagedDictionaryInit(stage, dictionary, template, helper)
			if err != nil {
				t.Fatal("direct script initializer entry", err)
			}
			if !token.Active() || op.Session.Sepgsql.Current() != "T" {
				token.Leave()
				t.Fatal("initializer ignored the actual own default label")
			}
			token.Leave()
			if op.Session.Sepgsql.Current() != "A" {
				t.Fatal("direct initializer failed to unwind")
			}
			if err := scope.InitializeStagedDictionary(stage, dictionary); err != nil {
				t.Fatal("actual candidate compiler rejected its unchanged own labels", err)
			}
			if len(*host.DictionaryMemos()) != 0 || e.CatalogRead().State() != before.State() ||
				e.StageRead(stage).State().SecurityLabel(key) != label {
				t.Fatal("DDL compilation published, warmed, or changed source labels")
			}
			if change == "publish" {
				if _, err := schemacmds.PublishExtensionStage(e, scope, stage); err != nil {
					t.Fatal("genuine script publication", err)
				}
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
				current := e.CatalogRead()
				if current.Get(helper.Address()) != helper || current.State().SecurityLabel(key) != label ||
					before.Get(helper.Address()) != nil || before.State().SecurityLabel(key) != nil ||
					len(*host.DictionaryMemos()) != 0 {
					t.Fatal("publication lost exact source/default label or mutated old read/query state")
				}
				if _, err := scope.EnterStagedDictionaryInit(stage, dictionary, template, helper); err == nil {
					t.Fatal("published source retained detached initializer authority")
				}
				return
			}
			switch change {
			case "peer_label_publication", "peer_role_publication":
				gate := &dependencyValidationGate{Provider: e.Catalog.Providers[ids.ExtensionClassID], engine: e, name: "unaccent", entered: make(chan struct{}), release: make(chan struct{})}
				e.Catalog.Providers[ids.ExtensionClassID] = gate
				var release sync.Once
				unblock := func() { release.Do(func() { close(gate.release) }) }
				finished := make(chan error, 1)
				go func() {
					_, err := schemacmds.PublishExtensionStage(e, scope, stage)
					finished <- scope.Finish(err)
				}()
				joined := false
				defer func() {
					unblock()
					if !joined {
						<-finished
					}
				}()
				select {
				case <-gate.entered:
				case err := <-finished:
					joined = true
					t.Fatal("real postcompile validator did not enter", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if change == "peer_label_publication" {
					enginetest.Run(t, e, "SECURITY LABEL ON SCHEMA public IS 'S'")
				} else {
					enginetest.Run(t, e, "CREATE ROLE dictionary_stage_peer")
				}
				unblock()
				err := <-finished
				joined = true
				if err == nil || e.CatalogRead().Get(helper.Address()) != nil ||
					e.CatalogRead().State().SecurityLabel(key) != nil || len(*host.DictionaryMemos()) != 0 ||
					op.Session.Sepgsql.Current() != "A" || before.Get(helper.Address()) != nil {
					t.Fatal("peer invalidation published the compiled source or query state", err)
				}
				return
			case "peer_label":
				enginetest.Run(t, e, "SECURITY LABEL ON SCHEMA public IS 'S'")
			case "validated":
				if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
					t.Fatal(err)
				}
			case "discard":
				e.DiscardCatalogStage(stage)
			case "ended":
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			case "foreign_scope":
				other, closeOther := enginetest.RoleOperation(t, e, "postgres")
				defer closeOther()
				otherScope, err := e.StartStatement(other)
				if err != nil {
					t.Fatal(err)
				}
				defer otherScope.Finish(nil)
				if _, err := otherScope.EnterStagedDictionaryInit(stage, dictionary, template, helper); err == nil {
					t.Fatal("another genuine scope adopted the detached source")
				}
				return
			case "copied_helper":
				copy := *helper
				if _, err := scope.EnterStagedDictionaryInit(stage, dictionary, template, &copy); err == nil {
					t.Fatal("equal-valued helper copy adopted the actual installation")
				}
				return
			}
			token, err = scope.EnterStagedDictionaryInit(stage, dictionary, template, helper)
			token.Leave()
			if err == nil || change == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("stale or ended initializer source entered", err)
			}
			if len(*host.DictionaryMemos()) != 0 || op.Session.Sepgsql.Current() != "A" ||
				e.CatalogRead().Get(helper.Address()) != nil || before.State().SecurityLabel(key) != nil {
				t.Fatal("rejected initializer leaked source/query/subject state")
			}
		})
	}
}

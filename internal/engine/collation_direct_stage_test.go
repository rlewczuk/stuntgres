package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// The public SQL executor splits collation preparation and completion. Exercise
// the synchronous schema-stage caller independently, with real role authority
// and a first TEMP namespace that belongs only to the unpublished candidate.
func TestCollationDirectStageTemporaryAuthority(t *testing.T) {
	for _, name := range []string{"candidate_success", "database_temp_denied", "foreign_namespace_denied"} {
		t.Run(name, func(t *testing.T) {
			e := NewWithSettings(nil, "direct_stage_db", false)
			admin, closeAdmin := roleOperation(t, e, "postgres")
			defer closeAdmin()
			roleExecute(t, e, admin, "CREATE ROLE direct_collation_creator LOGIN NOSUPERUSER")
			roleExecute(t, e, admin, "REVOKE TEMPORARY ON DATABASE "+e.databaseName+" FROM PUBLIC")
			if name != "database_temp_denied" {
				roleExecute(t, e, admin, "GRANT TEMPORARY ON DATABASE "+e.databaseName+" TO direct_collation_creator")
			}
			creator, closeCreator := roleOperation(t, e, "direct_collation_creator")
			defer closeCreator()
			actor, ok := e.Roles().Lookup("direct_collation_creator")
			if !ok || actor.Superuser {
				t.Fatal("fixture requires a real ordinary creator", actor)
			}
			bootstrap, ok := e.Roles().Lookup("postgres")
			if !ok {
				t.Fatal("fixture lost bootstrap TEMP namespace owner")
			}

			query := `CREATE COLLATION pg_temp.direct_candidate FROM pg_catalog."C"`
			var foreign *catalog.Namespace
			if name == "foreign_namespace_denied" {
				// A different physical session of the same role owns this
				// TEMP capability; role identity must not bypass that boundary.
				peer, closePeer := roleOperation(t, e, "direct_collation_creator")
				defer closePeer()
				roleExecute(t, e, peer, "CREATE TEMP TABLE foreign_namespace_seed(v integer)")
				foreign = e.CatalogRead().MustNamespace(peer.Session.TemporaryNamespace)
				if foreign.Temporary == creator.Session.SessionToken || foreign.Owner != bootstrap.Ref {
					t.Fatal("fixture must be same-role, foreign-session TEMP", foreign)
				}
				query = `CREATE COLLATION ` + foreign.Name() + `.direct_candidate FROM pg_catalog."C"`
			}
			if creator.Session.TemporaryNamespace != (ids.ObjectAddress{}) {
				t.Fatal("creator already has a TEMP namespace")
			}
			before := e.CatalogRead()
			journal := creator.Session.Transactions.Catalog.Clone()
			defer func() {
				if e.CatalogRead().State() != before.State() {
					t.Error("direct staging published committed catalog changes")
				}
				if creator.Session.TemporaryNamespace != (ids.ObjectAddress{}) {
					t.Error("direct staging installed a premature session TEMP pointer")
				}
				if !reflect.DeepEqual(creator.Session.Transactions.Catalog.Clone(), journal) {
					t.Error("direct staging installed a transaction journal")
				}
			}()

			ast := statement(t, query)
			e.mu.Lock()
			oid, revision := e.catalog.NextOID(), e.catalog.NextRevision()
			stage, err := e.stageSchemaLocked(creator, ast, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
			e.mu.Unlock()
			if stage != nil {
				defer e.DiscardCatalogStage(stage)
			}
			if name != "candidate_success" {
				message := `permission denied to create temporary tables in database "` + e.databaseName + `"`
				if foreign != nil {
					message = "permission denied for schema " + foreign.Name()
				}
				domainUnitDiagnostic(t, err, "42501", message)
				if stage != nil || e.catalog.NextOID() != oid || e.catalog.NextRevision() != revision {
					t.Fatal("denied direct stage allocated catalog identity or returned a candidate")
				}
				return
			}
			if err != nil || stage == nil {
				t.Fatal("first ordinary-creator TEMP collation must stage successfully", err)
			}
			candidate := e.stageRead(stage)
			var namespace *catalog.Namespace
			var toast *catalog.Namespace
			var collation *catalog.Collation
			for address, descriptor := range stage.Descriptors() {
				if before.Get(address) != nil || candidate.Get(address) != descriptor {
					t.Fatal("staged descriptor lost its candidate-only identity", address)
				}
				switch d := descriptor.(type) {
				case *catalog.Namespace:
					if d.TemporaryToast {
						toast = d
					} else {
						namespace = d
					}
				case *catalog.Collation:
					collation = d
				}
			}
			if namespace == nil || toast == nil || collation == nil {
				t.Fatal("candidate lacks genuine TEMP namespace pair or collation", namespace, toast, collation)
			}
			for _, ns := range []*catalog.Namespace{namespace, toast} {
				if ns.Temporary != creator.Session.SessionToken || ns.Owner != bootstrap.Ref || candidate.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: ns.Name()}) != ns.Address() {
					t.Fatal("candidate namespace identity/authority mismatch", ns)
				}
			}
			key := ids.NameKey{ClassID: ids.CollationClassID, Namespace: namespace.Address(), Name: "direct_candidate"}
			if collation.Namespace() != namespace.Address() || collation.Owner != actor.Ref || candidate.NameAddress(key) != collation.Address() || before.NameAddress(key) != (ids.ObjectAddress{}) {
				t.Fatal("collation did not retain exact candidate namespace/name/owner identity", collation)
			}
			if collation.Provider != "c" || collation.Encoding != -1 || !collation.Deterministic || collation.Semantics != 950 || collation.Collate == nil || *collation.Collate != "C" || collation.CType == nil || *collation.CType != "C" || collation.Locale != nil || collation.Rules != nil || collation.Version != nil {
				t.Fatal("staged descriptor misrepresents actual C provider", collation)
			}
			capability, err := collation.Usage()
			if err != nil || !capability.Valid() || capability.ID != uint32(collation.Address().ObjectID) {
				t.Fatal("candidate lacks real provider capability", capability, err)
			}
			text, ok := candidate.LookupType(sqltype.Text)
			if !ok {
				t.Fatal("candidate lost text type")
			}
			usage := text.Usage()
			usage.Collation = capability
			if err := stored.ValidateUsageCollation(candidate, usage); err != nil {
				t.Fatal("candidate consumer cannot use its provider", err)
			}
			if stored.ValidateUsageCollation(before, usage) == nil {
				t.Fatal("committed reader admitted unpublished provider identity")
			}
			if order, err := capability.CompareText(context.Background(), "z", "é"); err != nil || order >= 0 {
				t.Fatal("real C provider comparison did not execute", order, err)
			}
		})
	}
}

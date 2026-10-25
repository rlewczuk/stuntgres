package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// Exercise actual session publication, replacement, frontier admission and read
// capture. A cloned-catalog provenance helper alone cannot cover this consumer.
func TestTemporaryViewRetainedSourceFrontier(t *testing.T) {
	e := engine.New()
	owner := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TEMP TABLE retained_base(v INT)",
		"INSERT INTO retained_base VALUES(11)",
		"CREATE VIEW retained_v AS SELECT v FROM retained_base",
		"CREATE VIEW retained_w AS SELECT v FROM retained_v",
		"CREATE TEMP VIEW retained_catalog AS SELECT 11 AS v FROM pg_catalog.pg_class WHERE relname='retained_catalog'",
		"CREATE TEMP VIEW retained_summary AS SELECT 11 AS v FROM pg_catalog.pg_views WHERE viewname='retained_summary'",
	} {
		enginetest.SessionSQL(t, owner, q)
	}
	before := e.CatalogRead()
	baseAddress := before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: owner.TemporaryNamespace, Name: "retained_base"})
	viewAddress := before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: owner.TemporaryNamespace, Name: "retained_w"})
	original := before.MustRelation(viewAddress)
	oldBase := e.Catalog.Domain.Ref(before.Epoch(), baseAddress)
	providers := make(map[ids.ObjectAddress]*catalog.Relation)
	for _, name := range []string{"retained_catalog", "retained_summary"} {
		address := before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: owner.TemporaryNamespace, Name: name})
		consumer := before.MustRelation(address)
		source := stored.ViewOf(consumer).Sources()[0].Ref.Address
		providers[source] = before.MustRelation(source)
	}
	controls := []func() error{
		func() error { return engine.Reset(context.Background(), e) },
		func() error {
			return engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE restored_marker(v INT); INSERT INTO restored_marker VALUES(7);"))
		},
	}
	for i, replace := range controls {
		t.Run([]string{"reset", "restore"}[i], func(t *testing.T) {
			if err := replace(); err != nil {
				t.Fatal(err)
			}
			current := e.CatalogRead()
			if current.Get(viewAddress) != original {
				t.Fatal("replacement copied retained consumer")
			}
			for address, provider := range providers {
				if current.Get(address) != provider {
					t.Fatal("replacement copied certified catalog provider", provider.Name())
				}
				for _, column := range provider.Columns {
					if typ, ok := current.LookupType(column.Usage.Type.ID()); !ok || typ != column.Usage.Type {
						t.Fatal("catalog provider lost unused column generation", provider.Name(), column.Name)
					}
				}
			}
			use := e.Catalog.Domain.Ref(current.Epoch(), viewAddress)
			auth := namespaceCapturedAuthorization(t, owner)
			auth.Recapture(current)
			e.Mu.Lock()
			refs, err := e.QueryTableRefsLocked(auth, auth.State.CurrentUser, []exec.Leaf{{Ref: use, Relation: original}}, nil, owner.SessionToken, plan.RowMark{})
			_, staleErr := e.QueryTableRefsLocked(auth, auth.State.CurrentUser, []exec.Leaf{{Ref: oldBase, Relation: current.MustRelation(baseAddress)}}, nil, ids.SessionToken{}, plan.RowMark{})
			e.Mu.Unlock()
			if err != nil || len(refs) != 1 || refs[0].Address != baseAddress || refs[0].Epoch != current.Epoch() {
				t.Fatal("retained frontier", refs, err)
			}
			if !errors.Is(staleErr, ids.ErrCatalogReplaced) {
				t.Fatal("unscoped old source admitted", staleErr)
			}
			// Capture real metadata with the retained old-epoch definitions.
			// System view edges survive, but pinned system tables do not gain
			// usage rows or pg_depend SELECT-rule edges.
			usageSource := current.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoViewTableUsageOID})
			runtime := engine.EngineCatalogRuntime{Auth: auth, Database: "retained_database"}
			produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoViewTableUsageOID)
			if !ok {
				t.Fatal("missing usage producer")
			}
			usage, err := engine.CaptureCatalogSourceRows(pgcatalog.Input{Context: t.Context(), Catalog: current, Roles: auth.Roles, Owner: current.Owner(), Ref: current.Owner().Domain.Ref(current.Epoch(), usageSource.Address()), Source: usageSource, Runtime: runtime}, produce)
			if err != nil {
				t.Fatal("retained usage capture", err)
			}
			defer usage.Close()
			got := map[[2]string]int{}
			for _, row := range usage.Anonymous {
				got[[2]string{infoUserMappingsUnitText(t, row[2]), infoUserMappingsUnitText(t, row[5])}]++
			}
			want := map[[2]string]int{{"retained_v", "retained_base"}: 1, {"retained_w", "retained_v"}: 1, {"retained_summary", "pg_views"}: 1}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("retained direct usage pairs", got, want)
			}
			edges, err := pgcatalog.CollectDepend(t.Context(), current, runtime)
			if err != nil {
				t.Fatal("retained pg_depend capture", err)
			}
			summaryAddress := current.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: owner.TemporaryNamespace, Name: "retained_summary"})
			summaryRule := stored.ViewOf(current.MustRelation(summaryAddress)).RuleOID()
			catalogAddress := current.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: owner.TemporaryNamespace, Name: "retained_catalog"})
			catalogRule := stored.ViewOf(current.MustRelation(catalogAddress)).RuleOID()
			foundSummary := false
			for _, edge := range edges {
				if edge.Dependent.ClassID != 2618 || edge.Referenced.ClassID != ids.RelationClassID {
					continue
				}
				if edge.Dependent.ObjectID == summaryRule && edge.DepType == 'n' {
					target := edge.Referenced
					target.SubID = 0
					if relation, ok := current.Relation(target); ok && relation.Name() == "pg_views" {
						foundSummary = true
					}
				}
				if edge.Dependent.ObjectID == catalogRule && edge.Referenced.ObjectID == 1259 {
					t.Fatal("pinned pg_class edge", edge)
				}
			}
			if !foundSummary {
				t.Fatal("missing retained pg_views SELECT-rule dependency")
			}
			for _, q := range []string{"SELECT v FROM retained_w", "SELECT v FROM retained_catalog", "SELECT v FROM retained_summary"} {
				r := enginetest.SessionSQL(t, owner, q)
				if got := enginetest.Tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{int64(11)}}) {
					t.Fatal(q, got)
				}
			}
		})
	}
	enginetest.SessionSQL(t, owner, "INSERT INTO retained_base VALUES(12)")
	if got := enginetest.Tuples(enginetest.SessionSQL(t, owner, "SELECT v FROM retained_w ORDER BY v")); !reflect.DeepEqual(got, [][]driver.Value{{int64(11)}, {int64(12)}}) {
		t.Fatal(got)
	}
}

func infoUserMappingsUnitText(t *testing.T, value sqltype.Value) string {
	t.Helper()
	base, err := value.BaseValue()
	if err != nil || base.Type().ID() != sqltype.Name {
		t.Fatal("NAME payload lost", base, err)
	}
	text, err := base.Text()
	if err != nil {
		t.Fatal("NAME text", err)
	}
	return text
}

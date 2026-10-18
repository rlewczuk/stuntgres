package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
)

// Exercise actual session publication, replacement, frontier admission and read
// capture. A cloned-catalog provenance helper alone cannot cover this consumer.
func TestTemporaryViewRetainedSourceFrontier(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	for _, q := range []string{
		"CREATE TEMP TABLE retained_base(v INT)",
		"INSERT INTO retained_base VALUES(11)",
		"CREATE VIEW retained_v AS SELECT v FROM retained_base",
		"CREATE VIEW retained_w AS SELECT v FROM retained_v",
		"CREATE TEMP VIEW retained_catalog AS SELECT 11 AS v FROM pg_catalog.pg_class WHERE relname='retained_catalog'",
		"CREATE TEMP VIEW retained_summary AS SELECT 11 AS v FROM pg_catalog.pg_views WHERE viewname='retained_summary'",
	} {
		sessionSQL(t, owner, q)
	}
	before := e.CatalogRead()
	baseAddress := before.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: owner.temporaryNamespace, Name: "retained_base"}]
	viewAddress := before.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: owner.temporaryNamespace, Name: "retained_w"}]
	original := before.state.descriptors[viewAddress].(*relationDescriptor)
	oldBase := e.catalog.domain.Ref(before.Epoch(), baseAddress)
	providers := make(map[ids.ObjectAddress]*relationDescriptor)
	for _, name := range []string{"retained_catalog", "retained_summary"} {
		address := before.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: owner.temporaryNamespace, Name: name}]
		consumer := before.state.descriptors[address].(*relationDescriptor)
		source := consumer.view.sources[0].ref.Address
		providers[source] = before.state.descriptors[source].(*relationDescriptor)
	}
	controls := []func() error{
		func() error { return e.Reset(context.Background()) },
		func() error {
			return e.Restore(context.Background(), strings.NewReader("CREATE TABLE restored_marker(v INT); INSERT INTO restored_marker VALUES(7);"))
		},
	}
	for i, replace := range controls {
		t.Run([]string{"reset", "restore"}[i], func(t *testing.T) {
			if err := replace(); err != nil {
				t.Fatal(err)
			}
			current := e.CatalogRead()
			if current.state.descriptors[viewAddress] != original {
				t.Fatal("replacement copied retained consumer")
			}
			for address, provider := range providers {
				if current.state.descriptors[address] != provider {
					t.Fatal("replacement copied certified catalog provider", provider.name)
				}
				for _, column := range provider.columns {
					if typ, ok := current.LookupType(column.Usage.Type.ID()); !ok || typ != column.Usage.Type {
						t.Fatal("catalog provider lost unused column generation", provider.name, column.Name)
					}
				}
			}
			use := e.catalog.domain.Ref(current.Epoch(), viewAddress)
			auth := namespaceCapturedAuthorization(t, owner)
			auth.recaptureCatalog(current)
			e.mu.Lock()
			refs, err := e.queryTableRefsLocked(auth, auth.state.CurrentUser, []queryLeaf{{ref: use, relation: original}}, nil, owner.token, queryRowMark{})
			_, staleErr := e.queryTableRefsLocked(auth, auth.state.CurrentUser, []queryLeaf{{ref: oldBase, relation: current.state.descriptors[baseAddress].(*relationDescriptor)}}, nil, ids.SessionToken{}, queryRowMark{})
			e.mu.Unlock()
			if err != nil || len(refs) != 1 || refs[0].Address != baseAddress || refs[0].Epoch != current.Epoch() {
				t.Fatal("retained frontier", refs, err)
			}
			if !errors.Is(staleErr, ErrCatalogReplaced) {
				t.Fatal("unscoped old source admitted", staleErr)
			}
			for _, q := range []string{"SELECT v FROM retained_w", "SELECT v FROM retained_catalog", "SELECT v FROM retained_summary"} {
				r := sessionSQL(t, owner, q)
				if got := tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{int64(11)}}) {
					t.Fatal(q, got)
				}
			}
		})
	}
	sessionSQL(t, owner, "INSERT INTO retained_base VALUES(12)")
	if got := tuples(sessionSQL(t, owner, "SELECT v FROM retained_w ORDER BY v")); !reflect.DeepEqual(got, [][]driver.Value{{int64(11)}, {int64(12)}}) {
		t.Fatal(got)
	}
}

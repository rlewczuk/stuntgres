package writes_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestStatisticsViewsSourceAdmission(t *testing.T) {
	e, _, ref := expressionCollectionFixture(t)
	before := analyzeFacts(e, ref.Address)
	foreign := engine.New()
	for _, oid := range []ids.OID{pgcatalog.StatsOID, pgcatalog.StatsExtOID, pgcatalog.StatsExtExprsOID} {
		statisticExtDataInput(t, e, func(in pgcatalog.Input, _ core.AuthorizationView) {
			in = statisticsViewsInput(in, oid)
			produce, _ := pgcatalog.ProducerFor(oid)
			for _, mutate := range []func(*pgcatalog.Input){
				func(in *pgcatalog.Input) { in.Owner = nil },
				func(in *pgcatalog.Input) { in.Owner = foreign.CatalogRead().Owner() },
				func(in *pgcatalog.Input) { in.Roles = authorization.Read{} },
				func(in *pgcatalog.Input) { in.Roles = foreign.Roles() },
				func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
				func(in *pgcatalog.Input) { in.Ref.Epoch++ },
				func(in *pgcatalog.Input) { in.Ref = foreign.Catalog.Domain.Ref(in.Ref.Epoch, in.Ref.Address) },
				func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
				func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
				func(in *pgcatalog.Input) { in.Runtime = nil },
				func(in *pgcatalog.Input) {
					fields := core.CatalogRuntimeFieldsOf(in.Runtime)
					fields.Engine = nil
					in.Runtime = core.NewCatalogRuntime(fields)
				},
				func(in *pgcatalog.Input) {
					fields := core.CatalogRuntimeFieldsOf(in.Runtime)
					fields.Engine = foreign
					in.Runtime = core.NewCatalogRuntime(fields)
				},
				func(in *pgcatalog.Input) {
					fields := core.CatalogRuntimeFieldsOf(in.Runtime)
					fields.Auth.RoleRecord = nil
					in.Runtime = core.NewCatalogRuntime(fields)
				},
			} {
				bad := in
				mutate(&bad)
				if rows, err := produce(bad); err == nil || rows != nil {
					t.Fatal("invalid source authority published a vector", oid, err)
				}
			}
			// Every ordinary field remains usage-checked even if a visitor
			// attempts to return a same-OID foreign descriptor generation.
			if oid != pgcatalog.StatsOID {
				foreignName, _ := foreign.CatalogRead().LookupType(sqltype.Name)
				bad := in
				bad.Runtime = statisticsViewsForeignRuntime{Runtime: in.Runtime, foreignName: foreignName}
				if rows, err := produce(bad); err == nil || rows != nil {
					t.Fatal("foreign result generation escaped", oid, err)
				}
			}
			statisticsViewsProduce(t, in)
		})
	}
	if analyzeFacts(e, ref.Address) != before {
		t.Fatal("rejected source admission changed backing")
	}
}

type statisticsViewsForeignRuntime struct {
	pgcatalog.Runtime
	foreignName *sqltype.Descriptor
}

func (r statisticsViewsForeignRuntime) VisitExtendedStatisticsView(ctx context.Context, source *catalog.Relation, visit func([]sqltype.Value) error) error {
	return r.Runtime.VisitExtendedStatisticsView(ctx, source, func(row []sqltype.Value) error {
		copy := append([]sqltype.Value(nil), row...)
		copy[0] = sqltype.String(r.foreignName, "same OID, different owner")
		return visit(copy)
	})
}

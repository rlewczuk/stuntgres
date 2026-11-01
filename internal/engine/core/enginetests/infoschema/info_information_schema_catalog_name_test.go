package infoschema_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

const iscnUnitOID ids.OID = 16251

func iscnUnitInput(t *testing.T, f duUnitFixture, state *catalog.State, actor authorization.PrincipalRef, database string) pgcatalog.Input {
	t.Helper()
	view := f.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source, ok := view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: iscnUnitOID})
	if !ok {
		t.Fatal("missing catalog-name source16251")
	}
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth, Database: database})}
}

func iscnUnitRows(t *testing.T, in pgcatalog.Input, rows [][]sqltype.Value, want string) {
	t.Helper()
	if len(rows) != 1 || len(rows[0]) != 1 {
		t.Fatal("expected exactly one one-column row", rows)
	}
	identifier, _ := in.Catalog.LookupType(13248)
	name, _ := in.Catalog.LookupType(sqltype.Name)
	v := rows[0][0]
	if v.IsNull() || v.Type() != identifier || identifier.DomainBase().Type != name || !v.ValidResult(in.Source.Columns[0].Usage) {
		t.Fatal("foreign generation or NULL identifier", v)
	}
	base, err := v.BaseValue()
	if err != nil || base.Type() != name {
		t.Fatal("NAME base", base, err)
	}
	text, err := base.Text()
	if err != nil || text != want {
		t.Fatalf("database=%q want=%q error=%v", text, want, err)
	}
}

func TestInfoInformationSchemaCatalogNameProducer(t *testing.T) {
	f := newDUUnitFixture(t)
	in := iscnUnitInput(t, f, f.state, f.f.owner, "Catalog Name DB")
	produce, ok := pgcatalog.ProducerFor(iscnUnitOID)
	if !ok {
		t.Fatal("missing catalog-name producer")
	}
	source := in.Source
	if source.Name() != "information_schema_catalog_name" || source.Namespace().ObjectID != 13229 || source.RelationKind != catalog.CatalogRelation || !source.VirtualView || pgcatalog.ClassKind(in.Catalog, source) != "v" || len(source.Columns) != 1 || len(source.Attributes) != 1 || source.Attributes[0] != 1 || source.RowType != 13251 {
		t.Fatal("source identity and shape", source)
	}
	column := source.Columns[0]
	identifier, _ := in.Catalog.LookupType(13248)
	if column.Name != "catalog_name" || column.Usage != identifier.Usage() || !column.Nullable || !column.NullabilityKnown || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.DeclaredDimensions != 0 || column.OriginTable != 16251 || column.OriginAttribute != 1 {
		t.Fatal("nullable canonical identifier column", column)
	}
	row := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13251})
	array := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13250})
	if row.OwningRelation != source.Address() || row.Array != 13250 || array.SQLType.Element() != row.SQLType || array.OwningRelation != source.Address() {
		t.Fatal("canonical row/array backlinks", row, array)
	}
	if rights := authorization.ACLRights(source.Security.ACL, source.Owner, []authorization.PrincipalRef{f.f.reader}, authorization.TableGrantPrivileges, false); rights != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", rights)
	}
	for _, actor := range []authorization.PrincipalRef{f.f.op.Session.Authorization.State.CurrentUser, f.f.reader} {
		for _, database := range []string{"Catalog Name DB", "MiXeD 名称", ""} {
			x := iscnUnitInput(t, f, f.state, actor, database)
			captured, err := catalogrt.CaptureRows(x, produce)
			if err != nil {
				t.Fatal(err)
			}
			iscnUnitRows(t, x, captured.Anonymous, database)
			if captured.Ref != x.Ref || captured.View != nil {
				t.Fatal("owned anonymous source", captured)
			}
			captured.Close()
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	x := in
	x.Context = ctx
	if rows, err := produce(x); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture", rows, err)
	}
	for _, mutate := range []func(*pgcatalog.Input){
		func(x *pgcatalog.Input) { x.Owner = &engine.New().Catalog.Owner },
		func(x *pgcatalog.Input) { x.Catalog = engine.New().CatalogRead() },
		func(x *pgcatalog.Input) {
			x.Roles = engine.New().RoleStore.Published.Root.Read(authorization.PrincipalRef{})
		},
		func(x *pgcatalog.Input) { x.Ref.Epoch++ },
		func(x *pgcatalog.Input) { x.Ref.Epoch = 0 },
		func(x *pgcatalog.Input) { x.Ref.Address.SubID = 1 },
		func(x *pgcatalog.Input) { x.Ref = new(ids.Domain).Ref(x.Ref.Epoch, x.Ref.Address) },
		func(x *pgcatalog.Input) { copy := *x.Source; x.Source = &copy },
		func(x *pgcatalog.Input) {
			r := core.CatalogRuntimeFieldsOf(x.Runtime)
			r.Auth.RoleRecord = nil
			x.Runtime = core.NewCatalogRuntime(r)
		},
		func(x *pgcatalog.Input) {
			r := core.CatalogRuntimeFieldsOf(x.Runtime)
			r.Auth.State.CurrentUser = authorization.PrincipalRef{}
			x.Runtime = core.NewCatalogRuntime(r)
		},
	} {
		x := in
		mutate(&x)
		if rows, err := produce(x); rows != nil || err == nil {
			t.Fatal("forged capture accepted", rows, err)
		}
	}
	for _, kind := range []string{"width", "name", "domain", "generation", "name-generation", "typmod", "collation", "dimensions", "database"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			if kind == "name-generation" {
				d := *in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Name)})
				d.SQLType, _ = engine.New().CatalogRead().LookupType(sqltype.Name)
				d.ObjectRevision = d.Revision() + 1
				state.SetDescriptor(d.Address(), &d)
			} else if kind == "database" {
				state.RemoveDescriptor(in.Owner.DatabaseAddress())
			} else {
				d := *source
				d.Columns = append([]catalog.Column(nil), d.Columns...)
				switch kind {
				case "width":
					d.Columns = nil
				case "name":
					d.Columns[0].Name = "wrong"
				case "domain":
					d.Columns[0].Usage.Type, _ = in.Catalog.LookupType(sqltype.Name)
				case "generation":
					typ := d.Columns[0].Usage.Type
					var err error
					d.Columns[0].Usage.Type, err = typ.RebindDomain(typ.DomainBase(), typ.Name(), typ.SQLName())
					if err != nil {
						t.Fatal(err)
					}
				case "typmod":
					d.Columns[0].Usage.Typmod = 7
				case "collation":
					d.Columns[0].Usage.Collation.ID = 0
				case "dimensions":
					d.Columns[0].DeclaredDimensions = 1
				}
				d.ObjectRevision = d.Revision() + 1
				state.SetDescriptor(d.Address(), &d)
			}
			if rows, err := produce(iscnUnitInput(t, f, state, f.f.owner, "")); rows != nil || err == nil {
				t.Fatal("invalid source accepted", rows, err)
			}
		})
	}
}

func TestInfoInformationSchemaCatalogNameCapture(t *testing.T) {
	for _, database := range []string{"captured_database", ""} {
		t.Run(database, func(t *testing.T) {
			f := newDUUnitFixture(t)
			f.f.e.Catalog.PublishLocked(f.state)
			f.f.op.Session.DatabaseName = database
			f.f.actor(f.f.member)
			scope, err := f.f.e.StartStatement(f.f.op)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if scope != nil {
					if err := scope.Finish(nil); err != nil {
						t.Error(err)
					}
				}
			}()
			f.f.e.Mu.Lock()
			err = f.f.e.CaptureCommentSnapshotLocked(scope)
			f.f.e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			old := iscnUnitInput(t, f, f.state, f.f.member, database)
			view := scope.Inquiry.CommentCatalog
			if view != old.Catalog {
				t.Fatal("inquiry must own actual old catalog")
			}
			next := catalog.CloneState(f.state)
			domain := *old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13248})
			domain.SQLType, err = domain.SQLType.RebindDomain(domain.SQLType.DomainBase(), domain.SQLType.Name(), domain.SQLType.SQLName())
			if err != nil {
				t.Fatal(err)
			}
			domain.ObjectRevision = domain.Revision() + 1
			next.SetDescriptor(domain.Address(), &domain)
			source := *old.Source
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			source.Columns[0].Usage = domain.SQLType.Usage()
			source.ObjectRevision = source.Revision() + 1
			next.SetDescriptor(source.Address(), &source)
			f.f.e.Catalog.PublishLocked(next)
			roles := cloneRoleFixture(f.f.e.RoleStore.Published)
			setFixtureEdges(roles, nil)
			f.f.e.RoleStore.PublishLocked(roles)
			f.f.actor(f.f.reader)
			f.f.op.Session.DatabaseName = "later_database"
			f.f.e.Mu.Lock()
			captured, err := core.EngineOpenRelationSourceLocked(f.f.e, scope, old.Ref, old.Source.Owner, plan.RowMark{}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer captured.Close()
			iscnUnitRows(t, old, captured.Anonymous, database)
			retained := captured.Anonymous
			backing := &retained[0][0]
			if captured.Ref != old.Ref || captured.View != nil {
				t.Fatal("old source authority", captured)
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			scope = nil
			fresh, err := f.f.e.StartStatement(f.f.op)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := fresh.Finish(nil); err != nil {
					t.Error(err)
				}
			}()
			f.f.e.Mu.Lock()
			err = f.f.e.CaptureCommentSnapshotLocked(fresh)
			f.f.e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			freshInput := iscnUnitInput(t, f, next, f.f.reader, "later_database")
			if fresh.Inquiry.CommentCatalog != freshInput.Catalog || freshInput.Source == old.Source || freshInput.Source.Columns[0].Usage.Type == old.Source.Columns[0].Usage.Type || freshInput.Roles == old.Roles {
				t.Fatal("fresh inquiry did not replace source/domain/role authority")
			}
			f.f.e.Mu.Lock()
			freshRows, err := core.EngineOpenRelationSourceLocked(f.f.e, fresh, freshInput.Ref, freshInput.Source.Owner, plan.RowMark{}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			iscnUnitRows(t, freshInput, freshRows.Anonymous, "later_database")
			freshRows.Close()
			captured.Close()
			if &retained[0][0] != backing {
				t.Fatal("old backing replaced")
			}
			iscnUnitRows(t, old, retained, database)
		})
	}
}

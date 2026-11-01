package infoschema_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestInfoViewColumnUsageProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoViewColumnUsageOID)
	if !ok {
		t.Fatal("missing producer")
	}
	state := catalog.CloneState(f.e.Catalog.State)
	a := sequence.NewRelation(sequence.RelationSpec{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20300}, Namespace: f.schema, Name: "table_a", Revision: 1, Owner: f.owner, Columns: sequence.Columns(f.e.CatalogRead())})
	a.RelationKind = catalog.TableRelation
	a.TableMethod = 2
	b := *a
	b.ObjectAddress.ObjectID++
	b.ObjectName = "table_b"
	for _, d := range []*catalog.Relation{a, &b} {
		state.SetDescriptor(d.Address(), d)
	}
	ref := func(address ids.ObjectAddress) ids.ObjectRef {
		return f.e.Catalog.Domain.Ref(state.Epoch(), address)
	}
	integer, _ := f.e.CatalogRead().LookupType(sqltype.Int4)
	definition := func(refs ...ids.ObjectRef) *stored.View {
		var expressions []expr.Expr
		var dependencies []catalog.ExpressionDependency
		for _, r := range refs {
			expressions = append(expressions, plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage()}, Origin: r})
			dependencies = append(dependencies, catalog.ExpressionDependency{Ref: r, Revision: 1})
		}
		return stored.NewView(stored.ViewParts{Query: &plan.Query{Root: &plan.ProjectNode{Expressions: expressions}}, Dependencies: dependencies})
	}
	v := *a
	v.ObjectAddress.ObjectID += 10
	v.ObjectName = "view_v"
	v.RelationKind = catalog.ViewRelation
	attr := a.Address()
	attr.SubID = 1
	otherAttr := attr
	otherAttr.SubID = 3
	v.View = definition(ref(a.Address()), ref(attr), ref(otherAttr), ref(attr), ref(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: a.Address().ObjectID, SubID: -6}), ref(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: b.Address().ObjectID, SubID: 1}), ref(v.Address()), ref(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: v.Address().ObjectID, SubID: 1}))
	state.SetDescriptor(v.Address(), &v)
	w := v
	w.ObjectAddress.ObjectID++
	w.ObjectName = "view_w"
	w.View = definition(ref(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: v.Address().ObjectID, SubID: 1}))
	state.SetDescriptor(w.Address(), &w)
	// Whole-table edges have no attribute row; nonordinary defining objects
	// never contribute, even if their retained definition references columns.
	whole := v
	whole.ObjectAddress.ObjectID += 2
	whole.ObjectName = "whole_only"
	whole.View = definition(ref(b.Address()))
	state.SetDescriptor(whole.Address(), &whole)
	materialized := v
	materialized.ObjectAddress.ObjectID += 3
	materialized.ObjectName = "materialized_definition"
	materialized.RelationKind = catalog.MaterializedViewRelation
	state.SetDescriptor(materialized.Address(), &materialized)
	view := f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f, view)
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoViewColumnUsageOID})
	names := []string{"view_catalog", "view_schema", "view_name", "table_catalog", "table_schema", "table_name", "column_name"}
	domain, _ := view.LookupType(13248)
	if pgcatalog.InfoViewColumnUsageOID != 16254 || source.RowType != 13491 || source.Namespace().ObjectID != 13229 || !source.VirtualView || source.RelationKind != catalog.CatalogRelation {
		t.Fatal("native source identity", source)
	}
	rowType, rowOK := view.LookupType(13491)
	array, arrayOK := view.LookupType(13490)
	rowDescriptor, descriptorOK := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13491})
	if !rowOK || !arrayOK || !descriptorOK || array.Element() != rowType || rowType.AssociatedArray() != 13490 || rowDescriptor.OwningRelation != source.Address() {
		t.Fatal("row/array relation linkage")
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", acl)
	}
	for i, column := range source.Columns {
		if column.Name != names[i] || column.Usage.Type != domain || source.Attributes[i] != int32(i+1) {
			t.Fatal("exact column identity", i, column)
		}
	}
	input := func(read catalog.Read, authority core.AuthorizationView) pgcatalog.Input {
		return pgcatalog.Input{Context: t.Context(), Catalog: read, Roles: authority.Roles, Owner: read.Owner(), Ref: read.Owner().Domain.Ref(read.Epoch(), source.Address()), Source: read.MustRelation(source.Address()), Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: authority, Database: "captured_database"})}
	}
	check := func(in pgcatalog.Input, want map[[3]string]int) [][]sqltype.Value {
		t.Helper()
		captured, err := catalogrt.CaptureRows(in, produce)
		if err != nil {
			t.Fatal(err)
		}
		defer captured.Close()
		if captured.Ref != in.Ref || captured.View != nil || captured.Anonymous == nil {
			t.Fatal("anonymous capture identity", captured)
		}
		got := map[[3]string]int{}
		for _, row := range captured.Anonymous {
			if len(row) != 7 {
				t.Fatal("projection width", row)
			}
			for i, value := range row {
				column := in.Source.Columns[i]
				base, baseErr := value.BaseValue()
				if baseErr != nil || base.Type() != column.Usage.Type.BaseType() || base.Type().ID() != sqltype.Name || column.Usage.Typmod != -1 || column.DeclaredDimensions != 0 || column.Usage.Collation.ID != 950 || value.Type() != column.Usage.Type || value.Type().ID() != 13248 || value.IsNull() || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(pgcatalog.InfoViewColumnUsageOID) || column.OriginAttribute != int16(i+1) {
					t.Fatal("identifier domain/provenance", i, value, column)
				}
			}
			for _, i := range []int{0, 3} {
				if infoUserMappingsUnitText(t, row[i]) != "captured_database" {
					t.Fatal("database capture", row)
				}
			}
			for _, i := range []int{1, 4} {
				if infoUserMappingsUnitText(t, row[i]) != "default_schema" {
					t.Fatal("namespace capture", row)
				}
			}
			got[[3]string{infoUserMappingsUnitText(t, row[2]), infoUserMappingsUnitText(t, row[5]), infoUserMappingsUnitText(t, row[6])}]++
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("direct pair bag", got, want)
		}
		return captured.Anonymous
	}
	all := map[[3]string]int{{"view_v", "table_a", "last_value"}: 1, {"view_v", "table_a", "is_called"}: 1, {"view_v", "table_a", "tableoid"}: 1, {"view_v", "table_b", "last_value"}: 1, {"view_w", "view_v", "last_value"}: 1}
	for _, actor := range []authorization.PrincipalRef{auth.State.CurrentUser, f.owner, f.member} {
		auth.State.CurrentUser = actor
		check(input(view, auth), all)
	}
	auth.State.CurrentUser = f.reader
	check(input(view, auth), map[[3]string]int{})
	// An ordinary SELECT grant is not enabled ownership.
	granted := catalog.CloneState(state)
	allowed := *a
	allowed.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}
	granted.SetDescriptor(allowed.Address(), &allowed)
	grantedView := f.e.Catalog.Read(granted)
	grantedAuth := auth
	grantedAuth.Recapture(grantedView)
	check(input(grantedView, grantedAuth), map[[3]string]int{})
	auth.State.CurrentUser = f.owner
	retained := check(input(view, auth), all)
	changed := catalog.CloneState(state)
	renamed := *a
	renamed.ObjectName, renamed.Owner = "renamed_a", f.reader
	changed.SetDescriptor(renamed.Address(), &renamed)
	changedView := f.e.Catalog.Read(changed)
	changedAuth := auth
	changedAuth.Recapture(changedView)
	check(input(changedView, changedAuth), map[[3]string]int{{"view_v", "table_b", "last_value"}: 1, {"view_w", "view_v", "last_value"}: 1})
	changedAuth.State.CurrentUser = f.reader
	check(input(changedView, changedAuth), map[[3]string]int{{"view_v", "renamed_a", "last_value"}: 1, {"view_v", "renamed_a", "is_called"}: 1, {"view_v", "renamed_a", "tableoid"}: 1})
	check(input(view, auth), all)
	retainedBag := map[[3]string]int{}
	for _, row := range retained {
		retainedBag[[3]string{infoUserMappingsUnitText(t, row[2]), infoUserMappingsUnitText(t, row[5]), infoUserMappingsUnitText(t, row[6])}]++
	}
	if !reflect.DeepEqual(retainedBag, all) {
		t.Fatal("retained rows changed", retainedBag, all)
	}
	// Compact an unused middle slot and rename stable attribute3. Dense slot1
	// must still resolve the old attribute3, not its former physical neighbor.
	compacted := catalog.CloneState(state)
	compact := *a
	compact.Columns = []catalog.Column{a.Columns[0], a.Columns[2]}
	compact.Columns[1].Name = "label"
	compact.Attributes = []int32{1, 3}
	compacted.SetDescriptor(compact.Address(), &compact)
	compactView := f.e.Catalog.Read(compacted)
	compactAuth := auth
	compactAuth.Recapture(compactView)
	check(input(compactView, compactAuth), map[[3]string]int{
		{"view_v", "table_a", "last_value"}: 1,
		{"view_v", "table_a", "label"}:      1,
		{"view_v", "table_a", "tableoid"}:   1,
		{"view_v", "table_b", "last_value"}: 1,
		{"view_w", "view_v", "last_value"}:  1,
	})
	if retained[0][0].Type() != domain || source.Columns[0].Usage.Type != domain || view.Get(a.Address()) != a {
		t.Fatal("retained catalog/domain authority changed")
	}
	// Actual descriptor kinds, not fake runtime reference answers.
	for _, tc := range []struct {
		kind    catalog.RelationKind
		include bool
	}{
		{catalog.TableRelation, true}, {catalog.ViewRelation, true}, {catalog.ForeignRelation, true}, {catalog.PartitionedRelation, true},
		{catalog.SequenceRelation, false}, {catalog.MaterializedViewRelation, false}, {catalog.IndexRelation, false}, {catalog.CompositeRelation, false},
	} {
		next := catalog.CloneState(state)
		target := *a
		target.RelationKind = tc.kind
		if tc.kind == catalog.IndexRelation {
			target.SQLIndex = &catalog.IndexDefinition{}
		}
		next.SetDescriptor(target.Address(), &target)
		// Views and indexes have no executable tableoid. Test the kind
		// predicate with ordinary attribute references, not an invalid manifest.
		defining := v
		bAttr := b.Address()
		bAttr.SubID = 1
		defining.View = definition(ref(attr), ref(otherAttr), ref(bAttr))
		next.SetDescriptor(defining.Address(), &defining)
		nextView := f.e.Catalog.Read(next)
		nextAuth := auth
		nextAuth.Recapture(nextView)
		want := map[[3]string]int{{"view_v", "table_b", "last_value"}: 1, {"view_w", "view_v", "last_value"}: 1}
		if tc.include {
			for _, column := range []string{"last_value", "is_called"} {
				want[[3]string{"view_v", "table_a", column}] = 1
			}
		}
		check(input(nextView, nextAuth), want)
	}
	for _, ctx := range []context.Context{&dtpCancelContext{Context: context.Background(), remaining: 1}, &dtpCancelContext{Context: context.Background(), remaining: 15}} {
		in := input(view, auth)
		in.Context = ctx
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation exposed prefix", rows, err)
		}
	}
	// Exercise cancellation inside the real manifest preflight and structural
	// dependency walk independently of descriptor enumeration order.
	for _, remaining := range []int{1, 3, 10, 15} {
		ctx := &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if refs, err := (core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth})).ViewReferences(ctx, view, &v); refs != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("view-reference cancellation exposed prefix", remaining, refs, err)
		}
	}
	role, _ := auth.Roles.Find(f.owner)
	role.Name = "renamed_owner"
	nextRoot, err := auth.RoleRecord.Root.PutDescriptor(role)
	if err != nil {
		t.Fatal(err)
	}
	nextAuth := auth
	nextAuth.RoleRecord = &access.RoleRecord{Root: nextRoot, Revision: auth.RoleRecord.Revision + 1}
	nextAuth.Recapture(view)
	check(input(view, nextAuth), all)
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &engine.New().Catalog.Owner },
		func(in *pgcatalog.Input) { in.Catalog = engine.New().CatalogRead() },
		func(in *pgcatalog.Input) { in.Roles = nextAuth.Roles },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Epoch = 0 },
		func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(view.Epoch(), source.Address()) },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) {
			a := auth
			a.RoleRecord = nil
			in.Runtime = core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: a})
		},
		func(in *pgcatalog.Input) {
			a := auth
			a.State.CurrentUser = f.e.RoleStore.Domain.Principal(99999)
			in.Runtime = core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: a})
		},
	} {
		in := input(view, auth)
		mutate(&in)
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("invalid capture accepted", rows, err)
		}
	}
	for _, malformed := range []string{"width", "domain", "missing", "attribute", "foreign", "zero", "old", "future", "namespace", "view-namespace", "invalid-after-valid", "foreign-after-valid", "future-after-valid", "foreign-source", "future-source", "attribute-source", "missing-source", "zero-source", "old-source", "foreign-pinned", "attribute-pinned", "foreign-pinned-source"} {
		t.Run(malformed, func(t *testing.T) {
			bad := catalog.CloneState(state)
			switch malformed {
			case "width", "domain":
				copy := *source
				copy.Columns = append([]catalog.Column(nil), source.Columns...)
				if malformed == "width" {
					copy.Columns = copy.Columns[:6]
				} else {
					copy.Columns[0].Usage.Type, _ = view.LookupType(sqltype.Name)
				}
				bad.SetDescriptor(copy.Address(), &copy)
			case "namespace", "view-namespace":
				copy := *a
				if malformed == "view-namespace" {
					copy = v
				}
				copy.ObjectNamespace.ObjectID = 999999
				bad.SetDescriptor(copy.Address(), &copy)
			default:
				copy := v
				r := ref(attr)
				switch malformed {
				case "missing":
					r.Address.ObjectID = 999999
				case "attribute", "invalid-after-valid":
					r.Address.SubID = 99
				case "foreign", "foreign-after-valid", "foreign-source":
					r = new(ids.Domain).Ref(view.Epoch(), otherAttr)
				case "future", "future-after-valid", "future-source":
					r.Epoch++
				case "zero", "zero-source":
					r.Epoch = 0
				case "old", "old-source":
					bad.SetEpoch(bad.Epoch() + 1)
				case "missing-source":
					r.Address.ObjectID = 999999
				case "foreign-pinned", "foreign-pinned-source":
					r = new(ids.Domain).Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259})
				case "attribute-pinned":
					r = ref(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259, SubID: 99999})
				}
				copy.View = definition(r)
				if malformed == "invalid-after-valid" || malformed == "foreign-after-valid" || malformed == "future-after-valid" {
					copy.View = definition(ref(attr), r)
				}
				if malformed == "foreign-source" || malformed == "future-source" || malformed == "attribute-source" || malformed == "missing-source" || malformed == "zero-source" || malformed == "old-source" || malformed == "foreign-pinned-source" {
					if malformed != "attribute-source" {
						r.Address.SubID = 0
					}
					copy.View = stored.NewView(stored.ViewParts{Query: &plan.Query{Root: &plan.SourceNode{Ref: r}}, Sources: []stored.ViewLeaf{{Ref: r}}})
				}
				bad.SetDescriptor(copy.Address(), &copy)
			}
			badView := f.e.Catalog.Read(bad)
			badAuth := auth
			badAuth.Recapture(badView)
			rows, err := produce(input(badView, badAuth))
			if rows != nil || err == nil {
				t.Fatal("malformed metadata accepted", rows, err)
			}
			switch malformed {
			case "foreign", "zero", "old", "future", "foreign-after-valid", "future-after-valid", "foreign-source", "future-source", "attribute-source", "missing-source", "zero-source", "old-source", "foreign-pinned", "foreign-pinned-source":
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("malformed authority lost catalog identity error", err)
				}
			}
		})
	}
	emptyView := f.e.Catalog.Read(catalog.CloneState(f.e.Catalog.State))
	emptyAuth := auth
	emptyAuth.Recapture(emptyView)
	check(input(emptyView, emptyAuth), map[[3]string]int{})
	// Validation cannot depend on discovering a user view or emitting a row.
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &engine.New().Catalog.Owner },
		func(in *pgcatalog.Input) { in.Roles = engine.New().Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
	} {
		in := input(emptyView, emptyAuth)
		mutate(&in)
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("empty catalog bypassed capture validation", rows, err)
		}
	}
	for _, populated := range []bool{false, true} {
		for _, fault := range []string{"width", "primitive", "foreign-domain", "missing-domain", "foreign-base", "wrong-base"} {
			bad := catalog.CloneState(emptyView.State())
			if populated {
				bad = catalog.CloneState(state)
			}
			copy := *source
			copy.Columns = append([]catalog.Column(nil), source.Columns...)
			switch fault {
			case "width":
				copy.Columns = copy.Columns[:6]
			case "primitive":
				copy.Columns[0].Usage.Type, _ = view.LookupType(sqltype.Name)
			case "foreign-domain":
				copy.Columns[0].Usage.Type, _ = engine.New().CatalogRead().LookupType(13248)
			case "missing-domain":
				bad.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13248})
			case "foreign-base", "wrong-base":
				base, _ := engine.New().CatalogRead().LookupType(sqltype.Name)
				if fault == "wrong-base" {
					base, _ = view.LookupType(sqltype.Text)
				}
				descriptor := *view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13248})
				var err error
				descriptor.SQLType, err = domain.RebindDomain(base.Usage(), domain.Name(), domain.SQLName())
				if err != nil {
					t.Fatal(err)
				}
				bad.SetDescriptor(descriptor.Address(), &descriptor)
				for i := range copy.Columns {
					copy.Columns[i].Usage.Type = descriptor.SQLType
				}
			}
			bad.SetDescriptor(copy.Address(), &copy)
			badView := f.e.Catalog.Read(bad)
			badAuth := auth
			badAuth.Recapture(badView)
			if rows, err := produce(input(badView, badAuth)); rows != nil || err == nil {
				t.Fatal("invalid source type accepted", populated, fault, rows, err)
			}
		}
	}
	// A valid new domain generation owns new result tags without mutating
	// retained cells, their old source schema or the old type descriptor.
	regenerated := catalog.CloneState(state)
	nextDomain := *view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13248})
	nextDomain.SQLType, err = domain.RebindDomain(domain.DomainBase(), domain.Name(), domain.SQLName())
	if err != nil {
		t.Fatal(err)
	}
	regenerated.SetDescriptor(nextDomain.Address(), &nextDomain)
	nextSource := *source
	nextSource.Columns = append([]catalog.Column(nil), source.Columns...)
	for i := range nextSource.Columns {
		nextSource.Columns[i].Usage.Type = nextDomain.SQLType
	}
	regenerated.SetDescriptor(nextSource.Address(), &nextSource)
	regeneratedView := f.e.Catalog.Read(regenerated)
	regeneratedAuth := auth
	regeneratedAuth.Recapture(regeneratedView)
	fresh := check(input(regeneratedView, regeneratedAuth), all)
	if len(fresh) == 0 || fresh[0][0].Type() != nextDomain.SQLType || nextDomain.SQLType == domain || retained[0][0].Type() != domain || source.Columns[0].Usage.Type != domain {
		t.Fatal("domain generation ownership")
	}
}

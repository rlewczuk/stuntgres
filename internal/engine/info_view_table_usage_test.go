package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestInfoViewTableUsageProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoViewTableUsageOID)
	if !ok {
		t.Fatal("missing producer")
	}
	state := catalog.CloneState(f.e.Catalog.State)
	a := sequence.NewRelation(sequence.RelationSpec{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20300}, Namespace: f.schema, Name: "table_a", Revision: 1, Owner: f.owner, Columns: sequence.Columns(f.e.CatalogRead())})
	a.RelationKind = catalog.TableRelation
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
	v.View = definition(ref(a.Address()), ref(attr), ref(otherAttr), ref(attr), ref(b.Address()), ref(v.Address()))
	state.SetDescriptor(v.Address(), &v)
	w := v
	w.ObjectAddress.ObjectID++
	w.ObjectName = "view_w"
	w.View = definition(ref(v.Address()))
	state.SetDescriptor(w.Address(), &w)
	view := f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f, view)
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoViewTableUsageOID})
	input := func(read catalog.Read, authority AuthorizationView) pgcatalog.Input {
		return pgcatalog.Input{Context: t.Context(), Catalog: read, Roles: authority.Roles, Owner: read.Owner(), Ref: read.Owner().Domain.Ref(read.Epoch(), source.Address()), Source: read.MustRelation(source.Address()), Runtime: EngineCatalogRuntime{Auth: authority, Database: "captured_database"}}
	}
	check := func(in pgcatalog.Input, want map[[2]string]int) [][]sqltype.Value {
		t.Helper()
		captured, err := CaptureCatalogSourceRows(in, produce)
		if err != nil {
			t.Fatal(err)
		}
		defer captured.Close()
		if captured.Ref != in.Ref || captured.View != nil || captured.Anonymous == nil {
			t.Fatal("anonymous capture identity", captured)
		}
		got := map[[2]string]int{}
		for _, row := range captured.Anonymous {
			if len(row) != 6 {
				t.Fatal("projection width", row)
			}
			for i, value := range row {
				column := in.Source.Columns[i]
				if value.Type() != column.Usage.Type || value.Type().ID() != 13248 || value.IsNull() || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(pgcatalog.InfoViewTableUsageOID) || column.OriginAttribute != int16(i+1) {
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
			got[[2]string{infoUserMappingsUnitText(t, row[2]), infoUserMappingsUnitText(t, row[5])}]++
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("direct pair bag", got, want)
		}
		return captured.Anonymous
	}
	all := map[[2]string]int{{"view_v", "table_a"}: 1, {"view_v", "table_b"}: 1, {"view_w", "view_v"}: 1}
	for _, actor := range []authorization.PrincipalRef{auth.State.CurrentUser, f.owner, f.member} {
		auth.State.CurrentUser = actor
		check(input(view, auth), all)
	}
	auth.State.CurrentUser = f.reader
	check(input(view, auth), map[[2]string]int{})
	// An ordinary SELECT grant is not enabled ownership.
	granted := catalog.CloneState(state)
	allowed := *a
	allowed.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}
	granted.SetDescriptor(allowed.Address(), &allowed)
	grantedView := f.e.Catalog.Read(granted)
	grantedAuth := auth
	grantedAuth.Recapture(grantedView)
	check(input(grantedView, grantedAuth), map[[2]string]int{})
	auth.State.CurrentUser = f.owner
	retained := check(input(view, auth), all)
	changed := catalog.CloneState(state)
	renamed := *a
	renamed.ObjectName, renamed.Owner = "renamed_a", f.reader
	changed.SetDescriptor(renamed.Address(), &renamed)
	changedView := f.e.Catalog.Read(changed)
	changedAuth := auth
	changedAuth.Recapture(changedView)
	check(input(changedView, changedAuth), map[[2]string]int{{"view_v", "table_b"}: 1, {"view_w", "view_v"}: 1})
	changedAuth.State.CurrentUser = f.reader
	check(input(changedView, changedAuth), map[[2]string]int{{"view_v", "renamed_a"}: 1})
	check(input(view, auth), all)
	retainedBag := map[[2]string]int{}
	for _, row := range retained {
		retainedBag[[2]string{infoUserMappingsUnitText(t, row[2]), infoUserMappingsUnitText(t, row[5])}]++
	}
	if !reflect.DeepEqual(retainedBag, all) {
		t.Fatal("retained rows changed", retainedBag, all)
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
		nextView := f.e.Catalog.Read(next)
		nextAuth := auth
		nextAuth.Recapture(nextView)
		want := map[[2]string]int{{"view_v", "table_b"}: 1, {"view_w", "view_v"}: 1}
		if tc.include {
			want[[2]string{"view_v", "table_a"}] = 1
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
		if refs, err := (EngineCatalogRuntime{Auth: auth}).ViewReferences(ctx, view, &v); refs != nil || !errors.Is(err, context.Canceled) {
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
		func(in *pgcatalog.Input) { in.Owner = &New().Catalog.Owner },
		func(in *pgcatalog.Input) { in.Catalog = New().CatalogRead() },
		func(in *pgcatalog.Input) { in.Roles = nextAuth.Roles },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Epoch = 0 },
		func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(view.Epoch(), source.Address()) },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) { a := auth; a.RoleRecord = nil; in.Runtime = EngineCatalogRuntime{Auth: a} },
		func(in *pgcatalog.Input) {
			a := auth
			a.State.CurrentUser = f.e.RoleStore.Domain.Principal(99999)
			in.Runtime = EngineCatalogRuntime{Auth: a}
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
					copy.Columns = copy.Columns[:5]
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
	check(input(emptyView, emptyAuth), map[[2]string]int{})
}

func infoViewTableUsageCapture(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	target := sequence.NewRelation(sequence.RelationSpec{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20300}, Namespace: f.schema, Name: "captured_table", Revision: 1, Owner: f.owner, Columns: sequence.Columns(f.e.CatalogRead())})
	target.RelationKind = catalog.TableRelation
	state.SetDescriptor(target.Address(), target)
	v := *target
	v.ObjectAddress.ObjectID++
	v.ObjectName, v.RelationKind = "captured_view", catalog.ViewRelation
	ref := f.e.Catalog.Domain.Ref(state.Epoch(), target.Address())
	v.View = stored.NewView(stored.ViewParts{Query: &plan.Query{Root: &plan.SourceNode{Ref: ref}}, Sources: []stored.ViewLeaf{{Ref: ref}}})
	state.SetDescriptor(v.Address(), &v)
	f.e.Catalog.PublishLocked(state)
	f.op.Session.DatabaseName = "physical_capture_database"
	f.actor(f.owner)
	scope, err := f.e.StartStatement(f.op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	f.e.Mu.Lock()
	err = f.e.CaptureCommentSnapshotLocked(scope)
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	view := scope.Inquiry.CommentCatalog
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoViewTableUsageOID})
	// Change the published root after capture. The metadata source must use
	// the paired statement frontier, not acquire a fresh catalog halfway in.
	next := catalog.CloneState(state)
	renamed := *target
	renamed.ObjectName = "later_name"
	next.SetDescriptor(renamed.Address(), &renamed)
	f.e.Catalog.PublishLocked(next)
	f.e.Mu.Lock()
	captured, err := EngineOpenRelationSourceLocked(f.e, scope, view.Owner().Domain.Ref(view.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
	if err != nil || captured == nil {
		t.Fatal("coherent captured usage source", err)
	}
	defer captured.Close()
	want := []string{"physical_capture_database", "default_schema", "captured_view", "physical_capture_database", "default_schema", "captured_table"}
	if len(captured.Anonymous) != 1 || len(captured.Anonymous[0]) != 6 {
		t.Fatal("captured pair shape", captured.Anonymous)
	}
	for i, text := range want {
		if got := infoUserMappingsUnitText(t, captured.Anonymous[0][i]); got != text {
			t.Fatal("paired statement frontier", i, got, text)
		}
	}
	for i, cell := range captured.Anonymous[0] {
		column := source.Columns[i]
		if cell.Type() != column.Usage.Type || column.OriginTable != uint32(pgcatalog.InfoViewTableUsageOID) || column.OriginAttribute != int16(i+1) {
			t.Fatal("captured domain/origin", i, cell, column)
		}
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	// A fresh physical statement sees the published rename, while the
	// original captured row remains unchanged.
	scope, err = f.e.StartStatement(f.op)
	if err != nil {
		t.Fatal(err)
	}
	f.e.Mu.Lock()
	err = f.e.CaptureCommentSnapshotLocked(scope)
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	freshView := scope.Inquiry.CommentCatalog
	f.e.Mu.Lock()
	fresh, err := EngineOpenRelationSourceLocked(f.e, scope, freshView.Owner().Domain.Ref(freshView.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
	if err != nil || fresh == nil {
		t.Fatal("fresh captured usage source", err)
	}
	defer fresh.Close()
	if len(fresh.Anonymous) != 1 || infoUserMappingsUnitText(t, fresh.Anonymous[0][5]) != "later_name" {
		t.Fatal("fresh statement did not observe rename", fresh.Anonymous)
	}
	if infoUserMappingsUnitText(t, captured.Anonymous[0][5]) != "captured_table" {
		t.Fatal("retained capture mutated")
	}
}

func TestPGDependCatalogRelationPinning(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	for _, tc := range []struct {
		name   string
		pinned bool
	}{{"pg_class", true}, {"pg_views", false}, {"view_table_usage", false}} {
		found := false
		for _, descriptor := range view.Descriptors() {
			relation, ok := descriptor.(*catalog.Relation)
			if !ok || relation.Name() != tc.name {
				continue
			}
			found = true
			if got := stored.DependPinned(view, nil, relation.Address()); got != tc.pinned {
				t.Fatal("catalog pinning", tc.name, got, tc.pinned)
			}
		}
		if !found {
			t.Fatal("missing represented relation", tc.name)
		}
	}
	state := catalog.CloneState(view.State())
	ordinary := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20300}, RelationKind: catalog.TableRelation}
	state.SetDescriptor(ordinary.Address(), ordinary)
	if stored.DependPinned(e.Catalog.Read(state), nil, ordinary.Address()) {
		t.Fatal("user table pinned")
	}
}

package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func transformsInput(t *testing.T, f defaultUnitFixture, state *catalog.State) pgcatalog.Input {
	t.Helper()
	view := f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f, view)
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoTransformsOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}}
}

func transformsCapture(t *testing.T, in pgcatalog.Input) {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoTransformsOID)
	if !ok {
		t.Fatal("missing transforms producer")
	}
	source, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	if source.Ref != in.Ref || source.View != nil || source.Anonymous == nil || len(source.Anonymous) != 0 {
		t.Fatal("truthful empty anonymous capture", source)
	}
	source.Close()
}

func TestInfoTransformsProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	in := transformsInput(t, f, f.e.Catalog.State)
	transformsCapture(t, in)
	if in.Source.Name() != "transforms" || in.Source.Namespace().ObjectID != 13229 || !in.Source.VirtualView || in.Source.Owner.ID() != 10 || len(in.Source.Security.ACL.Entries) != 0 {
		t.Fatal("source owner/view ACL", in.Source)
	}
	for _, spec := range pgcatalog.Specs {
		if spec.OID == pgcatalog.InfoTransformsOID && (!spec.BootstrapOwner || !spec.NoPublic || !spec.Virtual || spec.Indexes) {
			t.Fatal("source installation policy", spec)
		}
	}
	names := []string{"udt_catalog", "udt_schema", "udt_name", "specific_catalog", "specific_schema", "specific_name", "group_name", "transform_type"}
	if len(in.Source.Columns) != len(names) || len(in.Source.Attributes) != len(names) {
		t.Fatal("exact width", in.Source)
	}
	for i, name := range names {
		column := in.Source.Columns[i]
		domainID, baseID := sqltype.ID(13248), sqltype.Name
		if i == 7 {
			domainID, baseID = 13246, sqltype.VarChar
		}
		domain, _ := in.Catalog.LookupType(domainID)
		base, _ := in.Catalog.LookupType(baseID)
		if column.Name != name || column.Usage != domain.Usage() || domain.BaseType() != base || !column.Nullable || !column.NullabilityKnown || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.DeclaredDimensions != 0 || column.OriginTable != 16242 || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			t.Fatal("exact logical metadata", i, column)
		}
	}
	row := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(in.Source.RowType)})
	array := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
	if row.OwningRelation != in.Source.Address() || array.OwningRelation != in.Source.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("row/array authority")
	}
	// Ordinary catalog metadata (including languages and types) cannot fabricate transforms.
	reader := in
	runtime := reader.Runtime.(core.EngineCatalogRuntime)
	runtime.Auth.State.CurrentUser = f.reader
	reader.Runtime = runtime
	transformsCapture(t, reader)
}

func TestInfoTransformsCapture(t *testing.T) {
	f := newDefaultUnitFixture(t)
	old := transformsInput(t, f, f.e.Catalog.State)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoTransformsOID)
	reject := func(in pgcatalog.Input) {
		t.Helper()
		rows, err := produce(in)
		if rows != nil || err == nil {
			t.Fatal("invalid capture admitted", rows, err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("invariant error fabricated SQLSTATE", err)
		}
	}
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Runtime = nil },
		func(in *pgcatalog.Input) { in.Owner = &engine.New().Catalog.Owner },
		func(in *pgcatalog.Input) { in.Catalog = engine.New().CatalogRead() },
		func(in *pgcatalog.Input) { in.Roles = engine.New().Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { in.Ref.Address.ObjectID++ },
		func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(in.Ref.Epoch, in.Ref.Address) },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) { in.Source = nil },
		func(in *pgcatalog.Input) {
			r := in.Runtime.(core.EngineCatalogRuntime)
			r.Auth.RoleRecord = nil
			in.Runtime = r
		},
	} {
		in := old
		mutate(&in)
		reject(in)
	}
	for _, kind := range []string{"source-name", "namespace", "width", "name", "order", "domain", "generation", "character-generation", "base-generation", "character-base-generation", "typmod", "collation", "nullable", "nullability-known", "dimensions", "origin-table", "origin-attribute", "virtual", "attributes", "attribute-width"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(f.e.Catalog.State)
			source := *old.Source
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			source.Attributes = append([]int32(nil), source.Attributes...)
			switch kind {
			case "source-name":
				source.ObjectName = "wrong"
			case "namespace":
				source.ObjectNamespace = f.schema
			case "width":
				source.Columns = source.Columns[:7]
			case "name":
				source.Columns[0].Name = "wrong"
			case "order":
				source.Columns[0], source.Columns[1] = source.Columns[1], source.Columns[0]
			case "domain":
				source.Columns[0].Usage = source.Columns[7].Usage
			case "generation":
				source.Columns[0].Usage.Type, _ = engine.New().CatalogRead().LookupType(13248)
			case "character-generation":
				source.Columns[7].Usage.Type, _ = engine.New().CatalogRead().LookupType(13246)
			case "base-generation":
				typ := *old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Name)})
				typ.SQLType, _ = engine.New().CatalogRead().LookupType(sqltype.Name)
				state.SetDescriptor(typ.Address(), &typ)
			case "character-base-generation":
				typ := *old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.VarChar)})
				typ.SQLType, _ = engine.New().CatalogRead().LookupType(sqltype.VarChar)
				state.SetDescriptor(typ.Address(), &typ)
			case "typmod":
				source.Columns[0].Usage.Typmod = 0
			case "collation":
				source.Columns[0].Usage.Collation = sqltype.CollationRef{}
			case "nullable":
				source.Columns[0].Nullable = false
			case "nullability-known":
				source.Columns[0].NullabilityKnown = false
			case "dimensions":
				source.Columns[0].DeclaredDimensions = 1
			case "origin-table":
				source.Columns[0].OriginTable++
			case "origin-attribute":
				source.Columns[0].OriginAttribute++
			case "virtual":
				source.VirtualView = false
			case "attributes":
				source.Attributes[0] = 2
			case "attribute-width":
				source.Attributes = source.Attributes[:7]
			}
			state.SetDescriptor(source.Address(), &source)
			reject(transformsInput(t, f, state))
		})
	}
}

func TestInfoTransformsCancellationAndOwnership(t *testing.T) {
	f := newDefaultUnitFixture(t)
	old := transformsInput(t, f, f.e.Catalog.State)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoTransformsOID)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	invalid := old
	invalid.Context, invalid.Source, invalid.Runtime = ctx, nil, nil
	if rows, err := produce(invalid); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation precedes invalid authority", rows, err)
	}
	if source, err := core.CaptureCatalogSourceRows(invalid, produce); source != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture published", source, err)
	}
	columns := append([]catalog.Column(nil), old.Source.Columns...)
	oldRow, _ := old.Catalog.LookupType(old.Source.RowType)
	oldArray, _ := old.Catalog.LookupArray(oldRow)
	replacement := newDefaultUnitFixture(t)
	if _, err := f.e.ReplaceCatalog(t.Context(), replacement.e, replacement.e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	fresh := transformsInput(t, f, f.e.Catalog.State)
	transformsCapture(t, fresh)
	freshRow, _ := fresh.Catalog.LookupType(fresh.Source.RowType)
	freshArray, _ := fresh.Catalog.LookupArray(freshRow)
	if old.Source == fresh.Source || oldRow == freshRow || oldArray == freshArray || old.Ref.Epoch == fresh.Ref.Epoch {
		t.Fatal("replacement reused source/row/array generation")
	}
	for i := range columns {
		if columns[i].Usage.Type == fresh.Source.Columns[i].Usage.Type || columns[i].Usage.Type.BaseType() == fresh.Source.Columns[i].Usage.Type.BaseType() {
			t.Fatal("replacement borrowed domain/base authority", i)
		}
	}
	if !reflect.DeepEqual(old.Source.Columns, columns) || oldArray.Element() != oldRow {
		t.Fatal("retained backing mutated")
	}
	stale := fresh
	stale.Source = old.Source
	if rows, err := produce(stale); rows != nil || err == nil {
		t.Fatal("old source admitted by current authority", rows, err)
	}
}

package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestPGTSTemplateCaptureContract(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3764}
	source := view.MustRelation(address)
	ref := e.Catalog.Domain.Ref(view.Epoch(), address)
	produce := registeredCatalogProducer(3764)
	if source.Name() != "pg_ts_template" || source.Namespace().ObjectID != 11 || len(source.Columns) != 5 {
		t.Fatal("template source identity", source)
	}
	names := []string{"oid", "tmplname", "tmplnamespace", "tmplinit", "tmpllexize"}
	types := []uint32{26, 19, 26, 24, 24}
	for i, column := range source.Columns {
		typ, ok := view.LookupType(column.Usage.Type.ID())
		if !ok || column.Usage.Type != typ || uint32(typ.ID()) != types[i] || column.Name != names[i] || column.OriginTable != 3764 || column.OriginAttribute != int16(i+1) || !column.NullabilityKnown || column.Nullable || column.Usage.Typmod != -1 || source.ColumnMeta.CatalogAttributes[i].NotNull != true {
			t.Fatal("template source-owned column contract", i, column)
		}
	}
	input := pgcatalog.Input{Context: context.Background(), Catalog: view, Ref: ref, Source: source, Owner: view.Owner(), Runtime: core.EngineCatalogRuntime{}}
	nextOID, nextRevision := e.Catalog.NextOID(), e.Catalog.NextRevision()
	snowballAddress := view.NameAddress(ids.NameKey{ClassID: ids.TSTemplateClassID, Namespace: source.Namespace(), Name: "snowball"})
	snowball, ok := view.TSTemplate(snowballAddress)
	if !ok || snowballAddress.ObjectID == 0 || snowballAddress.ObjectID == 3727 {
		t.Fatal("actual Snowball template seed", snowballAddress)
	}
	for range 2 {
		got, err := core.CaptureCatalogSourceRows(input, produce)
		if err != nil || got == nil {
			t.Fatal("seed template capture", got, err)
		}
		if got.Ref != ref || got.Anonymous == nil || len(got.Anonymous) != 4 {
			t.Fatal("exact simple/synonym/Snowball/thesaurus template inventory", got)
		}
		seen := map[string]bool{}
		for _, row := range got.Anonymous {
			if len(row) != 5 {
				t.Fatal("template row width", row)
			}
			for i, value := range row {
				if value.IsNull() || value.Type() != source.Columns[i].Usage.Type {
					t.Fatal("source-owned template cell", i, value)
				}
			}
			name, err := row[1].Text()
			if err != nil || seen[name] || name != "simple" && name != "synonym" && name != "snowball" && name != "thesaurus" {
				t.Fatal("template seed identity", name, err)
			}
			seen[name] = true
			wantOID, handlers := uint32(3727), []uint32{3725, 3726}
			if name == "snowball" {
				wantOID, handlers = uint32(snowballAddress.ObjectID), []uint32{uint32(snowball.Init.ObjectID), uint32(snowball.Lexize.ObjectID)}
			}
			if name == "synonym" {
				wantOID, handlers = 3730, []uint32{3728, 3729}
			}
			if name == "thesaurus" {
				wantOID, handlers = 3742, []uint32{3740, 3741}
			}
			oid, err := row[0].OID()
			if err != nil || oid != wantOID {
				t.Fatal("template actual OID", name, oid, err)
			}
			namespace, err := row[2].OID()
			if err != nil || namespace != 11 {
				t.Fatal("template namespace", name, namespace, err)
			}
			for i, want := range handlers {
				value := row[i+3]
				gotOID, err := value.ProcedureOID()
				if err != nil || gotOID != want || value.IsNull() || value.Type() != source.Columns[i+3].Usage.Type {
					t.Fatal("source-owned REGPROC handler", name, i, value, err)
				}
			}
		}
	}
	empty := catalog.CloneState(view.State())
	empty.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3727})
	empty.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3730})
	empty.RemoveDescriptor(snowballAddress)
	empty.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3742})
	emptyInput := input
	emptyInput.Catalog = e.Catalog.Read(empty)
	emptyRows, err := core.CaptureCatalogSourceRows(emptyInput, produce)
	if err != nil || emptyRows == nil || emptyRows.Ref != ref || emptyRows.Anonymous == nil || len(emptyRows.Anonymous) != 0 {
		t.Fatal("empty template source must be nonnil", emptyRows, err)
	}
	if e.Catalog.State != view.State() || e.Catalog.NextOID() != nextOID || e.Catalog.NextRevision() != nextRevision {
		t.Fatal("template read changed catalog/frontiers")
	}
	ordinaryFailure := func(in pgcatalog.Input, canceled bool) {
		t.Helper()
		got, err := core.CaptureCatalogSourceRows(in, produce)
		if got != nil || err == nil {
			t.Fatal("invalid template capture published", got, err)
		}
		if canceled && !errors.Is(err, context.Canceled) {
			t.Fatal("lost cancellation", err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("ordinary capture error acquired SQLSTATE", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	bad := input
	bad.Context = ctx
	ordinaryFailure(bad, true)
	foreign := engine.New().CatalogRead()
	stale := ref
	stale.Epoch++
	wrongAddress := ref
	wrongAddress.Address.ObjectID = 3600
	for _, owner := range []*catalog.Owner{nil, foreign.Owner()} {
		bad := input
		bad.Owner = owner
		ordinaryFailure(bad, false)
	}
	for _, tc := range []struct {
		view   catalog.Read
		ref    ids.ObjectRef
		source *catalog.Relation
	}{
		{catalog.Read{}, ref, source}, {foreign, ref, source}, {view, stale, source}, {view, wrongAddress, source}, {view, ref, nil},
	} {
		bad := input
		bad.Catalog, bad.Owner, bad.Ref, bad.Source = tc.view, tc.view.Owner(), tc.ref, tc.source
		ordinaryFailure(bad, false)
	}
	for i := range source.Columns {
		state := catalog.CloneState(view.State())
		badSource := *source
		badSource.Columns = append([]catalog.Column(nil), source.Columns...)
		badSource.Columns[i].Usage.Type, _ = foreign.LookupType(source.Columns[i].Usage.Type.ID())
		state.SetDescriptor(address, &badSource)
		bad := input
		bad.Catalog = e.Catalog.Read(state)
		bad.Owner, bad.Source = bad.Catalog.Owner(), &badSource
		ordinaryFailure(bad, false)
	}
	for _, malformed := range []string{"width", "name"} {
		state := catalog.CloneState(view.State())
		badSource := *source
		badSource.Columns = append([]catalog.Column(nil), source.Columns...)
		if malformed == "width" {
			badSource.Columns = badSource.Columns[:4]
		} else {
			badSource.Columns[0].Name = "not_oid"
		}
		state.SetDescriptor(address, &badSource)
		bad := input
		bad.Catalog = e.Catalog.Read(state)
		bad.Owner, bad.Source = bad.Catalog.Owner(), &badSource
		ordinaryFailure(bad, false)
	}
	for _, invalidRef := range []ids.ObjectRef{{}, foreign.Owner().Domain.Ref(view.Epoch(), address)} {
		bad := input
		bad.Ref = invalidRef
		ordinaryFailure(bad, false)
	}
	copySource := *source
	for _, invalidSource := range []*catalog.Relation{&copySource, foreign.MustRelation(address)} {
		bad := input
		bad.Source = invalidSource
		ordinaryFailure(bad, false)
	}
	// This descriptor exists only in a cloned read for representation coverage.
	// It is not an installed or advertised executable template.
	state := catalog.CloneState(empty)
	projection := &catalog.TSTemplate{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 90000},
		ObjectNamespace: source.Namespace(),
		ObjectName:      "projection_only",
		Lexize:          ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 90001},
	}
	state.SetDescriptor(projection.Address(), projection)
	local := input
	local.Catalog = e.Catalog.Read(state)
	captured, err := core.CaptureCatalogSourceRows(local, produce)
	if err != nil || captured == nil || len(captured.Anonymous) != 1 {
		t.Fatal("projection-only capture", captured, err)
	}
	for i, want := range []uint32{0, 90001} {
		value := captured.Anonymous[0][i+3]
		got, err := value.ProcedureOID()
		if err != nil || got != want || value.IsNull() || value.Type() != source.Columns[i+3].Usage.Type || value.Type().ID() != sqltype.Regproc {
			t.Fatal("source-owned REGPROC handler", i, value, err)
		}
	}
	if e.Catalog.State != view.State() || e.Catalog.NextOID() != nextOID || e.Catalog.NextRevision() != nextRevision {
		t.Fatal("template capture changed catalog/frontiers")
	}
}

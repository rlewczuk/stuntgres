package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/sqltype"
)

type infoSequencesFixture struct {
	f        defaultUnitFixture
	state    *catalog.State
	sequence *catalog.Relation
}

func newInfoSequencesFixture(t *testing.T) infoSequencesFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.catalog.State)
	relation := sequence.NewRelation(sequence.RelationSpec{
		Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20500}, Namespace: f.schema,
		Name: "sequence_unit", Revision: 1, Owner: f.owner, Columns: sequence.Columns(f.e.CatalogRead()),
		Options: catalog.SequenceOptions{SQLType: sqltype.Int8, Start: -9223372036854775808, Min: -9223372036854775808, Max: 9223372036854775807, Increment: 7, Cache: 1, Cycle: true},
	})
	relation.Security.ACL = authorization.ACLValue{Explicit: true}
	state.SetDescriptor(relation.Address(), relation)
	return infoSequencesFixture{f, state, relation}
}

func (f infoSequencesFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoSequencesOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: engineCatalogRuntime{auth: auth, database: "unit_database"}}
}

func infoSequencesRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoSequencesOID)
	if !ok {
		t.Fatal("missing sequences producer")
	}
	captured, err := captureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Anonymous == nil || captured.View != nil || captured.Ref != in.Ref {
		t.Fatal("anonymous capture identity", captured)
	}
	return captured.Anonymous
}

func infoSequencesValues(t *testing.T, rows [][]sqltype.Value) [][]any {
	t.Helper()
	result := make([][]any, len(rows))
	for r, row := range rows {
		result[r] = make([]any, len(row))
		for i, cell := range row {
			base, err := cell.BaseValue()
			if err != nil {
				t.Fatal(err)
			}
			if i >= 4 && i <= 6 {
				n, err := base.Integer()
				if err != nil {
					t.Fatal(err)
				}
				result[r][i] = n
			} else {
				text, err := base.Text()
				if err != nil {
					t.Fatal(err)
				}
				result[r][i] = text
			}
		}
	}
	return result
}

func infoSequencesWant(database, start, increment, cycle string) [][]any {
	return [][]any{{database, "default_schema", "sequence_unit", "bigint", int64(64), int64(2), int64(0), start, "-9223372036854775808", "9223372036854775807", increment, cycle}}
}

func TestInfoSequencesDescriptor(t *testing.T) {
	f := newInfoSequencesFixture(t)
	in := f.input(t, f.state, f.f.owner)
	rows := infoSequencesRows(t, in)
	want := infoSequencesWant("unit_database", "-9223372036854775808", "7", "YES")
	if got := infoSequencesValues(t, rows); !reflect.DeepEqual(got, want) {
		t.Fatal("complete definition", got, want)
	}
	names := []string{"sequence_catalog", "sequence_schema", "sequence_name", "data_type", "numeric_precision", "numeric_precision_radix", "numeric_scale", "start_value", "minimum_value", "maximum_value", "increment", "cycle_option"}
	for i, cell := range rows[0] {
		column := in.Source.Columns[i]
		domain, base := sqltype.ID(13246), sqltype.VarChar
		if i < 3 {
			domain, base = 13248, sqltype.Name
		}
		if i >= 4 && i <= 6 {
			domain, base = 13243, sqltype.Int4
		}
		if i == 11 {
			domain = 13256
		}
		payload, err := cell.BaseValue()
		collation := uint32(950)
		if i >= 4 && i <= 6 {
			collation = 0
		}
		if err != nil || column.Name != names[i] || cell.Type() != column.Usage.Type || cell.Type().ID() != domain || payload.Type().ID() != base || cell.IsNull() || !cell.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.Usage.Typmod != -1 || column.Usage.Collation.ID != collation || column.DeclaredDimensions != 0 || column.OriginTable != uint32(pgcatalog.InfoSequencesOID) || column.OriginAttribute != int16(i+1) {
			t.Fatal("domain carrier/descriptor", i, cell, column, err)
		}
	}
	if in.Source.RowType != 13409 {
		t.Fatal("row identity", in.Source.RowType)
	}
	row, ok := in.Catalog.LookupType(13409)
	if !ok {
		t.Fatal("missing row type")
	}
	array, ok := in.Catalog.LookupType(13408)
	if !ok || array.Element() != row {
		t.Fatal("array element identity")
	}
	for _, oid := range []ids.OID{13409, 13408} {
		d := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: oid})
		if d.OwningRelation != in.Source.Address() {
			t.Fatal("relation type backlink", oid)
		}
	}
	reader := f.input(t, f.state, f.f.reader)
	rights, err := reader.Runtime.Effective(reader.Ref, false)
	if err != nil || rights&authorization.SelectPrivilege == 0 {
		t.Fatal("PUBLIC view SELECT", rights, err)
	}
	if rows := infoSequencesRows(t, reader); len(rows) != 0 {
		t.Fatal("invented reader privilege", rows)
	}
	if rows := infoSequencesRows(t, f.input(t, f.f.e.catalog.State, f.f.owner)); len(rows) != 0 {
		t.Fatal("invented sequence", rows)
	}
}

func TestInfoSequencesAuthority(t *testing.T) {
	f := newInfoSequencesFixture(t)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoSequencesOID)
	in := f.input(t, f.state, f.f.owner)
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &New().catalog.Owner },
		func(in *pgcatalog.Input) { in.Roles = New().Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { in.Ref = New().catalog.Domain.Ref(in.Ref.Epoch, in.Ref.Address) },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) {
			r := in.Runtime.(engineCatalogRuntime)
			r.auth.RoleRecord = nil
			in.Runtime = r
		},
	} {
		bad := in
		mutate(&bad)
		if rows, err := produce(bad); rows != nil || err == nil {
			t.Fatal("invalid capture accepted", rows, err)
		}
	}
	for _, fault := range []string{"width", "domain", "options", "namespace", "numeric_type"} {
		state := catalog.CloneState(f.state)
		relation := *f.sequence
		source := *in.Source
		switch fault {
		case "width":
			source.Columns = source.Columns[:11]
			state.SetDescriptor(source.Address(), &source)
		case "domain":
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			source.Columns[0].Usage.Type, _ = in.Catalog.LookupType(sqltype.Name)
			state.SetDescriptor(source.Address(), &source)
		case "options":
			relation.Sequence = nil
			state.SetDescriptor(relation.Address(), &relation)
		case "namespace":
			relation.ObjectNamespace.ObjectID++
			state.SetDescriptor(relation.Address(), &relation)
		case "numeric_type":
			options := *relation.Sequence
			options.SQLType = sqltype.Text
			relation.Sequence = &options
			state.SetDescriptor(relation.Address(), &relation)
		}
		if rows, err := produce(f.input(t, state, f.f.owner)); rows != nil || err == nil {
			t.Fatal("malformed definition accepted", fault, rows, err)
		}
	}
	for _, actor := range []authorization.PrincipalRef{f.f.owner, f.f.member} {
		if rows := infoSequencesRows(t, f.input(t, f.state, actor)); len(rows) != 1 {
			t.Fatal("enabled owner excluded", rows)
		}
	}
	for _, rights := range []authorization.Privileges{authorization.SelectPrivilege, authorization.UpdatePrivilege, authorization.UsagePrivilege} {
		state := catalog.CloneState(f.state)
		relation := *f.sequence
		relation.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, rights, 0)}}
		state.SetDescriptor(relation.Address(), &relation)
		if rows := infoSequencesRows(t, f.input(t, state, f.f.reader)); len(rows) != 1 {
			t.Fatal("individual privilege excluded", rights, rows)
		}
	}
	for _, boundary := range []string{"identity", "owned", "table", "own_temp", "foreign_temp"} {
		state := catalog.CloneState(f.state)
		relation := *f.sequence
		options := *relation.Sequence
		relation.Sequence = &options
		count := 0
		var temporary ids.SessionToken
		switch boundary {
		case "identity":
			options.Identity = true
		case "owned":
			options.Owned = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20501, SubID: 1}
			count = 1
		case "table":
			relation.RelationKind = catalog.TableRelation
		case "own_temp", "foreign_temp":
			ns := *in.Catalog.MustNamespace(f.f.schema)
			ns.Temporary = in.Owner.Domain.Session(100)
			state.SetDescriptor(ns.Address(), &ns)
			if boundary == "own_temp" {
				temporary = ns.Temporary
				count = 1
			}
		}
		state.SetDescriptor(relation.Address(), &relation)
		input := f.input(t, state, f.f.owner)
		runtime := input.Runtime.(engineCatalogRuntime)
		runtime.temporary = temporary
		input.Runtime = runtime
		if rows := infoSequencesRows(t, input); len(rows) != count {
			t.Fatal("object boundary", boundary, rows)
		}
	}
}

func TestInfoSequencesCancellation(t *testing.T) {
	f := newInfoSequencesFixture(t)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoSequencesOID)
	in := f.input(t, f.state, f.f.owner)
	for _, remaining := range []int{1, 4} {
		input := in
		input.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(input); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation exposed prefix", remaining, rows, err)
		}
	}
	if rows, err := produce(in); err != nil || len(rows) != 1 {
		t.Fatal("healthy producer", rows, err)
	}
}

func TestInfoSequencesCapture(t *testing.T) {
	f := newInfoSequencesFixture(t)
	f.f.e.catalog.PublishLocked(f.state)
	f.f.op.Session.DatabaseName = "physical_capture_database"
	f.f.actor(f.f.member)
	scope, err := f.f.e.StartStatement(f.f.op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	})
	f.f.e.mu.Lock()
	err = f.f.e.captureCommentSnapshotLocked(scope)
	f.f.e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	view := scope.inquiry.commentCatalog
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoSequencesOID})
	oldReader := f.input(t, f.state, f.f.reader)
	// Publish options, ACL and role roots only after capturing the paired frontier.
	next := catalog.CloneState(f.state)
	relation := *f.sequence
	options := *relation.Sequence
	options.Start, options.Increment, options.Cycle = 21, 3, false
	relation.Sequence = &options
	relation.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.UsagePrivilege, 0)}}
	next.SetDescriptor(relation.Address(), &relation)
	roles := cloneRoleFixture(f.f.e.roles.Published)
	var edges []authorization.Membership
	for _, edge := range fixtureEdges(roles) {
		if edge.Member != f.f.member || edge.Role != f.f.owner {
			edges = append(edges, edge)
		}
	}
	setFixtureEdges(roles, edges)
	f.f.e.catalog.PublishLocked(next)
	f.f.e.roles.PublishLocked(roles)
	f.f.e.mu.Lock()
	captured, err := f.f.e.openRelationSourceLocked(scope, view.Owner().Domain.Ref(view.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
	if err != nil || captured == nil {
		t.Fatal("paired statement capture", err)
	}
	defer captured.Close()
	old := infoSequencesWant("physical_capture_database", "-9223372036854775808", "7", "YES")
	if got := infoSequencesValues(t, captured.Anonymous); !reflect.DeepEqual(got, old) {
		t.Fatal("mixed options/roles frontier", got, old)
	}
	if rows := infoSequencesRows(t, oldReader); len(rows) != 0 {
		t.Fatal("old ACL capture refreshed independently", rows)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []authorization.PrincipalRef{f.f.member, f.f.owner, f.f.reader} {
		f.f.actor(actor)
		scope, err = f.f.e.StartStatement(f.f.op)
		if err != nil {
			t.Fatal(err)
		}
		f.f.e.mu.Lock()
		err = f.f.e.captureCommentSnapshotLocked(scope)
		f.f.e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		freshView := scope.inquiry.commentCatalog
		f.f.e.mu.Lock()
		fresh, err := f.f.e.openRelationSourceLocked(scope, freshView.Owner().Domain.Ref(freshView.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
		if err != nil || fresh == nil {
			t.Fatal("fresh statement capture", err)
		}
		want := make([][]any, 0)
		if actor != f.f.member {
			want = infoSequencesWant("physical_capture_database", "21", "3", "NO")
		}
		if got := infoSequencesValues(t, fresh.Anonymous); !reflect.DeepEqual(got, want) {
			t.Fatal("fresh options/visibility", got, want)
		}
		fresh.Close()
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := infoSequencesValues(t, captured.Anonymous); !reflect.DeepEqual(got, old) {
		t.Fatal("retained rows mutated", got, old)
	}
}

package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func sqlFeaturesUnitInput(t *testing.T, f defaultUnitFixture, view catalog.Read) pgcatalog.Input {
	t.Helper()
	auth := dtpUnitAuthority(t, f, view)
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoSQLFeaturesOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: engine.EngineCatalogRuntime{Auth: auth, Database: "unit_database"}}
}

func sqlFeaturesUnitRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoSQLFeaturesOID)
	if !ok {
		t.Fatal("missing sql_features producer")
	}
	captured, err := engine.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Ref != in.Ref || captured.View != nil || len(captured.Anonymous) != 755 {
		t.Fatal("full anonymous capture", captured)
	}
	for _, row := range captured.Anonymous {
		if len(row) != 7 {
			t.Fatal("row width", len(row))
		}
		for i, value := range row {
			column := in.Source.Columns[i]
			id := sqltype.ID(13246)
			if i == 4 {
				id = 13256
			}
			if value.Type() != column.Usage.Type || value.Type().ID() != id || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(pgcatalog.InfoSQLFeaturesOID) || column.OriginAttribute != int16(i+1) {
				t.Fatal("domain/provenance", i, value, column)
			}
			if value.IsNull() != (i == 5) {
				t.Fatal("NULL boundary", i, value)
			}
		}
	}
	return captured.Anonymous
}

func sqlFeaturesUnitText(t *testing.T, value sqltype.Value) string {
	t.Helper()
	base, err := value.BaseValue()
	if err != nil || base.Type().ID() != sqltype.VarChar {
		t.Fatal("VARCHAR payload lost", base, err)
	}
	text, err := base.Text()
	if err != nil {
		t.Fatal("VARCHAR text", err)
	}
	return text
}

func sqlFeaturesUnitBag(t *testing.T, rows [][]sqltype.Value) map[[7]string]int {
	t.Helper()
	bag := make(map[[7]string]int)
	for _, row := range rows {
		var key [7]string
		for i, value := range row {
			if i != 5 {
				key[i] = sqlFeaturesUnitText(t, value)
			}
		}
		bag[key]++
	}
	return bag
}

func TestInfoSQLFeaturesProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	in := sqlFeaturesUnitInput(t, f, view)
	rows := sqlFeaturesUnitRows(t, in)
	source := in.Source
	if source.VirtualView || source.RelationKind != catalog.CatalogRelation || source.History.HasIndexes || source.RowType != 13414 {
		t.Fatal("physical relation identity", source)
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", acl)
	}
	rowtype, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13414})
	if !ok || rowtype.OwningRelation != source.Address() || rowtype.Array != 13413 {
		t.Fatal("owned rowtype", rowtype)
	}
	array, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13413})
	if !ok || array.OwningRelation != source.Address() || array.SQLType.Element() != rowtype.SQLType {
		t.Fatal("reciprocal array", array)
	}
	if len(source.ColumnMeta.CatalogAttributes) != 7 {
		t.Fatal("attribute width")
	}
	for _, attribute := range source.ColumnMeta.CatalogAttributes {
		if attribute.NotNull || attribute.Collation != 950 || attribute.Dimensions != 0 {
			t.Fatal("physical nullable attribute", attribute)
		}
	}
	counts := map[string]int{}
	for _, row := range rows {
		counts[sqlFeaturesUnitText(t, row[4])]++
		if sqlFeaturesUnitText(t, row[2]) == "" {
			counts["parent"]++
		}
		if sqlFeaturesUnitText(t, row[6]) == "" {
			counts["blank_comment"]++
		}
	}
	if !reflect.DeepEqual(counts, map[string]int{"YES": 459, "NO": 296, "parent": 615, "blank_comment": 719}) {
		t.Fatal("release counts", counts)
	}
	bag := sqlFeaturesUnitBag(t, rows)
	for _, expected := range [][7]string{
		{"B011", "Embedded Ada", "", "", "NO", "", ""},
		{"E021", "Character string types", "04", "CHARACTER_LENGTH function", "YES", "", "trims trailing spaces from CHARACTER values before counting"},
		{"T321", "Basic SQL-invoked routines", "", "", "NO", "", "partially supported"},
		{"T321", "Basic SQL-invoked routines", "06", "ROUTINES view", "YES", "", ""},
		{"T616", "Null treatment option for LEAD and LAG functions", "", "", "NO", "", ""},
		{"T824", "JSON_TABLE: specific PLAN clause", "", "", "NO", "", ""},
		{"T838", "JSON_TABLE: PLAN DEFAULT clause", "", "", "NO", "", ""},
	} {
		if bag[expected] != 1 {
			t.Fatal("release witness", expected, bag[expected])
		}
	}
	second := sqlFeaturesUnitRows(t, in)
	if !reflect.DeepEqual(bag, sqlFeaturesUnitBag(t, second)) {
		t.Fatal("capture changed release rows")
	}
	other := newDefaultUnitFixture(t)
	sqlFeaturesUnitRows(t, sqlFeaturesUnitInput(t, other, other.e.CatalogRead()))
	if !reflect.DeepEqual(bag, sqlFeaturesUnitBag(t, rows)) {
		t.Fatal("foreign engine changed retained values")
	}
	rows[0][0] = sqltype.Null(rows[0][0].Type())
	if second[0][0].IsNull() {
		t.Fatal("captures share mutable backing")
	}
	if !reflect.DeepEqual(bag, sqlFeaturesUnitBag(t, sqlFeaturesUnitRows(t, in))) {
		t.Fatal("capture mutation changed release source")
	}
}

func TestInfoSQLFeaturesCapture(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	in := sqlFeaturesUnitInput(t, f, view)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoSQLFeaturesOID)
	foreign := engine.New()
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &foreign.Catalog.Owner },
		func(in *pgcatalog.Input) { in.Roles = foreign.Roles() },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) { in.Catalog = catalog.Read{} },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { copied := *in.Source; in.Source = &copied },
		func(in *pgcatalog.Input) {
			r := in.Runtime.(engine.EngineCatalogRuntime)
			r.Auth.RoleRecord = nil
			in.Runtime = r
		},
		func(in *pgcatalog.Input) {
			r := in.Runtime.(engine.EngineCatalogRuntime)
			r.Auth.State.CurrentUser = authorization.PrincipalRef{}
			in.Runtime = r
		},
		func(in *pgcatalog.Input) {
			r := in.Runtime.(engine.EngineCatalogRuntime)
			r.Auth.State.CurrentUser = foreign.Roles().LookupRef("postgres")
			in.Runtime = r
		},
	} {
		bad := in
		mutate(&bad)
		if rows, err := produce(bad); rows != nil || err == nil {
			t.Fatal("invalid authority accepted", rows, err)
		}
	}
	for _, malformed := range []string{"width", "base_column", "wrong_domain", "foreign_domain", "foreign_yes", "missing_domain", "missing_base", "foreign_base"} {
		bad := catalog.CloneState(view.State())
		copy := *in.Source
		copy.Columns = append([]catalog.Column(nil), in.Source.Columns...)
		switch malformed {
		case "width":
			copy.Columns = copy.Columns[:6]
		case "base_column":
			copy.Columns[0].Usage.Type, _ = view.LookupType(sqltype.VarChar)
		case "wrong_domain":
			copy.Columns[0].Usage.Type, _ = view.LookupType(13256)
		case "foreign_domain":
			copy.Columns[0].Usage.Type, _ = foreign.CatalogRead().LookupType(13246)
		case "foreign_yes":
			copy.Columns[4].Usage.Type, _ = foreign.CatalogRead().LookupType(13256)
		case "missing_domain":
			bad.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13246})
		case "missing_base":
			bad.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.VarChar)})
		case "foreign_base":
			address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.VarChar)}
			descriptor := *view.Get(address).(*catalog.Type)
			descriptor.SQLType, _ = foreign.CatalogRead().LookupType(sqltype.VarChar)
			bad.SetDescriptor(address, &descriptor)
		}
		bad.SetDescriptor(copy.Address(), &copy)
		if rows, err := produce(sqlFeaturesUnitInput(t, f, f.e.Catalog.Read(bad))); rows != nil || err == nil {
			t.Fatal("malformed metadata accepted", malformed, rows, err)
		}
	}
}

func TestInfoSQLFeaturesCancellation(t *testing.T) {
	f := newDefaultUnitFixture(t)
	in := sqlFeaturesUnitInput(t, f, f.e.CatalogRead())
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoSQLFeaturesOID)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := in
	canceled.Context = ctx
	if rows, err := produce(canceled); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancellation exposed rows", rows, err)
	}
	// Entry, one check per row, and final publication have deterministic edges.
	for _, remaining := range []int{2, 300, 757} {
		canceled.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(canceled); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation exposed prefix", remaining, rows, err)
		}
	}
	sqlFeaturesUnitRows(t, in)
}

func TestInfoSQLFeaturesCatalogACLRecheck(t *testing.T) {
	f := newDefaultUnitFixture(t)
	e := f.e
	d := e.CatalogRead().MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoSQLFeaturesOID})
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
	admin, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	role := roleAttributeCreate(t, e, admin, "sql_features_reader", true, parser.RoleOptions{})
	reader, readerDone := enginetest.RoleOperation(t, e, role.Name)
	defer readerDone()
	requirement := access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege}
	retained, err := engine.OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := retained.Check(requirement); err != nil {
		t.Fatal("PUBLIC SELECT", err)
	}
	if err := enginetest.SetObjectACL(e, t.Context(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	denied, err := engine.OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	domainUnitDiagnostic(t, denied.Check(requirement), "42501", "permission denied for table sql_features")
	if err := retained.Check(requirement); err != nil {
		t.Fatal("retained ACL mutated", err)
	}
	current := e.CatalogRead().MustRelation(d.Address())
	if current == d || !current.Security.ACL.Explicit || len(current.Security.ACL.Entries) != 0 || len(d.Security.ACL.Entries) != 1 {
		t.Fatal("ACL roots not isolated")
	}
	if err := enginetest.SetObjectACL(e, t.Context(), ref, d.Security.ACL); err != nil {
		t.Fatal(err)
	}
	restored, err := engine.OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Check(requirement); err != nil {
		t.Fatal("restored SELECT", err)
	}
	domainUnitDiagnostic(t, denied.Check(requirement), "42501", "permission denied for table sql_features")
	if err := retained.Check(requirement); err != nil {
		t.Fatal("restoration mutated retained ACL", err)
	}
	// Schema admission is separate from this globally readable matrix.
	namespace := e.CatalogRead().MustNamespace(d.Namespace())
	nsRef := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), namespace.Address())
	if err := restored.CanUse(nsRef); err != nil {
		t.Fatal("PUBLIC schema USAGE", err)
	}
	if err := enginetest.SetObjectACL(e, t.Context(), nsRef, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	noUsage, err := engine.OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	domainUnitDiagnostic(t, noUsage.CanUse(nsRef), "42501", "permission denied for schema information_schema")
	if err := restored.CanUse(nsRef); err != nil {
		t.Fatal("retained schema ACL mutated", err)
	}
	if err := enginetest.SetObjectACL(e, t.Context(), nsRef, namespace.ACL); err != nil {
		t.Fatal(err)
	}
	withUsage, err := engine.OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := withUsage.CanUse(nsRef); err != nil {
		t.Fatal("restored schema USAGE", err)
	}
	domainUnitDiagnostic(t, noUsage.CanUse(nsRef), "42501", "permission denied for schema information_schema")
}

func roleAttributeCreate(t *testing.T, e *engine.Engine, op *session.Operation, name string, login bool, o parser.RoleOptions) authorization.RoleDescriptor {
	t.Helper()
	roleAttributeApply(t, e, op, &parser.CreateRole{Name: parser.Identifier{Text: name}, DefaultLogin: login, Options: o})
	d, ok := e.Roles().Lookup(name)
	if !ok {
		t.Fatal("missing role", name)
	}
	return d
}

func domainUnitDiagnostic(t *testing.T, err error, code, message string) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != code || d.Message != message {
		t.Fatal(err, code, message)
	}
}

func roleAttributeApply(t *testing.T, e *engine.Engine, op *session.Operation, s parser.Statement) {
	t.Helper()
	r, err := e.Execute(op, s, 0, nil)
	if err != nil || r.Count != 0 || r.Rows != nil {
		t.Fatalf("attribute utility: result=%#v error=%v", r, err)
	}
}

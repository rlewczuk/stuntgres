package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func sqlSizingUnitInput(t *testing.T, f defaultUnitFixture, view catalog.Read) pgcatalog.Input {
	t.Helper()
	auth := dtpUnitAuthority(t, f, view)
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoSQLSizingOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: EngineCatalogRuntime{Auth: auth, Database: "unit_database"}}
}

func sqlSizingUnitRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoSQLSizingOID)
	if !ok {
		t.Fatal("missing sql_sizing producer")
	}
	captured, err := CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Ref != in.Ref || captured.View != nil || len(captured.Anonymous) != 23 {
		t.Fatal("full anonymous capture", captured)
	}
	for _, row := range captured.Anonymous {
		if len(row) != 4 {
			t.Fatal("row width", len(row))
		}
		for i, value := range row {
			column := in.Source.Columns[i]
			id := sqltype.ID(13243)
			if i%2 != 0 {
				id = 13246
			}
			if value.Type() != column.Usage.Type || value.Type().ID() != id || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(pgcatalog.InfoSQLSizingOID) || column.OriginAttribute != int16(i+1) {
				t.Fatal("domain/provenance", i, value, column)
			}
		}
	}
	return captured.Anonymous
}

func sqlSizingUnitValues(t *testing.T, rows [][]sqltype.Value) [][]any {
	t.Helper()
	values := make([][]any, len(rows))
	for i, row := range rows {
		values[i] = make([]any, 4)
		for j, v := range row {
			base, err := v.BaseValue()
			if err != nil {
				t.Fatal(err)
			}
			id := sqltype.Int4
			if j%2 != 0 {
				id = sqltype.VarChar
			}
			if base.Type().ID() != id || base.IsNull() != v.IsNull() {
				t.Fatal("physical payload", v, base)
			}
			if v.IsNull() {
				continue
			}
			if j%2 == 0 {
				values[i][j], err = base.Integer()
			} else {
				values[i][j], err = base.Text()
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i][0].(int64) < values[j][0].(int64) })
	return values
}

// This literal native expectation is independent of the producer inventory.
func sqlSizingUnitExpected() [][]any {
	const comment = "Might be less, depending on character set."
	return [][]any{
		{int64(0), "MAXIMUM DRIVER CONNECTIONS", nil, nil},
		{int64(1), "MAXIMUM CONCURRENT ACTIVITIES", int64(0), nil},
		{int64(30), "MAXIMUM COLUMN NAME LENGTH", int64(63), comment},
		{int64(31), "MAXIMUM CURSOR NAME LENGTH", int64(0), nil},
		{int64(32), "MAXIMUM SCHEMA NAME LENGTH", int64(63), comment},
		{int64(34), "MAXIMUM CATALOG NAME LENGTH", int64(63), comment},
		{int64(35), "MAXIMUM TABLE NAME LENGTH", int64(63), comment},
		{int64(97), "MAXIMUM COLUMNS IN GROUP BY", int64(0), nil},
		{int64(99), "MAXIMUM COLUMNS IN ORDER BY", int64(0), nil},
		{int64(100), "MAXIMUM COLUMNS IN SELECT", int64(0), nil},
		{int64(101), "MAXIMUM COLUMNS IN TABLE", int64(1600), nil},
		{int64(106), "MAXIMUM TABLES IN SELECT", int64(0), nil},
		{int64(107), "MAXIMUM USER NAME LENGTH", int64(63), comment},
		{int64(10005), "MAXIMUM IDENTIFIER LENGTH", int64(63), comment},
		{int64(20000), "MAXIMUM STATEMENT OCTETS", int64(0), nil},
		{int64(20001), "MAXIMUM STATEMENT OCTETS DATA", int64(0), nil},
		{int64(20002), "MAXIMUM STATEMENT OCTETS SCHEMA", int64(0), nil},
		{int64(25000), "MAXIMUM CURRENT DEFAULT TRANSFORM GROUP LENGTH", nil, nil},
		{int64(25001), "MAXIMUM CURRENT TRANSFORM GROUP LENGTH", nil, nil},
		{int64(25002), "MAXIMUM CURRENT PATH LENGTH", int64(0), nil},
		{int64(25003), "MAXIMUM CURRENT ROLE LENGTH", nil, nil},
		{int64(25004), "MAXIMUM SESSION USER LENGTH", int64(63), comment},
		{int64(25005), "MAXIMUM SYSTEM USER LENGTH", int64(0), nil},
	}
}

func TestInfoSQLSizingProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	in := sqlSizingUnitInput(t, f, view)
	rows := sqlSizingUnitRows(t, in)
	want := sqlSizingUnitExpected()
	if got := sqlSizingUnitValues(t, rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows=%#v want %#v", got, want)
	}
	source := in.Source
	if source.VirtualView || source.RelationKind != catalog.CatalogRelation || source.History.HasIndexes || source.RowType != 13429 {
		t.Fatal("physical relation identity", source)
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", acl)
	}
	rowtype, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13429})
	if !ok || rowtype.OwningRelation != source.Address() || rowtype.Array != 13428 {
		t.Fatal("owned rowtype", rowtype)
	}
	array, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13428})
	if !ok || array.OwningRelation != source.Address() || array.SQLType.Element() != rowtype.SQLType {
		t.Fatal("reciprocal array", array)
	}
	if len(source.ColumnMeta.CatalogAttributes) != 4 {
		t.Fatal("attribute width")
	}
	for i, attribute := range source.ColumnMeta.CatalogAttributes {
		collation := uint32(0)
		if i%2 != 0 {
			collation = 950
		}
		if attribute.NotNull || uint32(attribute.Collation) != collation || attribute.Dimensions != 0 {
			t.Fatal("physical nullable attribute", attribute)
		}
	}
	counts := map[string]int{}
	idsSeen, namesSeen := map[any]bool{}, map[any]bool{}
	for _, row := range sqlSizingUnitValues(t, rows) {
		if idsSeen[row[0]] || namesSeen[row[1]] {
			t.Fatal("duplicate item", row)
		}
		idsSeen[row[0]], namesSeen[row[1]] = true, true
		counts[fmt.Sprint(row[2])]++
		if row[3] == nil {
			counts["NULL comment"]++
		} else {
			counts["comment"]++
		}
	}
	if !reflect.DeepEqual(counts, map[string]int{"<nil>": 4, "0": 11, "63": 7, "1600": 1, "NULL comment": 16, "comment": 7}) {
		t.Fatal("native sizing totals", counts)
	}
	second := sqlSizingUnitRows(t, in)
	if !reflect.DeepEqual(sqlSizingUnitValues(t, second), want) {
		t.Fatal("second capture changed rows")
	}
	other := newDefaultUnitFixture(t)
	sqlSizingUnitRows(t, sqlSizingUnitInput(t, other, other.e.CatalogRead()))
	if !reflect.DeepEqual(sqlSizingUnitValues(t, rows), want) {
		t.Fatal("foreign engine changed retained values")
	}
	rows[0][0] = sqltype.Null(rows[0][0].Type())
	if second[0][0].IsNull() {
		t.Fatal("captures share mutable backing")
	}
	if !reflect.DeepEqual(sqlSizingUnitValues(t, sqlSizingUnitRows(t, in)), want) {
		t.Fatal("capture mutation changed inventory")
	}
}

func sqlSizingUnitOrdinaryFailure(t *testing.T, rows [][]sqltype.Value, err error) {
	t.Helper()
	var state interface{ SQLState() string }
	if rows != nil || err == nil || errors.As(err, &state) {
		t.Fatal("invalid capture must return nil rows and an ordinary error", rows, err)
	}
}

func TestInfoSQLSizingCapture(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	in := sqlSizingUnitInput(t, f, view)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoSQLSizingOID)
	foreign := New()
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &foreign.Catalog.Owner },
		func(in *pgcatalog.Input) { in.Roles = foreign.Roles() },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) { in.Catalog = catalog.Read{} },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { copied := *in.Source; in.Source = &copied },
		func(in *pgcatalog.Input) {
			r := in.Runtime.(EngineCatalogRuntime)
			r.Auth.RoleRecord = nil
			in.Runtime = r
		},
		func(in *pgcatalog.Input) {
			r := in.Runtime.(EngineCatalogRuntime)
			r.Auth.State.CurrentUser = authorization.PrincipalRef{}
			in.Runtime = r
		},
		func(in *pgcatalog.Input) {
			r := in.Runtime.(EngineCatalogRuntime)
			r.Auth.State.CurrentUser = foreign.Roles().LookupRef("postgres")
			in.Runtime = r
		},
	} {
		bad := in
		mutate(&bad)
		rows, err := produce(bad)
		sqlSizingUnitOrdinaryFailure(t, rows, err)
	}
	for column := range 4 {
		for _, malformed := range []string{"primitive", "wrong", "foreign"} {
			t.Run(fmt.Sprintf("column%d/%s", column, malformed), func(t *testing.T) {
				state := catalog.CloneState(view.State())
				copy := *in.Source
				copy.Columns = append([]catalog.Column(nil), in.Source.Columns...)
				domain, base, wrong := sqltype.ID(13243), sqltype.Int4, sqltype.ID(13246)
				if column%2 != 0 {
					domain, base, wrong = 13246, sqltype.VarChar, 13243
				}
				switch malformed {
				case "primitive":
					copy.Columns[column].Usage.Type, _ = view.LookupType(base)
				case "wrong":
					copy.Columns[column].Usage.Type, _ = view.LookupType(wrong)
				case "foreign":
					copy.Columns[column].Usage.Type, _ = foreign.CatalogRead().LookupType(domain)
				}
				state.SetDescriptor(copy.Address(), &copy)
				rows, err := produce(sqlSizingUnitInput(t, f, f.e.Catalog.Read(state)))
				sqlSizingUnitOrdinaryFailure(t, rows, err)
			})
		}
	}
	for _, id := range []sqltype.ID{13243, 13246, sqltype.Int4, sqltype.VarChar} {
		for _, malformed := range []string{"missing", "foreign"} {
			t.Run(fmt.Sprintf("type%d/%s", id, malformed), func(t *testing.T) {
				state := catalog.CloneState(view.State())
				address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}
				if malformed == "missing" {
					state.RemoveDescriptor(address)
				} else {
					descriptor := *view.Get(address).(*catalog.Type)
					descriptor.SQLType, _ = foreign.CatalogRead().LookupType(id)
					state.SetDescriptor(address, &descriptor)
				}
				rows, err := produce(sqlSizingUnitInput(t, f, f.e.Catalog.Read(state)))
				sqlSizingUnitOrdinaryFailure(t, rows, err)
			})
		}
	}
	state := catalog.CloneState(view.State())
	copy := *in.Source
	copy.Columns = copy.Columns[:3]
	state.SetDescriptor(copy.Address(), &copy)
	rows, err := produce(sqlSizingUnitInput(t, f, f.e.Catalog.Read(state)))
	sqlSizingUnitOrdinaryFailure(t, rows, err)
}

func TestInfoSQLSizingCancellation(t *testing.T) {
	f := newDefaultUnitFixture(t)
	in := sqlSizingUnitInput(t, f, f.e.CatalogRead())
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoSQLSizingOID)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := in
	canceled.Context = ctx
	if rows, err := produce(canceled); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancellation exposed rows", rows, err)
	}
	// Entry + 23 row checks + final publication; test each exact edge.
	for remaining := 1; remaining <= 25; remaining++ {
		canceled.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(canceled); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation exposed prefix", remaining, rows, err)
		}
	}
	if !reflect.DeepEqual(sqlSizingUnitValues(t, sqlSizingUnitRows(t, in)), sqlSizingUnitExpected()) {
		t.Fatal("canceled capture changed inventory")
	}
}

func TestInfoSQLSizingCatalogACLRecheck(t *testing.T) {
	f := newDefaultUnitFixture(t)
	e := f.e
	d := e.CatalogRead().MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoSQLSizingOID})
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	role := roleAttributeCreate(t, e, admin, "sql_sizing_reader", true, parser.RoleOptions{})
	reader, readerDone := roleOperation(t, e, role.Name)
	defer readerDone()
	requirement := access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege}
	retained, err := OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := retained.Check(requirement); err != nil {
		t.Fatal("PUBLIC SELECT", err)
	}
	if err := e.SetObjectACL(t.Context(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	denied, err := OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	domainUnitDiagnostic(t, denied.Check(requirement), "42501", "permission denied for table sql_sizing")
	if err := retained.Check(requirement); err != nil {
		t.Fatal("retained ACL mutated", err)
	}
	current := e.CatalogRead().MustRelation(d.Address())
	if current == d || !current.Security.ACL.Explicit || len(current.Security.ACL.Entries) != 0 || len(d.Security.ACL.Entries) != 1 {
		t.Fatal("ACL roots not isolated")
	}
	if err := e.SetObjectACL(t.Context(), ref, d.Security.ACL); err != nil {
		t.Fatal(err)
	}
	restored, err := OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Check(requirement); err != nil {
		t.Fatal("restored SELECT", err)
	}
	domainUnitDiagnostic(t, denied.Check(requirement), "42501", "permission denied for table sql_sizing")
	if err := retained.Check(requirement); err != nil {
		t.Fatal("restoration mutated retained ACL", err)
	}
	namespace := e.CatalogRead().MustNamespace(d.Namespace())
	nsRef := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), namespace.Address())
	if err := restored.CanUse(nsRef); err != nil {
		t.Fatal("PUBLIC schema USAGE", err)
	}
	if err := e.SetObjectACL(t.Context(), nsRef, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	noUsage, err := OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	domainUnitDiagnostic(t, noUsage.CanUse(nsRef), "42501", "permission denied for schema information_schema")
	if err := restored.CanUse(nsRef); err != nil {
		t.Fatal("retained schema ACL mutated", err)
	}
	if err := e.SetObjectACL(t.Context(), nsRef, namespace.ACL); err != nil {
		t.Fatal(err)
	}
	withUsage, err := OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := withUsage.CanUse(nsRef); err != nil {
		t.Fatal("restored schema USAGE", err)
	}
	domainUnitDiagnostic(t, noUsage.CanUse(nsRef), "42501", "permission denied for schema information_schema")
}

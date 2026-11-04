package enginetests_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestSeclabelsCaptureAuthority(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.SeclabelsOID}
	source := view.MustRelation(address)
	in := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: e.Roles(), Owner: view.Owner(), Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Source: source}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.SeclabelsOID)
	if !ok || !pgcatalog.RegisteredSource(view, source) {
		t.Fatal("missing registered view")
	}
	if pgcatalog.SeclabelsOID != 12099 || source.Address().ObjectID != 12099 || pgcatalog.SeclabelsRuleOID != 12361 || source.CatalogReturnRule != 12361 || source.ReturnRuleOID() != 12361 || source.View != nil || !catalog.DefinitionOIDUsed(source, 12361) || source.ReturnRuleOID() >= e.Catalog.NextOID() {
		t.Fatal("native view and genuine return-rule identities", source)
	}
	databaseView := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatDatabaseOID})
	if pgcatalog.StatDatabaseRuleOID != 12360 || databaseView.ReturnRuleOID() != 12360 {
		t.Fatal("database return-rule identity changed", databaseView)
	}
	// Walk the entire installed catalog: a rule cannot collide with any
	// globally allocated descriptor or another descriptor's owned definition.
	if err := catalog.ValidateDefinitionOIDs(view); err != nil {
		t.Fatal("installed definition identities", err)
	}
	descriptorClaims, definitionClaims := 0, 0
	for installedAddress, descriptor := range view.Descriptors() {
		if installedAddress.ClassID != ids.LargeObjectClassID && installedAddress.ClassID != ids.ReplicationOriginClassID && installedAddress.ObjectID == 12361 {
			descriptorClaims++
		}
		if err := catalog.VisitDefinitionOIDs(descriptor, func(id ids.OID) error {
			if id == 12361 {
				definitionClaims++
				if installedAddress != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12099}) || descriptor != source {
					t.Fatal("foreign owner of label view return rule", installedAddress, descriptor)
				}
			}
			return nil
		}); err != nil {
			t.Fatal("installed owned identity traversal", err)
		}
	}
	if descriptorClaims != 0 || definitionClaims != 1 {
		t.Fatal("return rule must have exactly one global owner", descriptorClaims, definitionClaims)
	}
	e.Mu.Lock()
	liveNext := e.Catalog.NextOID()
	cursor := ids.OID(12360)
	allocated, allocationErr := e.Catalog.AllocateOIDFromLocked(&cursor, 0)
	afterNext := e.Catalog.NextOID()
	e.Mu.Unlock()
	if allocationErr != nil || allocated <= 12361 || allocated == 12360 || allocated == 12361 || cursor != allocated+1 || afterNext != liveNext {
		t.Fatal("private allocation must skip both return rules without advancing live frontier", allocated, cursor, liveNext, afterNext, allocationErr)
	}
	specCount, producerCount := 0, 0
	for _, spec := range pgcatalog.Specs {
		if spec.OID == 12099 {
			specCount++
		}
	}
	for _, registration := range pgcatalog.Registrations() {
		if registration.OID == 12099 {
			producerCount++
		}
	}
	if specCount != 1 || producerCount != 1 {
		t.Fatal("duplicate or missing label view registration", specCount, producerCount)
	}
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12101})
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12100})
	if source.RowType != 12101 || row.Array != 12100 || row.OwningRelation != address || array.OwningRelation != address {
		t.Fatal("nominal view identities", source, row, array)
	}
	if len(source.Columns) != 8 || !source.VirtualView || !source.CatalogView || pgcatalog.CatalogHasIndexes(pgcatalog.SeclabelsOID) {
		t.Fatal("view descriptor", source)
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 2 {
		t.Fatal("explicit owner/PUBLIC view ACL", acl)
	}
	owner, public := false, false
	for _, entry := range acl.Entries {
		if entry.Grantor != source.Owner {
			t.Fatal("foreign ACL grantor", entry)
		}
		if entry.Grantee.Public {
			public = entry.Privileges == authorization.SelectPrivilege
		} else if entry.Grantee.Role == source.Owner {
			owner = entry.Privileges == authorization.TableGrantPrivileges
		}
	}
	if !owner || !public {
		t.Fatal("owner defaults and PUBLIC SELECT", acl)
	}
	for i, typ := range [...]sqltype.ID{26, 26, 23, 25, 26, 25, 25, 25} {
		owned, ok := view.LookupType(typ)
		column := source.Columns[i]
		usage := owned.Usage()
		if i >= 5 {
			usage.Collation.ID = 950
		}
		if !ok || column.Usage.Type != owned || column.OriginTable != 12099 || column.OriginAttribute != int16(i+1) || !column.Nullable || !column.NullabilityKnown || column.Usage != usage || column.DeclaredDimensions != 0 {
			t.Fatal("owned descriptor", i, column)
		}
		fact := source.ColumnMeta.CatalogAttributes[i]
		if fact.NotNull || fact.Dimensions != 0 || fact.Collation != usage.Collation.ID {
			t.Fatal("nullable view attribute facts", i, fact)
		}
	}
	state := view.State()
	for range 2 {
		if rows, err := produce(in); err != nil || rows != nil {
			t.Fatal("allocation-free empty source", rows, err)
		}
		if got, err := catalogrt.CaptureRows(in, produce); err != nil || got == nil || got.Ref != in.Ref || len(got.Anonymous) != 0 {
			t.Fatal("empty capture", got, err)
		}
	}
	if e.CatalogRead().State() != state {
		t.Fatal("capture published state")
	}
	foreign := engine.New()
	for _, mutate := range []func(*pgcatalog.Input){
		func(x *pgcatalog.Input) { x.Owner = nil },
		func(x *pgcatalog.Input) { x.Owner = foreign.CatalogRead().Owner() },
		func(x *pgcatalog.Input) { x.Catalog = foreign.CatalogRead() },
		func(x *pgcatalog.Input) { x.Roles = foreign.Roles() },
		func(x *pgcatalog.Input) { x.Source = nil },
		func(x *pgcatalog.Input) { x.Ref = ids.ObjectRef{} },
		func(x *pgcatalog.Input) { x.Ref.Epoch++ },
		func(x *pgcatalog.Input) { x.Ref.Address.ObjectID++ },
		func(x *pgcatalog.Input) { x.Ref = foreign.Catalog.Domain.Ref(x.Catalog.Epoch(), address) },
		func(x *pgcatalog.Input) { copy := *x.Source; x.Source = &copy },
	} {
		x := in
		mutate(&x)
		if rows, err := produce(x); rows != nil || err == nil {
			t.Fatal("foreign authority admitted", rows, err)
		}
		if got, err := catalogrt.CaptureRows(x, produce); got != nil || err == nil {
			t.Fatal("foreign authority published capture", got, err)
		}
	}
	for _, mutate := range []func(*catalog.Relation){
		func(d *catalog.Relation) { d.Columns = d.Columns[:7] },
		func(d *catalog.Relation) { d.Columns = append(d.Columns, d.Columns[0]) },
		func(d *catalog.Relation) { d.Columns[0].Name = "forged" },
		func(d *catalog.Relation) { d.Columns[0].OriginTable++ },
		func(d *catalog.Relation) { d.Columns[0].OriginAttribute++ },
		func(d *catalog.Relation) { d.Columns[0].Nullable = false },
		func(d *catalog.Relation) { d.Columns[0].NullabilityKnown = false },
		func(d *catalog.Relation) { d.Columns[0].Usage.Type, _ = foreign.CatalogRead().LookupType(26) },
		func(d *catalog.Relation) { d.Columns[0].Usage.Type = nil },
		func(d *catalog.Relation) { d.Columns[0].Usage.Typmod = 1 },
		func(d *catalog.Relation) { d.Columns[0].DeclaredDimensions = 1 },
		func(d *catalog.Relation) { d.Columns[5].Usage.Collation.ID = 100 },
		func(d *catalog.Relation) { d.Columns[3].Usage.Collation.Derivation++ },
		func(d *catalog.Relation) { d.ColumnMeta.CatalogAttributes = d.ColumnMeta.CatalogAttributes[:7] },
		func(d *catalog.Relation) {
			d.ColumnMeta.CatalogAttributes = append(d.ColumnMeta.CatalogAttributes, d.ColumnMeta.CatalogAttributes[0])
		},
		func(d *catalog.Relation) { d.ColumnMeta.CatalogAttributes[3].Collation = 950 },
		func(d *catalog.Relation) { d.ColumnMeta.CatalogAttributes[0].NotNull = true },
		func(d *catalog.Relation) { d.ColumnMeta.CatalogAttributes[0].Dimensions = 1 },
	} {
		d := *source
		d.Columns = append([]catalog.Column(nil), source.Columns...)
		d.ColumnMeta.CatalogAttributes = append([]catalog.CatalogAttribute(nil), source.ColumnMeta.CatalogAttributes...)
		mutate(&d)
		s := catalog.CloneState(state)
		s.SetDescriptor(address, &d)
		x := in
		x.Catalog, x.Source = view.WithState(s), &d
		if rows, err := produce(x); rows != nil || err == nil {
			t.Fatal("forged shape admitted", rows, err)
		}
		if got, err := catalogrt.CaptureRows(x, produce); got != nil || err == nil {
			t.Fatal("forged shape published capture", got, err)
		}
	}
	// A matching catalog with a different database owner must not pair with
	// the original role snapshot, even though the source pointer is genuine.
	database, _ := view.Database(view.Owner().DatabaseAddress())
	badDB := *database
	badDB.Owner = e.Roles().LookupRef("pg_read_all_stats")
	s := catalog.CloneState(state)
	s.SetDescriptor(badDB.Address(), &badDB)
	x := in
	x.Catalog = view.WithState(s)
	if rows, err := produce(x); rows != nil || err == nil {
		t.Fatal("database owner mismatch admitted", rows, err)
	}
	if got, err := catalogrt.CaptureRows(x, produce); got != nil || err == nil {
		t.Fatal("database owner mismatch published capture", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	x = in
	x.Context, x.Source, x.Owner = ctx, nil, nil
	if rows, err := produce(x); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation precedence", rows, err)
	}
	if got, err := catalogrt.CaptureRows(x, produce); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled publication", got, err)
	}
	if e.CatalogRead().State() != state {
		t.Fatal("rejected capture published state")
	}
	for _, pretty := range []bool{false, true} {
		references := make(map[string]bool)
		definition := pgcatalog.BuiltinViewDefinition(pgcatalog.SeclabelsOID, pretty, func(n pgcatalog.BuiltinViewName) string {
			if n.IdentifierOnly {
				return n.Name
			}
			if n.Class == ids.ProcedureClassID && n.Signature == "" {
				t.Fatal("untyped routine reference", n)
			}
			switch n.Name {
			case "quote_ident":
				if n.Class != ids.ProcedureClassID || n.Signature != "25" {
					t.Fatal("quote_ident dependency", n)
				}
			case "pg_get_function_arguments", "pg_table_is_visible", "pg_function_is_visible", "pg_type_is_visible":
				if n.Class != ids.ProcedureClassID || n.Signature != "26" {
					t.Fatal("OID routine dependency", n)
				}
			case "text", "oid", "regclass", "char":
				if n.Class != ids.TypeClassID {
					t.Fatal("type dependency", n)
				}
			case "pg_largeobject":
				if n.Class != ids.RelationClassID || !n.RegclassLiteral {
					t.Fatal("largeobject class literal", n)
				}
			default:
				if strings.HasPrefix(n.Name, "pg_") && n.Class != ids.RelationClassID {
					t.Fatal("catalog dependency", n)
				}
			}
			references[n.Name] = true
			return n.Name
		})
		if strings.Count(definition, "UNION ALL") != 12 || strings.Contains(definition, "WHERE FALSE") || pgcatalog.BuiltinViewEvents(pgcatalog.SeclabelsOID, nil) != 0 {
			t.Fatal("genuine non-updatable definition", definition)
		}
		for _, name := range []string{"pg_seclabel", "pg_shseclabel", "pg_class", "pg_attribute", "pg_proc", "pg_type", "pg_largeobject_metadata", "pg_largeobject", "pg_language", "pg_namespace", "pg_event_trigger", "pg_publication", "pg_subscription", "pg_database", "pg_tablespace", "pg_authid", "quote_ident", "pg_get_function_arguments", "pg_table_is_visible", "pg_function_is_visible", "pg_type_is_visible", "text", "oid", "regclass"} {
			if !references[name] {
				t.Fatal("missing typed dependency", name)
			}
		}
	}
}

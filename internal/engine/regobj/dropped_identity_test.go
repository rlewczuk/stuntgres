package regobj

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// These payloads supply catalog facts, not executable SQL or identity answers.
type droppedIdentityRules []catalog.RuleFact

func (r droppedIdentityRules) Validate(catalog.Read, *catalog.Relation) error { return nil }
func (r droppedIdentityRules) Len() int                                       { return len(r) }
func (r droppedIdentityRules) OID(i int) ids.OID                              { return r[i].OID }
func (r droppedIdentityRules) RoutineRecipe(ids.OID, int) catalog.ViewPayload { return nil }
func (r droppedIdentityRules) Visit(visit func(catalog.RuleFact) error) error {
	for _, fact := range r {
		if err := visit(fact); err != nil {
			return err
		}
	}
	return nil
}

type droppedIdentityTriggers []catalog.TriggerFact

func (r droppedIdentityTriggers) Validate(catalog.Read, *catalog.Relation) error { return nil }
func (r droppedIdentityTriggers) Len() int                                       { return len(r) }
func (r droppedIdentityTriggers) OID(i int) ids.OID                              { return r[i].OID }
func (r droppedIdentityTriggers) Visit(visit func(catalog.TriggerFact) error) error {
	for _, fact := range r {
		if err := visit(fact); err != nil {
			return err
		}
	}
	return nil
}

func TestDroppedIdentityProjection(t *testing.T) {
	for _, temporary := range []bool{false, true} {
		label := "quoted"
		if temporary {
			label = "temporary"
		}
		t.Run(label, func(t *testing.T) {
			address := func(class, oid ids.OID) ids.ObjectAddress {
				return ids.ObjectAddress{ClassID: class, ObjectID: oid}
			}
			state := catalog.NewState(0)
			put := func(d catalog.Descriptor) { state.SetDescriptor(d.Address(), d) }
			var integer *sqltype.Descriptor
			for _, typ := range sqltype.Builtins() {
				put(&catalog.Type{SQLType: typ})
				if typ.ID() == sqltype.Int4 {
					integer = typ
				}
			}
			put(&catalog.Namespace{ObjectAddress: address(2615, ids.PGCatalogNamespaceOID), ObjectName: "pg_catalog"})
			ns := &catalog.Namespace{ObjectAddress: address(2615, 20001), ObjectName: "Odd S"}
			schema, qualified := "Odd S", `"Odd S"`
			if temporary {
				ns.ObjectName = "pg_temp_123"
				ns.Temporary = new(ids.Domain).Session(1)
				schema, qualified = "pg_temp", "pg_temp"
			}
			put(ns)
			relation := &catalog.Relation{
				ObjectAddress: address(1259, 20002), ObjectNamespace: ns.Address(), ObjectName: "T q",
				Columns: []catalog.Column{{Name: "c!", Usage: integer.Usage()}}, Attributes: []int32{3},
				ColumnMeta:  catalog.ColumnMetadata{Defaults: map[int32]catalog.ColumnDefault{3: {OID: 20005}}},
				Constraints: catalog.RelationConstraints{NotNull: []catalog.NotNullConstraint{{OID: 20006, Name: "nn!", Attribute: 3}}},
				Rules:       droppedIdentityRules{{OID: 20007, Name: "r!"}},
				Triggers:    droppedIdentityTriggers{{OID: 20008, Name: "tr!"}},
				Security:    catalog.RelationSecurity{Policies: []catalog.PolicyDefinition{{OID: 20009, Name: "pol!"}}},
			}
			put(relation)
			row, err := sqltype.Composite(20003, "T q", `"Odd S"."T q"`, 20004, nil)
			if err != nil {
				t.Fatal(err)
			}
			array, err := sqltype.Array(20004, "_T q", row)
			if err != nil {
				t.Fatal(err)
			}
			put(&catalog.Type{SQLType: row, ObjectNamespace: ns.Address(), OwningRelation: relation.Address(), Array: 20004})
			put(&catalog.Type{SQLType: array, ObjectNamespace: ns.Address()})
			index := &catalog.Relation{ObjectAddress: address(1259, 20010), ObjectNamespace: ns.Address(), ObjectName: "idx!", RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Parent: relation.Address()}}
			put(index)
			procedure := &catalog.Procedure{ObjectAddress: address(1255, 20011), ObjectNamespace: ns.Address(), ObjectName: "fn!", Signature: "23"}
			put(procedure)
			domain, err := sqltype.Domain(20012, "d!", `"Odd S"."d!"`, integer.Usage())
			if err != nil {
				t.Fatal(err)
			}
			put(&catalog.Type{SQLType: domain, ObjectNamespace: ns.Address(), DomainChecks: []catalog.DomainCheckConstraint{{OID: 20013, Name: "ck!"}}})
			view := catalog.NewRead(nil, state)
			path := []ids.ObjectAddress{ns.Address()}
			owner := qualified + `."T q"`
			cases := []struct {
				label                string
				a                    ids.ObjectAddress
				kind, identity, name string
				hasSchema, hasName   bool
				names, args          []string
			}{
				{"schema", ns.Address(), "schema", qualified, ns.Name(), false, true, []string{schema}, nil},
				{"table", relation.Address(), "table", owner, "T q", true, true, []string{schema, "T q"}, nil},
				{"column", ids.ObjectAddress{ClassID: 1259, ObjectID: 20002, SubID: 3}, "table column", owner + `."c!"`, "", true, false, []string{schema, "T q", "c!"}, nil},
				{"row_type", address(1247, 20003), "type", owner, "T q", true, true, []string{owner}, nil},
				{"array_type", address(1247, 20004), "type", owner + "[]", "_T q", true, true, []string{owner + "[]"}, nil},
				{"default", address(2604, 20005), "default value", "for " + owner + `."c!"`, "", true, false, []string{schema, "T q", "c!"}, nil},
				{"constraint", address(2606, 20006), "table constraint", `"nn!" on ` + owner, "", true, false, []string{schema, "T q", "nn!"}, nil},
				{"rule", address(2618, 20007), "rule", `"r!" on ` + owner, "", false, false, []string{schema, "T q", "r!"}, nil},
				{"trigger", address(2620, 20008), "trigger", `"tr!" on ` + owner, "", true, false, []string{schema, "T q", "tr!"}, nil},
				{"policy", address(3256, 20009), "policy", `"pol!" on ` + owner, "", true, false, []string{schema, "T q", "pol!"}, nil},
				{"index", index.Address(), "index", qualified + `."idx!"`, "idx!", true, true, []string{schema, "idx!"}, nil},
				{"routine", procedure.Address(), "function", catalog.Identifier(ns.Name()) + `."fn!"(integer)`, "", true, false, []string{schema, "fn!"}, []string{"integer"}},
				{"domain_constraint", address(2606, 20013), "domain constraint", `"ck!" on ` + qualified + `."d!"`, "", true, false, []string{qualified + `."d!"`}, []string{"ck!"}},
			}
			for _, tc := range cases {
				t.Run(tc.label, func(t *testing.T) {
					before, err := IdentifyAddress(view, authorization.Read{}, path, tc.a, ns.Temporary)
					if err != nil {
						t.Fatal(err)
					}
					got, err := DroppedAddress(view, authorization.Read{}, path, tc.a, ns.Temporary)
					if err != nil {
						t.Fatal(err)
					}
					wantSchema := ""
					if tc.hasSchema {
						wantSchema = schema
					}
					wantTemporary := temporary && tc.kind != "rule"
					if !got.Found || got.Address != tc.a || got.Original || got.Normal || got.Temporary != wantTemporary || got.Kind != tc.kind || got.Identity != tc.identity || got.Schema != wantSchema || got.Name != tc.name || got.HasSchema != tc.hasSchema || got.HasName != tc.hasName || !reflect.DeepEqual(got.Names, tc.names) || got.Args == nil || !reflect.DeepEqual(got.Args, append([]string{}, tc.args...)) {
						t.Fatalf("DroppedAddress = %+v; want kind=%q identity=%q schema=%q name=%q presence=%v/%v names=%q args=%q temporary=%v", got, tc.kind, tc.identity, wantSchema, tc.name, tc.hasSchema, tc.hasName, tc.names, tc.args, wantTemporary)
					}
					after, err := IdentifyAddress(view, authorization.Read{}, path, tc.a, ns.Temporary)
					if err != nil || !reflect.DeepEqual(before, after) {
						t.Fatalf("inquiry changed: before=%+v after=%+v err=%v", before, after, err)
					}
				})
			}
			inquiry, err := IdentifyAddress(view, authorization.Read{}, path, relation.Address(), ns.Temporary)
			if err != nil || inquiry.Schema != catalog.Identifier(ns.Name()) || inquiry.Name != `"T q"` {
				t.Fatalf("inquiry raw/quoted separation = %+v, %v", inquiry, err)
			}
			captured, err := DroppedAddress(view, authorization.Read{}, path, relation.Address(), ns.Temporary)
			if err != nil {
				t.Fatal(err)
			}
			state.RemoveDescriptor(relation.Address())
			state.RemoveDescriptor(ns.Address())
			if captured.Identity != owner || !reflect.DeepEqual(captured.Names, []string{schema, "T q"}) || captured.Name != "T q" {
				t.Fatalf("captured identity changed after removal: %+v", captured)
			}
			missing, err := DroppedAddress(view, authorization.Read{}, path, relation.Address(), ns.Temporary)
			if err != nil || !reflect.DeepEqual(missing, catalog.DroppedObject{}) {
				t.Fatalf("missing = %+v, %v; want zero record", missing, err)
			}
			missing, err = DroppedAddress(view, authorization.Read{}, path, address(2604, 99999), ns.Temporary)
			if err != nil || !reflect.DeepEqual(missing, catalog.DroppedObject{}) {
				t.Fatalf("missing owned address = %+v, %v", missing, err)
			}
			if _, err := DroppedAddress(view, authorization.Read{}, path, address(99999, 1), ns.Temporary); err == nil {
				t.Fatal("unsupported class did not retain inquiry error")
			}
		})
	}
}

func TestDroppedIdentityCast(t *testing.T) {
	state := catalog.NewState(0)
	put := func(d catalog.Descriptor) {
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	for _, typ := range sqltype.Builtins() {
		put(&catalog.Type{SQLType: typ})
	}
	ns := &catalog.Namespace{
		ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90001},
		ObjectName:    "Odd S",
	}
	put(ns)
	source, err := sqltype.Enum(90002, "Source!", `"Odd S"."Source!"`, []string{"red"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := sqltype.Enum(90003, "Target!", `"Odd S"."Target!"`, []string{"blue"})
	if err != nil {
		t.Fatal(err)
	}
	sourceType := &catalog.Type{SQLType: source, ObjectNamespace: ns.Address()}
	targetType := &catalog.Type{SQLType: target, ObjectNamespace: ns.Address()}
	put(sourceType)
	put(targetType)
	cast := &catalog.Cast{OID: 90004, Source: source.ID(), Target: target.ID(), ObjectRevision: 1}
	put(cast)
	view := catalog.NewRead(nil, state)
	path := []ids.ObjectAddress{ns.Address()}
	check := func(t *testing.T, view catalog.Read, path []ids.ObjectAddress, a ids.ObjectAddress, source, target, description string) (ObjectIdentity, catalog.DroppedObject) {
		t.Helper()
		identity := "(" + source + " AS " + target + ")"
		wantInquiry := ObjectIdentity{
			Kind: "cast", Identity: identity, Description: description,
			Names: []string{source}, Args: []string{target}, Found: true,
		}
		inquiry, err := IdentifyAddress(view, authorization.Read{}, path, a, ids.SessionToken{})
		if err != nil || !reflect.DeepEqual(inquiry, wantInquiry) {
			t.Fatalf("IdentifyAddress = %+v, %v; want %+v", inquiry, err, wantInquiry)
		}
		wantDropped := catalog.DroppedObject{
			Address: a, Kind: "cast", Identity: identity,
			Names: []string{source}, Args: []string{target}, Found: true,
		}
		dropped, err := DroppedAddress(view, authorization.Read{}, path, a, ids.SessionToken{})
		if err != nil || !reflect.DeepEqual(dropped, wantDropped) {
			t.Fatalf("DroppedAddress = %+v, %v; want %+v", dropped, err, wantDropped)
		}
		return inquiry, dropped
	}
	checkMissing := func(t *testing.T, view catalog.Read, a ids.ObjectAddress) {
		t.Helper()
		inquiry, err := IdentifyAddress(view, authorization.Read{}, path, a, ids.SessionToken{})
		if err != nil || !reflect.DeepEqual(inquiry, ObjectIdentity{Kind: "cast"}) {
			t.Fatalf("missing IdentifyAddress = %+v, %v", inquiry, err)
		}
		dropped, err := DroppedAddress(view, authorization.Read{}, path, a, ids.SessionToken{})
		if err != nil || !reflect.DeepEqual(dropped, catalog.DroppedObject{}) {
			t.Fatalf("missing DroppedAddress = %+v, %v", dropped, err)
		}
	}
	t.Run("qualified_endpoints_and_visible_description", func(t *testing.T) {
		check(t, view, path, cast.Address(), `"Odd S"."Source!"`, `"Odd S"."Target!"`, `cast from "Source!" to "Target!"`)
		check(t, view, nil, cast.Address(), `"Odd S"."Source!"`, `"Odd S"."Target!"`, `cast from "Odd S"."Source!" to "Odd S"."Target!"`)
	})
	t.Run("fixed_builtin_without_descriptor", func(t *testing.T) {
		a := ids.ObjectAddress{ClassID: ids.CastClassID, ObjectID: 20052}
		if view.Get(a) != nil {
			t.Fatal("builtin fallback fixture unexpectedly has a cast descriptor")
		}
		check(t, view, path, a, "integer", "boolean", "cast from integer to boolean")
	})
	t.Run("unknown_address", func(t *testing.T) {
		checkMissing(t, view, ids.ObjectAddress{ClassID: ids.CastClassID, ObjectID: 99999})
	})
	t.Run("immutable_pre_removal_reader", func(t *testing.T) {
		inquiry, dropped := check(t, view, path, cast.Address(), `"Odd S"."Source!"`, `"Odd S"."Target!"`, `cast from "Source!" to "Target!"`)
		next := catalog.CloneState(state)
		next.RemoveDescriptor(cast.Address())
		removed := catalog.NewRead(nil, next)
		checkMissing(t, removed, cast.Address())
		if removed.Get(cast.Address()) != nil {
			t.Fatal("cast descriptor survived actual removal")
		}
		replacement := *cast
		replacement.Source, replacement.Target = sqltype.Int4, sqltype.Bool
		replacement.ObjectRevision = cast.Revision() + 1
		next.SetDescriptor(replacement.Address(), &replacement)
		current := catalog.NewRead(nil, next)
		check(t, current, path, cast.Address(), "integer", "boolean", "cast from integer to boolean")
		if view.Get(cast.Address()) != cast || view.Get(sourceType.Address()) != sourceType || view.Get(targetType.Address()) != targetType {
			t.Fatal("retained reader lost original cast or endpoint descriptor ownership")
		}
		if current.Get(cast.Address()) != &replacement || current.Get(sourceType.Address()) != sourceType || current.Get(targetType.Address()) != targetType {
			t.Fatal("replacement changed endpoint ownership or did not replace cast")
		}
		againInquiry, againDropped := check(t, view, path, cast.Address(), `"Odd S"."Source!"`, `"Odd S"."Target!"`, `cast from "Source!" to "Target!"`)
		if !reflect.DeepEqual(inquiry, againInquiry) || !reflect.DeepEqual(dropped, againDropped) {
			t.Fatalf("retained results changed: inquiry=%+v dropped=%+v", inquiry, dropped)
		}
	})
}

func TestObjectIdentityTemporarySessionOwnership(t *testing.T) {
	state := catalog.NewState(0)
	address := func(class, oid ids.OID) ids.ObjectAddress {
		return ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	put := func(d catalog.Descriptor) { state.SetDescriptor(d.Address(), d) }
	domain := new(ids.Domain)
	owner, peer := domain.Session(1), domain.Session(2)
	ns := &catalog.Namespace{ObjectAddress: address(2615, 29001), ObjectName: "pg_temp_123", Temporary: owner}
	toast := &catalog.Namespace{ObjectAddress: address(2615, 29002), ObjectName: "pg_toast_temp_123", Temporary: owner, TemporaryToast: true}
	put(ns)
	put(toast)
	for _, typ := range sqltype.Builtins() {
		put(&catalog.Type{SQLType: typ})
	}
	row, err := sqltype.Composite(29004, "T q", `pg_temp_123."T q"`, 29005, nil)
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(29005, "_T q", row)
	if err != nil {
		t.Fatal(err)
	}
	relation := &catalog.Relation{
		ObjectAddress: address(1259, 29003), ObjectNamespace: ns.Address(), ObjectName: "T q",
		Columns: []catalog.Column{{Name: "c!", Usage: row.Usage()}}, Attributes: []int32{3},
		Triggers: droppedIdentityTriggers{{OID: 29006, Name: "tr!"}},
	}
	put(relation)
	put(&catalog.Type{SQLType: row, ObjectNamespace: ns.Address(), OwningRelation: relation.Address(), Array: 29005})
	put(&catalog.Type{SQLType: array, ObjectNamespace: ns.Address()})
	routine := &catalog.Procedure{ObjectAddress: address(1255, 29007), ObjectNamespace: ns.Address(), ObjectName: "fn!", Signature: "23"}
	operator := &catalog.Operator{ObjectAddress: address(2617, 29008), ObjectNamespace: ns.Address(), ObjectName: "===", Left: 23, Right: 23}
	put(routine)
	put(operator)
	view := catalog.NewRead(nil, state)
	for _, tc := range []struct {
		name   string
		token  ids.SessionToken
		schema string
	}{{"owner", owner, "pg_temp"}, {"peer", peer, ns.Name()}, {"detached", ids.SessionToken{}, ns.Name()}} {
		t.Run(tc.name, func(t *testing.T) {
			qualified := catalog.Identifier(tc.schema)
			for _, object := range []struct {
				address          ids.ObjectAddress
				schema, identity string
			}{
				{relation.Address(), tc.schema, qualified + `."T q"`},
				{ids.ObjectAddress{ClassID: 1259, ObjectID: relation.Address().ObjectID, SubID: 3}, tc.schema, qualified + `."T q"."c!"`},
				{address(1247, 29004), tc.schema, qualified + `."T q"`},
				{address(1247, 29005), tc.schema, qualified + `."T q"[]`},
				{address(2620, 29006), "", `"tr!" on ` + qualified + `."T q"`},
				{ns.Address(), "", qualified},
				{toast.Address(), "", toast.Name()},
				{routine.Address(), tc.schema, `pg_temp_123."fn!"(integer)`},
				{operator.Address(), tc.schema, `pg_temp_123.===(integer,integer)`},
			} {
				got, err := IdentifyAddress(view, authorization.Read{}, nil, object.address, tc.token)
				if err != nil || !got.Found || got.RawSchema != object.schema || got.Identity != object.identity {
					t.Fatalf("address %v: got %+v, %v; want schema=%q identity=%q", object.address, got, err, object.schema, object.identity)
				}
			}
			dropped, err := DroppedAddress(view, authorization.Read{}, nil, relation.Address(), tc.token)
			if err != nil || dropped.Schema != tc.schema || dropped.Temporary != (tc.token == owner) || dropped.Identity != qualified+`."T q"` {
				t.Fatalf("dropped ownership: %+v, %v", dropped, err)
			}
		})
	}
	if ns.Name() != "pg_temp_123" || toast.Name() != "pg_toast_temp_123" {
		t.Fatal("identity rendering mutated catalog namespace descriptors")
	}
}

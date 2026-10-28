package control

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func fdwUnitAddress(class, oid ids.OID) ids.ObjectAddress {
	return ids.ObjectAddress{ClassID: class, ObjectID: oid}
}
func fdwUnitSource() (Source, *catalog.ForeignObject, *catalog.ForeignObject, *catalog.ForeignObject) {
	domain := new(authorization.Domain)
	roles := authorization.Seed(domain)
	owner := domain.Principal(10)
	state := catalog.NewState(1)
	// Full capture always inspects the initialized namespace/collation,
	// conversion, and dictionary manifests, even when the selected user graph
	// is SQL/MED. Use the real seed declarations and capabilities, not omitted
	// seeds that would manufacture bootstrap DROP/repair output.
	roleView := roles.Read(owner)
	var aclTypes []*sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		element := typ
		if typ.IsArray() {
			element = typ.Element()
		}
		if element.ID() == sqltype.ACLItem || element.ID() == sqltype.Int4 {
			aclTypes = append(aclTypes, typ)
		}
	}
	catalog.InstallBuiltinTypes(state, aclTypes, owner)
	for _, seed := range []struct {
		oid  ids.OID
		name string
	}{{ids.PGCatalogNamespaceOID, "pg_catalog"}, {ids.PGToastNamespaceOID, "pg_toast"}, {ids.PublicNamespaceOID, "public"}, {13229, "information_schema"}} {
		namespaceOwner := owner
		if seed.oid == ids.PublicNamespaceOID {
			namespaceOwner = roleView.LookupRef("pg_database_owner")
		}
		namespace := &catalog.Namespace{ObjectAddress: fdwUnitAddress(ids.NamespaceClassID, seed.oid), ObjectName: seed.name, ObjectRevision: 1, Owner: namespaceOwner, Comment: catalog.NamespaceCommentBaseline(seed.oid)}
		if seed.oid != ids.PGToastNamespaceOID {
			namespace.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
				{Grantor: namespaceOwner, Grantee: authorization.ACLGrantee{Role: namespaceOwner}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege},
				{Grantor: namespaceOwner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
			}}
		}
		state.SetDescriptor(namespace.Address(), namespace)
		state.BindName(catalog.DescriptorNameKey(namespace), namespace.Address())
	}
	for _, seed := range catalog.BuiltinCollations {
		collation := seed
		collation.Owner = owner
		state.SetDescriptor(collation.Address(), &collation)
		state.BindName(catalog.DescriptorNameKey(&collation), collation.Address())
	}
	procedures := typedef.BuiltinConversionProcedures(owner)
	for i := range procedures {
		procedure := &procedures[i]
		state.SetDescriptor(procedure.Address(), procedure)
		state.BindName(catalog.DescriptorNameKey(procedure), procedure.Address())
	}
	for _, seed := range catalog.BuiltinConversions {
		conversion := seed
		conversion.Owner = owner
		conversion.ObjectNamespace = fdwUnitAddress(ids.NamespaceClassID, ids.PGCatalogNamespaceOID)
		conversion.ObjectRevision = 1
		state.SetDescriptor(conversion.Address(), &conversion)
		state.BindName(catalog.DescriptorNameKey(&conversion), conversion.Address())
	}
	dictionaryProcedures := routine.BuiltinProcedures(owner)
	for i := range dictionaryProcedures {
		procedure := &dictionaryProcedures[i]
		if procedure.Address().ObjectID != 3725 && procedure.Address().ObjectID != 3726 {
			continue
		}
		state.SetDescriptor(procedure.Address(), procedure)
		state.BindName(catalog.DescriptorNameKey(procedure), procedure.Address())
	}
	catalog.InstallBuiltinDictionaries(state, owner, functions.DefaultSimpleDictionary)
	wrapper := &catalog.ForeignObject{ObjectAddress: fdwUnitAddress(ids.ForeignWrapperClassID, 90003), ObjectRevision: 1, ObjectName: `w"quoted`, Owner: owner, Options: []catalog.ForeignOption{{Name: "tag", Value: "first"}, {Name: "empty", Value: ""}}}
	empty := ""
	server := &catalog.ForeignObject{ObjectAddress: fdwUnitAddress(ids.ForeignServerClassID, 90002), ObjectRevision: 1, ObjectName: "s", Owner: owner, Wrapper: wrapper.Address(), ServerType: &empty, Options: []catalog.ForeignOption{{Name: "path", Value: "a'b\\c\nnext"}}}
	mapping := &catalog.ForeignObject{ObjectAddress: fdwUnitAddress(ids.ForeignMappingClassID, 90001), ObjectRevision: 1, Owner: owner, MappedRole: owner, Server: server.Address(), Options: []catalog.ForeignOption{{Name: "secret", Value: "synthetic O'Brien\\tail\nnext"}, {Name: "empty", Value: ""}}}
	mapping.ObjectName = catalog.ForeignMappingName(server.Address(), mapping.MappedRole)
	for _, d := range []*catalog.ForeignObject{wrapper, server, mapping} {
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	view := catalog.NewRead(&catalog.Owner{Principals: domain, Providers: map[ids.OID]catalog.Provider{ids.ProcedureClassID: stored.ProcedureProvider{}}}, state)
	return Source{State: state, CatalogView: view, OriginalCatalog: view, OriginalRoles: roles.Read(owner), DomainRoles: roles.Read(owner), Bootstrap: owner}, wrapper, server, mapping
}

func fdwUnitValidator(source *Source, w *catalog.ForeignObject) *catalog.Procedure {
	ns := &catalog.Namespace{ObjectAddress: fdwUnitAddress(ids.NamespaceClassID, 90100), ObjectName: `cap"schema`, ObjectRevision: 1, Owner: source.Bootstrap}
	name := catalog.ForeignFunctionName{Schema: ns.Name(), Name: `actual"validator`}
	validate := func(context.Context, catalog.ForeignObjectKind, []catalog.ForeignOption) error {
		return errors.New("dump must not execute option validators")
	}
	proc := &catalog.Procedure{ObjectAddress: fdwUnitAddress(ids.ProcedureClassID, 90101), ObjectNamespace: ns.Address(), ObjectName: name.Name, ObjectRevision: 1, Owner: source.Bootstrap, Signature: catalog.ForeignValidatorSignature, RoutineKind: catalog.FunctionRoutine, Result: 2278, Foreign: &catalog.ForeignProcedure{Name: name, Validator: validate}}
	for _, d := range []catalog.Descriptor{ns, proc} {
		source.State.SetDescriptor(d.Address(), d)
		source.State.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	source.Foreign.Install(nil, []fdw.ValidatorDefinition{{Name: name, Validate: validate}}, nil, nil)
	source.Foreign.AddNamespace(ns.Address(), ns.Name())
	w.Validator = proc.Address()
	return proc
}
func fdwUnitDump(source Source, dst io.Writer) error {
	if err := CheckPortable(source.State); err != nil {
		return err
	}
	// Follow the real locked capture preparation: generation recipes and ACL
	// formatting use the original immutable read; constraint replay supplies
	// the declaration read and every later rename/ordering phase.
	ctx := context.Background()
	var err error
	source.RecipePlan, err = table.CapturedGenerationReplayPlan(source.OriginalCatalog)
	if err != nil {
		return err
	}
	source.ACLOutput = regobj.ACLItemOutput{Owner: source.OriginalCatalog.Owner(), Roles: source.OriginalRoles, Lossless: true}
	ctx = regobj.ACLItemOutputContext(ctx, source.ACLOutput)
	if err := PreflightACLItems(ctx, source.OriginalCatalog, source.Relations, source.Frontier, source.ACLOutput); err != nil {
		return err
	}
	source.CatalogView, source.ConstraintReplay = partition.ConstraintDumpCatalog(Resolver{}, source.OriginalCatalog)
	capture, err := Collect(ctx, source)
	if err != nil {
		return err
	}
	return Dump(ctx, capture, dst)
}

func TestFDWDumpDefinitions(t *testing.T) {
	source, w, s, m := fdwUnitSource()
	oldW, oldS, oldM := *w, *s, *m
	oldW.Options = append([]catalog.ForeignOption(nil), w.Options...)
	oldS.Options = append([]catalog.ForeignOption(nil), s.Options...)
	oldM.Options = append([]catalog.ForeignOption(nil), m.Options...)
	var out bytes.Buffer
	if err := fdwUnitDump(source, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`CREATE FOREIGN DATA WRAPPER "w""quoted" NO HANDLER NO VALIDATOR OPTIONS ("tag" E'first', "empty" E'');`,
		`CREATE SERVER "s" TYPE E'' FOREIGN DATA WRAPPER "w""quoted"`,
		`CREATE USER MAPPING FOR "postgres" SERVER "s"`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("declaration missing %q (dump withheld)", want)
		}
	}
	if strings.Contains(out.String(), " VERSION ") {
		t.Fatal("absent server VERSION became an empty value")
	}
	if !strings.Contains(out.String(), "\"secret\" E'synthetic O''Brien\\\\tail\nnext', \"empty\" E''") {
		t.Fatal("ordered sensitive option literal was not faithfully escaped")
	}
	if !reflect.DeepEqual(*w, oldW) || !reflect.DeepEqual(*s, oldS) || !reflect.DeepEqual(*m, oldM) {
		t.Fatal("rendering mutated captured descriptors")
	}
	// PUBLIC has no principal; named mapping ownership is not an invented
	// independent owner-transfer target.
	public := *m
	public.MappedRole = authorization.PrincipalRef{}
	public.Owner = authorization.PrincipalRef{}
	public.ObjectName = catalog.ForeignMappingName(s.Address(), public.MappedRole)
	source.State.UnbindName(catalog.DescriptorNameKey(m))
	source.State.SetDescriptor(public.Address(), &public)
	source.State.BindName(catalog.DescriptorNameKey(&public), public.Address())
	out.Reset()
	if err := fdwUnitDump(source, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `CREATE USER MAPPING FOR PUBLIC SERVER "s"`) {
		t.Fatal("PUBLIC mapping declaration missing")
	}
	empty := ""
	s.ServerType = nil
	s.Version = &empty
	out.Reset()
	if err := fdwUnitDump(source, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), " TYPE ") || !strings.Contains(out.String(), " VERSION E'' ") {
		t.Fatal("server nil TYPE/empty VERSION distinction lost")
	}
	source, w, _, _ = fdwUnitSource()
	validator := fdwUnitValidator(&source, w)
	out.Reset()
	if err := fdwUnitDump(source, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `NO HANDLER VALIDATOR "cap""schema"."actual""validator"`) {
		t.Fatal("actual qualified validator identity was not emitted")
	}
	if strings.Contains(out.String(), "CREATE FUNCTION") {
		t.Fatal("dump serialized executable capability")
	}
	predecessor, err := (Resolver{}).Predecessor(source.CatalogView, validator.Address(), w.Address())
	if err != nil || predecessor != 0 {
		t.Fatalf("configured validator prerequisite=%d,%v", predecessor, err)
	}
	invalid := *validator
	invalid.Signature = catalog.ForeignHandlerSignature
	source.State.SetDescriptor(invalid.Address(), &invalid)
	if _, err := (Resolver{}).Predecessor(source.CatalogView, invalid.Address(), w.Address()); err == nil {
		t.Fatal("incompatible installed validator accepted as prerequisite")
	}
	source, _, server, _ := fdwUnitSource()
	ns := &catalog.Namespace{ObjectAddress: fdwUnitAddress(ids.NamespaceClassID, 93000), ObjectName: "unit", ObjectRevision: 1, Owner: source.Bootstrap}
	source.State.SetDescriptor(ns.Address(), ns)
	source.State.BindName(catalog.DescriptorNameKey(ns), ns.Address())
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
			break
		}
	}
	relation := &catalog.Relation{ObjectAddress: fdwUnitAddress(ids.RelationClassID, 93001), ObjectNamespace: ns.Address(), ObjectName: "t", ObjectRevision: 1, Owner: source.Bootstrap, RelationKind: catalog.ForeignRelation, Columns: []catalog.Column{{Name: "id", Usage: integer.Usage(), Nullable: true}, {Name: "note", Usage: integer.Usage(), Nullable: true}}, Attributes: []int32{1, 3}, Comment: "table's comment", Foreign: &catalog.ForeignTableDefinition{Server: server.Address(), Columns: map[int32][]catalog.ForeignOption{3: {{Name: "column_name", Value: "actual_note"}}}}}
	statement, err := fdw.DumpTable(context.Background(), source.CatalogView, source.DomainRoles, relation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statement.SQL, `"note" INTEGER OPTIONS ("column_name" E'actual_note')`) {
		t.Fatal("column options were not read by actual attribute number")
	}
	if !strings.Contains(statement.FollowingSQL, `COMMENT ON FOREIGN TABLE "unit"."t" IS E'table''s comment';`) || strings.Contains(statement.SQL, "COMMENT ON") {
		t.Fatal("foreign comment declaration was not escaped")
	}
	if relation.Foreign.Columns[3][0].Value != "actual_note" || relation.Attributes[1] != 3 || relation.Comment != "table's comment" {
		t.Fatal("foreign renderer mutated input")
	}
}

func TestFDWDumpDependencies(t *testing.T) {
	source, w, s, m := fdwUnitSource()
	for _, d := range []*catalog.ForeignObject{w, s, m} {
		got, err := (Resolver{}).Predecessor(source.CatalogView, d.Address(), ids.ObjectAddress{})
		if err != nil || got != d.Address().ObjectID {
			t.Errorf("predecessor(%s)=%d,%v want=%d", d.Name(), got, err, d.Address().ObjectID)
		}
	}
	var out bytes.Buffer
	if err := fdwUnitDump(source, &out); err != nil {
		t.Fatal(err)
	}
	wrapper := strings.Index(out.String(), "CREATE FOREIGN DATA WRAPPER")
	server := strings.Index(out.String(), "CREATE SERVER")
	mapping := strings.Index(out.String(), "CREATE USER MAPPING")
	if wrapper < 0 || server <= wrapper || mapping <= server {
		t.Fatal("reverse allocation order defeated dependency ordering")
	}
}

func TestFDWReplacementRemap(t *testing.T) {
	source, w, s, m := fdwUnitSource()
	handler, validator := fdwUnitAddress(ids.ProcedureClassID, 91001), fdwUnitAddress(ids.ProcedureClassID, 91002)
	w.Handler, w.Validator = handler, validator
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{}
	for _, a := range []ids.ObjectAddress{w.Address(), s.Address(), m.Address(), handler, validator} {
		mapping[a] = fdwUnitAddress(a.ClassID, a.ObjectID+1000)
	}
	for _, d := range []*catalog.ForeignObject{w, s, m} {
		old := *d
		old.Options = append([]catalog.ForeignOption(nil), d.Options...)
		got, err := remapReplacementDescriptor(source.CatalogView, mapping, d.Address(), mapping[d.Address()], d)
		if err != nil {
			t.Fatal(err)
		}
		n := got.(*catalog.ForeignObject)
		if n.Address() != mapping[d.Address()] {
			t.Errorf("own address not moved: %v", n.Address())
		}
		move := func(a ids.ObjectAddress) ids.ObjectAddress {
			if target, ok := mapping[a]; ok {
				return target
			}
			return a
		}
		if n.Wrapper != move(d.Wrapper) || n.Server != move(d.Server) || n.Handler != move(d.Handler) || n.Validator != move(d.Validator) {
			t.Error("SQL/MED reference retained source address")
		}
		if n.Kind() == ids.ForeignMappingObject && n.Name() != catalog.ForeignMappingName(n.Server, n.MappedRole) {
			t.Error("mapping key not rebuilt after server remap")
		}
		if !reflect.DeepEqual(*d, old) {
			t.Error("address remap mutated source descriptor")
		}
	}
	table := &catalog.Relation{ObjectAddress: fdwUnitAddress(ids.RelationClassID, 92000), ObjectRevision: 1, RelationKind: catalog.ForeignRelation, Foreign: &catalog.ForeignTableDefinition{Server: s.Address(), Options: []catalog.ForeignOption{{Name: "table_name", Value: "real"}}, Columns: map[int32][]catalog.ForeignOption{3: {{Name: "column_name", Value: "note"}}}}}
	got, err := remapReplacementRelation(source.CatalogView, mapping, table.Address(), table.Address(), table)
	if err != nil {
		t.Fatal(err)
	}
	n := got.(*catalog.Relation)
	if n.Foreign.Server != mapping[s.Address()] || table.Foreign.Server != s.Address() {
		t.Fatal("table-server remap absent or mutated original")
	}
	if !reflect.DeepEqual(n.Foreign.Columns, table.Foreign.Columns) || !reflect.DeepEqual(n.Foreign.Options, table.Foreign.Options) {
		t.Fatal("column attnums or options changed")
	}
	if table.Foreign.Columns[3][0] != (catalog.ForeignOption{Name: "column_name", Value: "note"}) || table.Foreign.Options[0] != (catalog.ForeignOption{Name: "table_name", Value: "real"}) {
		t.Fatal("remap mutated original option bytes")
	}
	// Both remap orders must converge to the same server/principal key.
	targetDomain := new(authorization.Domain)
	remap := catalog.PrincipalRemap{Principal: func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		return targetDomain.Principal(ref.ID() + 100), nil
	}}
	remap.SeededOwner = func(ref authorization.PrincipalRef, _ bool) (authorization.PrincipalRef, error) {
		return remap.Principal(ref)
	}
	for _, public := range []bool{false, true} {
		for _, principalFirst := range []bool{false, true} {
			state := catalog.CloneState(source.State)
			wantPrincipal := targetDomain.Principal(m.MappedRole.ID() + 100)
			if public {
				copied := *m
				copied.MappedRole = authorization.PrincipalRef{}
				copied.Owner = authorization.PrincipalRef{}
				copied.ObjectName = catalog.ForeignMappingName(copied.Server, copied.MappedRole)
				state.UnbindName(catalog.DescriptorNameKey(m))
				state.SetDescriptor(copied.Address(), &copied)
				state.BindName(catalog.DescriptorNameKey(&copied), copied.Address())
				wantPrincipal = authorization.PrincipalRef{}
			}
			applyAddress := func() {
				current := catalog.NewRead(nil, state)
				if err := RemapReplacementIdentities(current, state, nil, mapping, nil, nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			if principalFirst {
				if err := catalog.RemapPrincipals(state, remap); err != nil {
					t.Fatal(err)
				}
				applyAddress()
			} else {
				applyAddress()
				if err := catalog.RemapPrincipals(state, remap); err != nil {
					t.Fatal(err)
				}
			}
			n, ok := state.Foreign(mapping[m.Address()])
			if !ok {
				t.Fatal("remapped mapping missing")
			}
			if n.MappedRole != wantPrincipal || n.Server != mapping[s.Address()] || n.Name() != catalog.ForeignMappingName(n.Server, n.MappedRole) {
				t.Fatal("principal/address order changed mapping identity")
			}
			if err := (catalog.ForeignCatalogProvider{Class: ids.ForeignMappingClassID}).Validate(catalog.NewRead(nil, state), n); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestFDWDumpACLPreflight(t *testing.T) {
	for _, kind := range []string{"absent", "wrapper", "server", "mapping"} {
		t.Run(kind, func(t *testing.T) {
			source, w, s, m := fdwUnitSource()
			switch kind {
			case "wrapper":
				w.ACL.Explicit = true
			case "server":
				s.ACL.Explicit = true
			case "mapping":
				m.MappedRole = authorization.PrincipalRef{}
				m.Owner = authorization.PrincipalRef{}
				source.State.UnbindName(catalog.DescriptorNameKey(m))
				m.ObjectName = catalog.ForeignMappingName(m.Server, m.MappedRole)
				source.State.BindName(catalog.DescriptorNameKey(m), m.Address())
			}
			var out bytes.Buffer
			out.WriteString("sentinel")
			err := fdwUnitDump(source, &out)
			if err != nil {
				t.Fatal("representable owner/PUBLIC/ACL graph rejected", err)
			}
			if kind == "wrapper" || kind == "server" {
				if !strings.Contains(out.String(), `=pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[]`) {
					t.Fatal("explicit-empty foreign ACL repair missing")
				}
			}
		})
	}
	for _, column := range []bool{false, true} {
		source, _, s, _ := fdwUnitSource()
		ns := &catalog.Namespace{ObjectAddress: fdwUnitAddress(ids.NamespaceClassID, 95000), ObjectName: "foreign_acl", ObjectRevision: 1, Owner: source.Bootstrap}
		var integer *sqltype.Descriptor
		for _, typ := range sqltype.Builtins() {
			if typ.ID() == sqltype.Int4 {
				integer = typ
				break
			}
		}
		if integer == nil {
			t.Fatal("builtin int4 missing")
		}
		relation := &catalog.Relation{ObjectAddress: fdwUnitAddress(ids.RelationClassID, 95001), ObjectNamespace: ns.Address(), ObjectName: "f", ObjectRevision: 1, Owner: source.Bootstrap, RelationKind: catalog.ForeignRelation, Columns: []catalog.Column{{Name: "id", Usage: integer.Usage(), Nullable: true, NullabilityKnown: true}}, Attributes: []int32{1}, Index: map[string]int{"id": 0}, Foreign: &catalog.ForeignTableDefinition{Server: s.Address()}}
		if column {
			relation.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true}}
		} else {
			relation.Security.ACL.Explicit = true
		}
		for _, d := range []catalog.Descriptor{ns, relation} {
			source.State.SetDescriptor(d.Address(), d)
			source.State.BindName(catalog.DescriptorNameKey(d), d.Address())
		}
		var out bytes.Buffer
		out.WriteString("sentinel")
		err := fdwUnitDump(source, &out)
		if err != nil {
			t.Fatal("representable relation/column ACL rejected", err)
		}
		catalogName, aclName := "pg_class", "relacl"
		if column {
			catalogName, aclName = "pg_attribute", "attacl"
		}
		if !strings.Contains(out.String(), "UPDATE pg_catalog."+catalogName+" SET "+aclName+"=pg_catalog.acldefault(") {
			t.Fatal("explicit-empty relation/column ACL repair missing")
		}
	}
}

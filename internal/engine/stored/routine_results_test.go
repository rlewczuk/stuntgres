package stored

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func resultProviderFixture(t *testing.T) (catalog.Read, *catalog.Procedure) {
	t.Helper()
	read, relation, integer, _ := routineCaptureFixture(t)
	principals := new(authorization.Domain)
	roles := authorization.Seed(principals)
	owner, ok := roles.Read(principals.Principal(authorization.BootstrapPrincipalID)).Lookup("postgres")
	if !ok {
		t.Fatal("missing owner")
	}
	read.Owner().Principals = principals
	database := &catalog.Database{ObjectAddress: catalog.CurrentDatabaseAddress, ObjectRevision: 1, ObjectName: "capture", Owner: owner.Ref}
	read.State().SetDescriptor(database.Address(), database)
	read.State().BindName(catalog.DescriptorNameKey(database), database.Address())
	language := &catalog.Language{ObjectAddress: ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: 14}, ObjectRevision: 1, ObjectName: "sql", Owner: owner.Ref, Trusted: true, Validator: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 2248}}
	read.State().SetDescriptor(language.Address(), language)
	read.State().BindName(catalog.DescriptorNameKey(language), language.Address())
	if err := (catalog.LanguageProvider{}).Validate(read, language); err != nil {
		t.Fatal(err)
	}
	definition := &proc.Definition{Name: "outputs"}
	if err := definition.SetFormals([]proc.Formal{{Mode: "i", Name: "x", Usage: integer.Usage()}, {Mode: "o", Name: "first", Usage: integer.Usage()}, {Mode: "o", Name: "second", Usage: integer.Usage()}}); err != nil {
		t.Fatal(err)
	}
	shape, err := definition.OutputTuple()
	if err != nil {
		t.Fatal(err)
	}
	definition.Result = shape.Usage()
	d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 98000}, ObjectRevision: 1, ObjectNamespace: relation.Namespace(), ObjectName: definition.Name, Signature: "23", RoutineKind: catalog.FunctionRoutine, Result: 2249, Volatility: expr.Volatile, Parallel: 'u', Cost: 100, SQL: definition, Language: language.Address(), Owner: owner.Ref}
	read.State().SetDescriptor(d.Address(), d)
	return read, d
}

func TestSQLRoutineResultShapeValidation(t *testing.T) {
	read, d := resultProviderFixture(t)
	if err := validateSQLProcedure(read, d); err != nil {
		t.Fatal("correct formal-derived shape", err)
	}
	original := d.SQL.Result.Type
	for _, kind := range []string{"name", "order", "type", "attribute", "width", "unshaped", "foreign child", "set policy", "projection", "procedure policy"} {
		t.Run(kind, func(t *testing.T) {
			next := *d
			definition := *d.SQL
			next.SQL = &definition
			members := make([]sqltype.CompositeMember, original.CompositeLen())
			for i := range members {
				members[i] = original.CompositeMember(i)
			}
			switch kind {
			case "name":
				members[0].Name = "other"
			case "order":
				members[0].Name, members[1].Name = members[1].Name, members[0].Name
			case "type":
				boolean, _ := read.LookupType(sqltype.Bool)
				members[1].Usage = boolean.Usage()
			case "attribute":
				members[1].Attribute = 3
			case "width":
				members = members[:1]
			case "unshaped":
				generic, _ := read.LookupType(2249)
				definition.Result = generic.Usage()
			case "foreign child":
				leaf := *members[0].Usage.Type
				members[0].Usage.Type = &leaf
				definition.Formals = append([]proc.Formal(nil), definition.Formals...)
				definition.Formals[1].Usage = members[0].Usage
				members[1].Usage = members[0].Usage
				definition.Formals[2].Usage = members[0].Usage
			case "set policy":
				definition.TableResult = true
			case "projection":
				definition.OutputFormals = []int{1, 999}
			case "procedure policy":
				definition.Procedure = true
			}
			if kind != "unshaped" {
				shape, err := sqltype.RecordShape(members)
				if err != nil {
					t.Fatal(err)
				}
				definition.Result = shape.Usage()
			}
			if err := validateSQLProcedure(read, &next); err == nil {
				t.Fatal("corrupt result admitted", kind)
			}
			if d.SQL.Result.Type != original || d.SQL.Formals[1].Usage != original.CompositeMember(0).Usage {
				t.Fatal("validation mutated original")
			}
		})
	}
	integer, _ := read.LookupType(sqltype.Int4)
	named, err := sqltype.Composite(98001, "named", "capture.named", 0, []sqltype.CompositeMember{{Attribute: 1, Name: "x", Usage: integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	descriptor := &catalog.Type{SQLType: named, ObjectNamespace: d.Namespace(), ObjectRevision: 1}
	read.State().SetDescriptor(descriptor.Address(), descriptor)
	next := *d
	definition := &proc.Definition{Name: "named", Result: named.Usage()}
	if err := definition.SetFormals(nil); err != nil {
		t.Fatal(err)
	}
	next.SQL, next.Result, next.Signature = definition, 98001, ""
	if err := validateSQLProcedure(read, &next); err != nil {
		t.Fatal("catalog-owned named result", err)
	}
	foreign := *named
	definition.Result = foreign.Usage()
	if err := validateSQLProcedure(read, &next); err == nil {
		t.Fatal("same-ID foreign named result admitted")
	}
}

func TestSQLRoutineResultRemap(t *testing.T) {
	read, d := resultProviderFixture(t)
	integer, _ := read.LookupType(sqltype.Int4)
	types := make([]*sqltype.Descriptor, 3)
	for i := range types {
		typ, err := sqltype.Domain(sqltype.ID(98100+i), "leaf", "capture.leaf", integer.Usage())
		if err != nil {
			t.Fatal(err)
		}
		types[i] = typ
		descriptor := &catalog.Type{SQLType: typ, ObjectNamespace: d.Namespace(), ObjectRevision: 1}
		read.State().SetDescriptor(descriptor.Address(), descriptor)
	}
	definition := &proc.Definition{Name: d.Name()}
	if err := definition.SetFormals([]proc.Formal{{Mode: "i", Name: "x", Usage: types[0].Usage()}, {Mode: "o", Name: "first", Usage: types[0].Usage()}, {Mode: "o", Name: "second", Usage: integer.Usage()}}); err != nil {
		t.Fatal(err)
	}
	shape, err := definition.OutputTuple()
	if err != nil {
		t.Fatal(err)
	}
	definition.Result = shape.Usage()
	source := *d
	source.SQL, source.Signature = definition, "98100"
	read.State().SetDescriptor(source.Address(), &source)
	leaf := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == types[0] {
			return types[1]
		}
		if old == types[1] {
			return types[2]
		}
		return old
	}
	remap := NewRoutineRemap(read, nil, leaf, nil, true)
	mapped, err := remap.Procedure(&source)
	if err != nil {
		t.Fatal(err)
	}
	if err := remap.Declarations(); err != nil {
		t.Fatal(err)
	}
	if mapped.SQL == definition || mapped.SQL.Inputs[0].Type != types[1] || mapped.SQL.Formals[1].Usage.Type != types[1] || !mapped.SQL.MatchesOutputTuple(mapped.SQL.Result.Type) || mapped.SQL.Result.Type.CompositeMember(0).Usage.Type != types[1] {
		t.Fatal("formal/result mapped zero or twice")
	}
	if remap.Type(shape) != mapped.SQL.Result.Type {
		t.Fatal("declaration and expression have competing record occurrences")
	}
	literal := plan.LiteralExpr{Info: expr.Info{Usage: types[0].Usage()}, Value: sqltype.Null(types[0])}
	call := plan.SQLRoutineExpr{Info: expr.Info{Usage: definition.Result, RoutineReference: &source, Property: expr.Properties{Volatility: expr.Volatile}}, Procedure: &source, Ref: read.Owner().Domain.Ref(read.Epoch(), source.Address()), Args: []expr.Expr{literal}, WrittenCount: 1, InputMap: []plan.RoutineInput{{Start: 0, Count: 1}}, ChildSlots: []int{0}}
	call.Signature = proc.InvocationSignature{Inputs: definition.Inputs, Result: definition.Result}
	if err := call.ValidateStoredProvider(); err != nil {
		t.Fatal("original call provenance", err)
	}
	rebuilt, _, err := mapExpressionWithProcedure(read, nil, call, remap.Type, nil, nil, false, nil, remap.Procedure, remap.Collation)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.ResultUsage() != mapped.SQL.Result || rebuilt.(plan.SQLRoutineExpr).Procedure != mapped {
		t.Fatal("mapped call lost declaration result")
	}
	if err := rebuilt.(plan.SQLRoutineExpr).ValidateStoredProvider(); err != nil {
		t.Fatal("mapped call result provenance", err)
	}
	if source.SQL != definition || definition.Result.Type != shape || shape.CompositeMember(0).Usage.Type != types[0] || definition.Formals[1].Usage.Type != types[0] || call.ResultUsage().Type != shape {
		t.Fatal("source/cursor occurrence mutated")
	}
	same := NewRoutineRemap(read, nil, func(old *sqltype.Descriptor) *sqltype.Descriptor { return old }, nil, true)
	shared, err := same.Definition(definition)
	if err != nil || shared != definition {
		t.Fatal("no-change definition lost sharing", err)
	}
	if err := remap.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestPseudoRoutineResultRemap(t *testing.T) {
	read, _ := resultProviderFixture(t)
	var generations [2]map[sqltype.ID]*sqltype.Descriptor
	for i := range generations {
		generations[i] = make(map[sqltype.ID]*sqltype.Descriptor)
		for _, typ := range sqltype.Builtins() {
			generations[i][typ.ID()] = typ
		}
	}
	for _, id := range []sqltype.ID{2283, 2277, 2776, 3500, 3831, 4537, 5077, 5078, 5079, 5080, 4538} {
		definition := &proc.Definition{Name: "pseudo_outputs"}
		if err := definition.SetFormals([]proc.Formal{
			{Mode: "b", Name: "first", Usage: generations[0][id].Usage()},
			{Mode: "o", Name: "label", Usage: generations[0][sqltype.Text].Usage()},
		}); err != nil {
			t.Fatal(id, err)
		}
		shape, err := definition.OutputTuple()
		if err != nil {
			t.Fatal(id, err)
		}
		definition.Result = shape.Usage()
		remap := NewRoutineRemap(read, nil, func(old *sqltype.Descriptor) *sqltype.Descriptor {
			if old != nil && generations[0][old.ID()] == old {
				return generations[1][old.ID()]
			}
			return old
		}, nil, true)
		mapped, err := remap.Definition(definition)
		if err != nil {
			t.Fatal(id, err)
		}
		if mapped == definition || mapped.Inputs[0].Type != generations[1][id] ||
			mapped.Formals[0].Usage != generations[1][id].Usage() ||
			mapped.Formals[1].Usage != generations[1][sqltype.Text].Usage() ||
			!mapped.MatchesOutputTuple(mapped.Result.Type) {
			t.Fatal("declaration members lost mapped provenance", id)
		}
		if mapped.Result.Type == shape || mapped.Result.Type.Concrete() || mapped.Result.Valid() {
			t.Fatal("declaration remapping granted executable authority", id)
		}
		if remap.Type(shape) != mapped.Result.Type {
			t.Fatal("original output occurrence acquired competing shapes", id)
		}
		again, err := remap.Definition(definition)
		if err != nil || again != mapped {
			t.Fatal("original declaration lost operation memo identity", id, err)
		}
		if definition.Inputs[0].Type != generations[0][id] ||
			definition.Result.Type != shape || !definition.MatchesOutputTuple(shape) {
			t.Fatal("remapping mutated published declaration", id)
		}
		foreign := *generations[1][id]
		invalid := NewRoutineRemap(read, nil, func(old *sqltype.Descriptor) *sqltype.Descriptor {
			if old == generations[0][id] {
				return &foreign
			}
			return old
		}, nil, true)
		if _, err := invalid.Definition(definition); err == nil {
			t.Fatal("foreign same-OID polymorphic member admitted", id)
		}
	}
}

func TestSQLRoutineScalarRecordRemap(t *testing.T) {
	read, original := resultProviderFixture(t)
	record, _ := read.LookupType(2249)
	integer, _ := read.LookupType(sqltype.Int4)
	source := *original
	source.SQL = &proc.Definition{Name: source.Name(), Result: record.Usage()}
	source.Signature = ""
	if err := source.SQL.SetFormals(nil); err != nil {
		t.Fatal(err)
	}
	read.State().SetDescriptor(source.Address(), &source)
	if err := validateSQLProcedure(read, &source); err != nil {
		t.Fatal("original catalog RECORD declaration", err)
	}
	// A fresh builtin generation changes declaration/member authority, while
	// deferred transport remains the original call's occurrence-owned artifact.
	destination := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		destination[typ.ID()] = typ
	}
	leaf := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		if next := destination[old.ID()]; next != nil {
			return next
		}
		return old
	}
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "a", Usage: integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []sqltype.Usage{sqltype.DeferredRecord().Usage(), shape.Usage()} {
		remap := NewRoutineRemap(read, nil, leaf, nil, true)
		// Stabilize declaration projections before remapping any consumer, as
		// required by the catalog remap lifecycle.
		procedure, err := remap.Procedure(&source)
		if err != nil {
			t.Fatal("prepare scalar RECORD declaration", err)
		}
		if err := remap.Declarations(); err != nil {
			t.Fatal("stabilize scalar RECORD declaration", err)
		}
		call := plan.SQLRoutineExpr{
			Info:      expr.Info{Usage: result, RoutineReference: &source, Property: expr.Properties{Volatility: expr.Volatile}},
			Procedure: &source, Ref: read.Owner().Domain.Ref(read.Epoch(), source.Address()),
			Signature: proc.InvocationSignature{Result: result},
		}
		if err := call.ValidateStoredProvider(); err != nil {
			t.Fatal("original scalar RECORD occurrence", err)
		}
		node, _, err := mapExpressionWithProcedure(read, nil, call, remap.Type, nil, nil, false, nil, remap.Procedure, remap.Collation)
		if err != nil {
			t.Fatal("scalar RECORD generation transport", err)
		}
		mapped := node.(plan.SQLRoutineExpr)
		if mapped.Procedure != procedure || mapped.RoutineReference != procedure || mapped.Ref != call.Ref {
			t.Fatalf("mapped provider provenance diverged: procedure=%p want=%p reference=%p ref=%v want=%v",
				mapped.Procedure, procedure, mapped.RoutineReference, mapped.Ref, call.Ref)
		}
		if mapped.Procedure.SQL.Result.Type != destination[2249] || mapped.Procedure.SQL == source.SQL {
			t.Fatalf("mapped declaration generation diverged: result=%p want=%p definition=%p original=%p",
				mapped.Procedure.SQL.Result.Type, destination[2249], mapped.Procedure.SQL, source.SQL)
		}
		if mapped.Signature.Result != mapped.Usage {
			t.Fatalf("mapped signature result diverged: signature=%v usage=%v", mapped.Signature.Result, mapped.Usage)
		}
		if result.Type.IsDeferredRecord() {
			if mapped.Usage != result {
				t.Fatal("deferred transport was replaced by a catalog descriptor")
			}
		} else if mapped.Usage.Type == shape || mapped.Usage.Type.CompositeMember(0).Usage.Type != destination[sqltype.Int4] {
			t.Fatal("completed FROM member retained its old generation")
		}
		if err := mapped.ValidateStoredProvider(); err != nil {
			t.Fatal("transport lost private call authority", err)
		}
		if _, err := mapped.Procedure.SQL.Specialize(mapped.Signature); err != nil {
			t.Fatal("transport lost private invocation authority", err)
		}
		if source.SQL.Result.Type != record || call.Usage != result || shape.CompositeMember(0).Usage.Type != integer {
			t.Fatal("transport mutated the source declaration or occurrence")
		}
		if err := remap.Finish(); err != nil {
			t.Fatal("finish scalar RECORD transport", err)
		}
	}
	foreign := *record
	bad := source
	definition := *source.SQL
	definition.Result = foreign.Usage()
	bad.SQL = &definition
	if err := validateSQLProcedure(read, &bad); err == nil {
		t.Fatal("foreign same-OID scalar RECORD declaration accepted")
	}
}

func TestSQLRoutineProcedureResultOwnership(t *testing.T) {
	read, original := resultProviderFixture(t)
	integer, _ := read.LookupType(sqltype.Int4)
	var leaves [3]*sqltype.Descriptor
	for i := range leaves {
		typ, err := sqltype.Domain(sqltype.ID(98500+i), "procedure_leaf", "capture.procedure_leaf", integer.Usage())
		if err != nil {
			t.Fatal(err)
		}
		leaves[i] = typ
		descriptor := &catalog.Type{SQLType: typ, ObjectNamespace: original.Namespace(), ObjectRevision: 1}
		read.State().SetDescriptor(descriptor.Address(), descriptor)
	}
	definition := &proc.Definition{Name: original.Name(), Procedure: true}
	if err := definition.SetFormals([]proc.Formal{{Mode: "i", Name: "x", Usage: leaves[0].Usage()}, {Mode: "o", Name: "answer", Usage: leaves[0].Usage()}}); err != nil {
		t.Fatal(err)
	}
	shape, err := definition.OutputTuple()
	if err != nil {
		t.Fatal(err)
	}
	definition.Result = shape.Usage()
	source := *original
	source.SQL, source.Signature, source.RoutineKind = definition, "98500", catalog.ProcedureRoutine
	read.State().SetDescriptor(source.Address(), &source)
	if err := validateSQLProcedure(read, &source); err != nil {
		t.Fatal("single OUT RECORD rejected", err)
	}
	for _, corruption := range []string{"scalar", "generic", "foreign", "name", "kind"} {
		next, layout := source, *definition
		next.SQL = &layout
		switch corruption {
		case "scalar":
			layout.Result = leaves[0].Usage()
			next.Result = 98500
		case "generic":
			generic, _ := read.LookupType(2249)
			layout.Result = generic.Usage()
		case "foreign":
			foreign := *leaves[0]
			layout.Formals = append([]proc.Formal(nil), layout.Formals...)
			layout.Formals[1].Usage = foreign.Usage()
			tuple, err := layout.OutputTuple()
			if err != nil {
				t.Fatal(err)
			}
			layout.Result = tuple.Usage()
		case "name":
			tuple, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "wrong", Usage: leaves[0].Usage()}})
			if err != nil {
				t.Fatal(err)
			}
			layout.Result = tuple.Usage()
		case "kind":
			layout.Procedure = false
		}
		if err := validateSQLProcedure(read, &next); err == nil {
			t.Fatal("corrupt procedure output admitted", corruption)
		}
	}
	mapper := NewRoutineRemap(read, nil, func(typ *sqltype.Descriptor) *sqltype.Descriptor {
		if typ == leaves[0] {
			return leaves[1]
		}
		if typ == leaves[1] {
			return leaves[2]
		}
		return typ
	}, nil, true)
	mapped, err := mapper.Procedure(&source)
	if err != nil {
		t.Fatal(err)
	}
	if err = mapper.Declarations(); err != nil {
		t.Fatal(err)
	}
	if !mapped.SQL.Procedure || mapped.SQL.Inputs[0].Type != leaves[1] || mapped.SQL.Formals[1].Usage.Type != leaves[1] || !mapped.SQL.MatchesOutputTuple(mapped.SQL.Result.Type) || mapped.SQL.Result.Type.CompositeMember(0).Usage.Type != leaves[1] || mapper.Type(shape) != mapped.SQL.Result.Type {
		t.Fatal("procedure declaration/result mapped zero or twice")
	}
	literal := plan.LiteralExpr{Info: expr.Info{Usage: leaves[0].Usage()}, Value: sqltype.Null(leaves[0])}
	call := plan.SQLRoutineExpr{Info: expr.Info{Usage: definition.Result, RoutineReference: &source, Property: expr.Properties{Volatility: expr.Volatile}}, Procedure: &source, Ref: read.Owner().Domain.Ref(read.Epoch(), source.Address()), Args: []expr.Expr{literal}, WrittenCount: 1, InputMap: []plan.RoutineInput{{Start: 0, Count: 1}}, ChildSlots: []int{0}, Signature: proc.InvocationSignature{Inputs: definition.Inputs, Result: definition.Result}}
	if err := call.ValidateStoredProvider(); err != nil {
		t.Fatal("original procedure call provenance", err)
	}
	rebuilt, _, err := mapExpressionWithProcedure(read, nil, call, mapper.Type, nil, nil, false, nil, mapper.Procedure, mapper.Collation)
	if err != nil {
		t.Fatal(err)
	}
	if err = rebuilt.(plan.SQLRoutineExpr).ValidateStoredProvider(); err != nil {
		t.Fatal(err)
	}
	if rebuilt.ResultUsage().Type != mapper.Type(shape) || rebuilt.(plan.SQLRoutineExpr).Procedure != mapped {
		t.Fatal("call and declaration remapped competing result generations")
	}
	rejected := NewRoutineRemap(read, nil, func(typ *sqltype.Descriptor) *sqltype.Descriptor {
		if typ == leaves[0] {
			return nil
		}
		return typ
	}, nil, true)
	if _, err := rejected.Definition(definition); err == nil {
		t.Fatal("missing child generation accepted")
	}
	if definition.Result.Type != shape || definition.Formals[1].Usage.Type != leaves[0] || source.SQL != definition || call.ResultUsage().Type != shape {
		t.Fatal("remap or rejected candidate mutated source")
	}
	if err := mapper.Finish(); err != nil {
		t.Fatal(err)
	}
}

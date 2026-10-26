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
	d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 98000}, ObjectRevision: 1, ObjectNamespace: relation.Namespace(), ObjectName: definition.Name, Signature: "23", RoutineKind: catalog.FunctionRoutine, Result: 2249, Volatility: expr.Volatile, SQL: definition, Language: language.Address(), Owner: owner.Ref}
	read.State().SetDescriptor(d.Address(), d)
	return read, d
}

func TestSQLRoutineResultShapeValidation(t *testing.T) {
	read, d := resultProviderFixture(t)
	if err := validateSQLProcedure(read, d); err != nil {
		t.Fatal("correct formal-derived shape", err)
	}
	original := d.SQL.Result.Type
	for _, kind := range []string{"name", "order", "type", "attribute", "width", "unshaped", "foreign child", "set policy", "projection"} {
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
	literal := plan.LiteralExpr{Info: expr.Info{Usage: types[1].Usage()}, Value: sqltype.Null(types[1])}
	call := plan.SQLRoutineExpr{Info: expr.Info{Usage: definition.Result, RoutineReference: &source, Property: expr.Properties{Volatility: expr.Volatile}}, Procedure: &source, Ref: read.Owner().Domain.Ref(read.Epoch(), source.Address()), Args: []expr.Expr{literal}, WrittenCount: 1, InputMap: []plan.RoutineInput{{Start: 0, Count: 1}}, ChildSlots: []int{0}}
	info := call.Info
	info.Usage.Type = remap.Type(info.Usage.Type)
	info.RoutineReference = mapped
	rebuilt, err := call.Rebuild(info, call.Args)
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

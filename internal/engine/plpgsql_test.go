package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func plUnitScope(t *testing.T) (*Engine, *StatementScope) {
	t.Helper()
	e := New()
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(done)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	return e, scope
}

func TestPLpgSQLProcedureReturn(t *testing.T) {
	for _, source := range []string{"BEGIN RETURN; END", "BEGIN END", "BEGIN BEGIN RETURN; END; END"} {
		t.Run(source, func(t *testing.T) {
			e, s := plUnitScope(t)
			read := e.CatalogRead()
			language, err := catalog.LookupLanguage(read, "plpgsql")
			if err != nil {
				t.Fatal(err)
			}
			void, _ := read.LookupType(sqltype.Void)
			raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			d := &catalog.Procedure{ObjectName: "procedure_unit", RoutineKind: catalog.ProcedureRoutine, Language: language.Address(), SQL: &proc.Definition{Kind: proc.PLpgSQLBody, Name: "procedure_unit", Source: source, Result: void.Usage(), PLSyntax: raw}}
			e.mu.Lock()
			auth, err := e.authorizationLocked(s.op)
			if err == nil {
				err = e.compilePLRoutineLocked(s.op, auth, d)
			}
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			input := proc.NewFrame(nil)
			defer input.Close()
			value, err := s.invokePLRoutine(d, input, read, sqltype.CollationRef{})
			if err != nil || value.Type() != nil {
				t.Fatal("procedure returned a fake function scalar", value, err)
			}
		})
	}
	e, s := plUnitScope(t)
	read := e.CatalogRead()
	language, err := catalog.LookupLanguage(read, "plpgsql")
	if err != nil {
		t.Fatal(err)
	}
	void, _ := read.LookupType(sqltype.Void)
	source := "BEGIN RETURN 1; END"
	raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	d := &catalog.Procedure{ObjectName: "bad_procedure", RoutineKind: catalog.ProcedureRoutine, Language: language.Address(), SQL: &proc.Definition{Kind: proc.PLpgSQLBody, Name: "bad_procedure", Source: source, Result: void.Usage(), PLSyntax: raw}}
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	if err == nil {
		err = e.compilePLRoutineLocked(s.op, auth, d)
	}
	e.mu.Unlock()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "RETURN cannot have a parameter in a procedure" {
		t.Fatal(err)
	}
}

func TestPLpgSQLRowAssignment(t *testing.T) {
	e, s := plUnitScope(t)
	read := e.CatalogRead()
	integer, _ := read.LookupType(sqltype.Int4)
	text, _ := read.LookupType(sqltype.Text)
	pair, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "a", Usage: integer.Usage()}, {Attribute: 2, Name: "b", Usage: text.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	single, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "different", Usage: text.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := sqltype.OwnedCompositeValue(single, []sqltype.Value{sqltype.String(text, "42")})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.convertPLValue(source, pair.Usage())
	if err != nil {
		t.Fatal(err)
	}
	number, err := result.CompositeField(0).Integer()
	if err != nil || number != 42 || !result.CompositeField(1).IsNull() || result.CompositeField(1).Type() != text || source.CompositeField(0) != sqltype.String(text, "42") {
		t.Fatal("ordinal/null-fill/IO/source ownership", result, err)
	}
	small, err := s.convertPLValue(result, single.Usage())
	if err != nil {
		t.Fatal(err)
	}
	if small.CompositeLen() != 1 {
		t.Fatal("surplus source fields retained")
	}
	nullRow, err := s.convertPLValue(sqltype.Null(integer), pair.Usage())
	if err != nil || !nullRow.IsNull() || nullRow.Type() != pair {
		t.Fatal("whole NULL lost its known shape", nullRow, err)
	}
	materializedFrame := &proc.PLFrame{Program: &proc.PLProgram{Variables: []proc.PLVariable{{Name: "r", Usage: pair.Usage()}}}, Values: []sqltype.Value{nullRow}}
	defer materializedFrame.Close()
	materialized, err := plUnitAssignQueryValue(t, s, materializedFrame, "r.a", sqltype.String(text, "7"))
	if err != nil || materialized.IsNull() || !materialized.CompositeField(1).IsNull() || !nullRow.IsNull() {
		t.Fatal("field assignment failed to materialize privately", materialized, nullRow, err)
	}
	f := &proc.PLFrame{Program: &proc.PLProgram{Variables: []proc.PLVariable{{Name: "r", Record: true}}}, Values: make([]sqltype.Value, 1)}
	if f.Values[0].Type() != nil {
		t.Fatal("record was preassigned a shape")
	}
	if _, err = s.assignPLVariable(f, 0, source); err != nil {
		t.Fatal(err)
	}
	if f.Values[0].Type() != single {
		t.Fatal("first record shape lost")
	}
	if _, err = s.assignPLVariable(f, 0, result); err != nil {
		t.Fatal(err)
	}
	if f.Values[0].Type() != pair {
		t.Fatal("record shape did not change")
	}
	previous := f.Values[0]
	if _, err = plUnitAssignQueryValue(t, s, f, "r.a", sqltype.String(text, "7")); err != nil {
		t.Fatal(err)
	}
	old, err := previous.CompositeField(0).Integer()
	if err != nil || old != 42 {
		t.Fatal("field update mutated retained row")
	}
	retained := f.Values[0]
	f.Program.Variables[0].NotNull = true
	_, err = s.assignPLVariable(f, 0, sqltype.Null(integer))
	domainUnitDiagnostic(t, err, "22004", `null value cannot be assigned to variable "r" declared NOT NULL`)
	if f.Values[0] != retained {
		t.Fatal("failed RECORD clear changed the slot")
	}
	f.Program.Variables[0].NotNull = false
	if _, err = s.assignPLVariable(f, 0, sqltype.Null(text)); err != nil || f.Values[0] != (sqltype.Value{}) {
		t.Fatal("RECORD NULL retained shape", f.Values[0], err)
	}
	_, err = s.assignPLVariable(f, 0, materialized.CompositeField(0))
	domainUnitDiagnostic(t, err, "0A000", "input of anonymous composite types is not implemented")
	if f.Values[0] != (sqltype.Value{}) || retained.Type() != pair {
		t.Fatal("failed scalar assignment changed cleared/retained record")
	}
	if _, err = s.assignPLVariable(f, 0, source); err != nil || f.Values[0].Type() != single {
		t.Fatal("record did not adopt fresh shape", err)
	}
	f.Close()
}

func TestPLpgSQLCompositeAssignmentAtomicity(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, declaration := range []string{
		"CREATE TYPE pl_atomic_pair AS (a integer)",
		"CREATE DOMAIN pl_atomic_positive AS pl_atomic_pair CHECK((VALUE).a>0)",
	} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, declaration))
	}
	s, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Finish(nil)
	// Direct converter entry still needs the real owning statement's invoker
	// authority for the domain CHECK's ordinary int4gt routine admission.
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err == nil {
		s.routineAuth = auth
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	read := e.CatalogRead()
	domain := domainUnitType(t, read, "pl_atomic_positive").SQLType
	base := domain.BaseType()
	integer, _ := read.LookupType(sqltype.Int4)
	three, err := sqltype.Integer(integer, 3)
	if err != nil {
		t.Fatal(err)
	}
	negative, err := sqltype.Integer(integer, -1)
	if err != nil {
		t.Fatal(err)
	}
	good, err := sqltype.OwnedCompositeValue(base, []sqltype.Value{three})
	if err != nil {
		t.Fatal(err)
	}
	bad, err := sqltype.OwnedCompositeValue(base, []sqltype.Value{negative})
	if err != nil {
		t.Fatal(err)
	}
	// Tagging alone is not validation: a fresh same-domain candidate must not
	// take an identity coercion shortcut around the checked input boundary.
	taggedBad, err := domain.Wrap(bad)
	if err != nil {
		t.Fatal(err)
	}
	f := &proc.PLFrame{Program: &proc.PLProgram{Variables: []proc.PLVariable{{Name: "r", Usage: domain.Usage()}}}, Values: make([]sqltype.Value, 1)}
	defer f.Close()
	if _, err = s.assignPLVariable(f, 0, good); err != nil {
		t.Fatal(err)
	}
	retained := f.Values[0]
	for _, assignment := range []struct {
		path  string
		value sqltype.Value
	}{
		{"", bad},
		{"", taggedBad},
		{"r.a", negative},
	} {
		if assignment.path == "" {
			_, err = s.assignPLVariable(f, 0, assignment.value)
		} else {
			_, err = plUnitAssignQueryValue(t, s, f, assignment.path, assignment.value)
		}
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "23514" {
			t.Fatal("missing genuine domain CHECK failure", err)
		}
		if f.Values[0] != retained || retained.CompositeField(0) != three || good.CompositeField(0) != three {
			t.Fatal("failed assignment published a candidate or mutated retained input")
		}
	}
	holder, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "inner", Usage: domain.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	outer, err := sqltype.OwnedCompositeValue(holder, []sqltype.Value{retained})
	if err != nil {
		t.Fatal(err)
	}
	nested := &proc.PLFrame{Program: &proc.PLProgram{Variables: []proc.PLVariable{{Name: "holder", Usage: holder.Usage()}}}, Values: []sqltype.Value{outer}}
	defer nested.Close()
	_, err = plUnitAssignQueryValue(t, s, nested, "holder.inner.a", negative)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23514" || nested.Values[0] != outer || outer.CompositeField(0) != retained {
		t.Fatal("nested domain failure changed ancestor slots/payloads", err)
	}
	if _, err = s.assignPLVariable(f, 0, sqltype.Null(integer)); err != nil || !f.Values[0].IsNull() || f.Values[0].Type() != domain {
		t.Fatal("nullable composite-domain NULL lost its tag/nullness", err)
	}
}

func TestPLpgSQLHandlerAuthority(t *testing.T) {
	e := New()
	read := e.CatalogRead()
	language, err := catalog.LookupLanguage(read, "plpgsql")
	if err != nil {
		t.Fatal(err)
	}
	other := New().CatalogRead()
	if err := (catalog.LanguageProvider{}).Validate(other, language); err == nil {
		t.Fatal("foreign language accepted")
	}
	for _, address := range []ids.ObjectAddress{language.Call, language.Inline, language.Validator} {
		handler, _ := read.Procedure(address)
		if err := catalog.ValidatePLHandler(read, handler); err != nil {
			t.Fatal(err)
		}
		if err := catalog.ValidatePLHandler(other, handler); err == nil {
			t.Fatal("foreign handler accepted")
		}
		for _, corrupt := range []func(*catalog.Procedure){
			func(d *catalog.Procedure) { d.Result = 23 },
			func(d *catalog.Procedure) { d.Signature = "23" },
			func(d *catalog.Procedure) { d.SQL = &proc.Definition{} },
			func(d *catalog.Procedure) {
				d.DeclarationDependencies = []ids.ObjectAddress{{ClassID: ids.TypeClassID, ObjectID: 23}}
			},
			func(d *catalog.Procedure) { d.Owner = other.Owner().Principals.Principal(d.Owner.ID()) },
			func(d *catalog.Procedure) { d.ObjectRevision = 0 },
		} {
			forged := *handler
			corrupt(&forged)
			read.State().SetDescriptor(address, &forged)
			if err := catalog.ValidatePLHandler(read, &forged); err == nil {
				t.Fatal("forged handler accepted", forged)
			}
			read.State().SetDescriptor(address, handler)
		}
	}
	clone := *language
	clone.Inline = language.Call
	read.State().SetDescriptor(language.Address(), &clone)
	if err := (catalog.LanguageProvider{}).Validate(read, &clone); err == nil {
		t.Fatal("call capability substituted for inline")
	}
	if err := (catalog.LanguageProvider{}).Validate(read, language); err == nil {
		t.Fatal("stale language accepted")
	}
	read.State().SetDescriptor(language.Address(), language)
	sqlLanguage, err := catalog.LookupLanguage(read, "sql")
	if err != nil || sqlLanguage.Inline != (ids.ObjectAddress{}) {
		t.Fatal("SQL acquired inline capability", err)
	}
}

func TestPLpgSQLRemapOnce(t *testing.T) {
	source := New().CatalogRead()
	destination := New().CatalogRead()
	language, err := catalog.LookupLanguage(source, "plpgsql")
	if err != nil {
		t.Fatal(err)
	}
	move := func(a ids.ObjectAddress, delta ids.OID) ids.ObjectAddress { a.ObjectID += delta; return a }
	mapping := make(map[ids.ObjectAddress]ids.ObjectAddress)
	for _, address := range []ids.ObjectAddress{language.Address(), language.Call, language.Inline, language.Validator} {
		mapping[address] = move(address, 100000)
		mapping[move(address, 100000)] = move(address, 200000)
	}
	next := extension.RemapDescriptor(language, mapping).(*catalog.Language)
	if next.Address() != mapping[language.Address()] || next.Call != mapping[language.Call] || next.Inline != mapping[language.Inline] || next.Validator != mapping[language.Validator] || language.Address() == next.Address() {
		t.Fatal("language references remapped twice or source mutated")
	}
	handler, _ := source.Procedure(language.Call)
	routine := *handler
	routine.PLHandler = catalog.NoPLHandler
	routine.Language = language.Address()
	sourceMember := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90001, SubID: 7}
	sourceBase := sourceMember
	sourceBase.SubID = 0
	mapping[sourceBase] = move(sourceBase, 100000)
	mapping[move(sourceBase, 100000)] = move(sourceBase, 200000)
	routine.DeclarationDependencies = []ids.ObjectAddress{sourceMember}
	mapped := extension.RemapDescriptor(&routine, mapping).(*catalog.Procedure)
	if mapped.Language != mapping[language.Address()] || routine.Language != language.Address() {
		t.Fatal("procedure language remapped twice/source changed")
	}
	expectedMember := mapping[sourceBase]
	expectedMember.SubID = sourceMember.SubID
	if len(mapped.DeclarationDependencies) != 1 || mapped.DeclarationDependencies[0] != expectedMember || routine.DeclarationDependencies[0] != sourceMember {
		t.Fatal("declaration replay source remapped twice/lost stable attribute/source changed")
	}
	unchanged := extension.RemapDescriptor(&routine, nil).(*catalog.Procedure)
	if &unchanged.DeclarationDependencies[0] != &routine.DeclarationDependencies[0] {
		t.Fatal("unchanged immutable replay metadata copied")
	}
	state := catalog.NewState(1)
	original := *language
	original.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: language.Owner, Grantee: authorization.ACLGrantee{Role: language.Owner}, Privileges: authorization.UsagePrivilege}}}
	state.SetDescriptor(original.Address(), &original)
	target := destination.Owner().Principals.Principal(language.Owner.ID())
	principal := func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		if ref != language.Owner {
			return authorization.PrincipalRef{}, errors.New("principal mapped twice")
		}
		return target, nil
	}
	if err := catalog.RemapPrincipals(state, catalog.PrincipalRemap{Principal: principal, SeededOwner: func(ref authorization.PrincipalRef, _ bool) (authorization.PrincipalRef, error) {
		return principal(ref)
	}}); err != nil {
		t.Fatal(err)
	}
	remapped := state.Get(original.Address()).(*catalog.Language)
	if remapped.Owner != target || remapped.ACL.Entries[0].Grantor != target || remapped.ACL.Entries[0].Grantee.Role != target || original.Owner != language.Owner {
		t.Fatal("principal ownership/source mutation")
	}
	copy := *handler
	if !exec.SameRoutineDeclaration(handler, &copy) {
		t.Fatal("equivalent COW changed declaration")
	}
	copy.Language = language.Address()
	if exec.SameRoutineDeclaration(handler, &copy) {
		t.Fatal("language change ignored")
	}
	copy = *handler
	copy.DeclarationDependencies = []ids.ObjectAddress{sourceMember}
	if exec.SameRoutineDeclaration(handler, &copy) {
		t.Fatal("declaration replay source change ignored")
	}
}

func TestPLpgSQLDepthUnwind(t *testing.T) {
	e := New()
	unicode, err := sqltype.RegisteredCollation(6411, 6411)
	if err != nil {
		t.Fatal(err)
	}
	c, err := sqltype.RegisteredCollation(950, 950)
	if err != nil {
		t.Fatal(err)
	}
	outer := &proc.PLFrame{Program: &proc.PLProgram{}, InputCollation: unicode}
	s := &StatementScope{sqlRoutine: sqlRoutineDemand{lexical: outer}, activeSQLRoutineDepth: 3}
	for _, primary := range []error{nil, context.Canceled, errors.New("reached expression failure")} {
		restore := s.activateSQLRoutine(nil, e.CatalogRead())
		s.sqlRoutine.lexical = &proc.PLFrame{InputCollation: c}
		if err := s.enterSQLRoutine(); err != nil {
			t.Fatal(err)
		}
		s.leaveSQLRoutine()
		if got := restore(primary); !errors.Is(got, primary) || s.sqlRoutine.lexical != outer || s.sqlRoutine.lexical.InputCollation != unicode || s.activeSQLRoutineDepth != 3 {
			t.Fatal("PL lexical/root unwind", got)
		}
	}
	s.activeSQLRoutineDepth = maxSQLRoutineDepth
	if err := s.enterSQLRoutine(); err == nil || s.activeSQLRoutineDepth != maxSQLRoutineDepth {
		t.Fatal("PL acquired an independent depth guard")
	}
}

func TestPLpgSQLDiagnosticIdentity(t *testing.T) {
	e, scope := plUnitScope(t)
	read := e.CatalogRead()
	for _, tc := range []struct {
		name      string
		signature catalog.ProcedureSignature
		want      string
	}{
		{"plain", "", "plain()"},
		{"pl_domain_authority", "23", "pl_domain_authority(integer)"},
		{"Mixed Name", "23,25", `"Mixed Name"(integer,text)`},
	} {
		d := &catalog.Procedure{ObjectName: tc.name, Signature: tc.signature}
		if got := scope.plRoutineDiagnosticIdentity(read, d); got != tc.want {
			t.Fatalf("identity %q, want %q", got, tc.want)
		}
		d.SQL = &proc.Definition{Source: "BEGIN RETURN; END"}
		err := scope.plRuntimeError(sqlError("22012", "division by zero"), read, d, parser.Position{}, parser.Position{}, parser.Position{}, "at assignment", false)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Context != "PL/pgSQL function "+tc.want+" line 1 at assignment" {
			t.Fatal("runtime context lost canonical identity", err)
		}
	}
	t.Run("definition_visibility", func(t *testing.T) {
		candidate := catalog.NewCandidate(read)
		namespace := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 910010}, ObjectName: "Mixed Schema"}
		typ := &catalog.Type{SQLType: sqltype.LookupOnly(910011, "special", 0), ObjectNamespace: namespace.Address()}
		routine := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 910012}, ObjectNamespace: namespace.Address(), ObjectName: "Mixed Name", Signature: "23,910011"}
		shadow := *routine
		shadow.ObjectAddress.ObjectID = 910013
		shadow.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		for _, descriptor := range []catalog.Descriptor{namespace, typ, routine, &shadow} {
			if err := candidate.Create(descriptor); err != nil { t.Fatal(err) }
		}
		reader := candidate.Definitions()
		public := shadow.Namespace()
		for _, tc := range []struct { path []ids.ObjectAddress; want string }{
			{[]ids.ObjectAddress{namespace.Address(), public}, `"Mixed Name"(integer,special)`},
			{[]ids.ObjectAddress{public, namespace.Address()}, `"Mixed Schema"."Mixed Name"(integer,special)`},
			{[]ids.ObjectAddress{public}, `"Mixed Schema"."Mixed Name"(integer,"Mixed Schema".special)`},
		} {
			if got := formatPLDiagnosticIdentity(read, reader, tc.path, routine); got != tc.want {
				t.Fatalf("definition-aware identity got %q want %q", got, tc.want)
			}
		}
	})
}

func TestPLpgSQLCheckedCallCollation(t *testing.T) {
	read := New().CatalogRead()
	text, _ := read.LookupType(sqltype.Text)
	integer, _ := read.LookupType(sqltype.Int4)
	unicode, err := sqltype.RegisteredCollation(6411, 6411)
	if err != nil {
		t.Fatal(err)
	}
	c, err := sqltype.RegisteredCollation(950, 950)
	if err != nil {
		t.Fatal(err)
	}
	definition := &proc.Definition{Result: integer.Usage()}
	if err := definition.SetFormals([]proc.Formal{{Mode: "i", Usage: text.Usage()}}); err != nil {
		t.Fatal(err)
	}
	procedure := &catalog.Procedure{ObjectName: "checked_call", Result: ids.OID(integer.ID()), SQL: definition}
	usage := text.Usage()
	usage.Collation = unicode
	arg := plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: sqltype.String(text, "Straße")}
	node := plan.SQLRoutineExpr{Info: expr.Info{Usage: integer.Usage(), RoutineReference: procedure}, Procedure: procedure, Args: []expr.Expr{arg}, WrittenCount: 1, InputMap: []plan.RoutineInput{{Start: 0, Count: 1}}}
	derive := func(info expr.Info, args ...expr.Expr) (expr.Info, error) {
		return analyze.CollatedInfo(read, info, args...)
	}
	node, err = node.Collate(derive)
	if err != nil {
		t.Fatal(err)
	}
	node, err = node.AssembleArguments()
	if err != nil {
		t.Fatal("genuine collated actual rejected", err)
	}
	if node.InputCollation != unicode || node.Usage.Collation.ID != 0 {
		t.Fatal("input authority conflated with noncollatable result", node)
	}
	other := arg
	other.Usage.Collation = c
	mapped, changed, err := node.Retag(node.Info, []expr.Expr{other}, true, expr.Retag{Collated: derive})
	if err != nil || !changed || mapped.(plan.SQLRoutineExpr).InputCollation != c || node.InputCollation != unicode {
		t.Fatal("call rebuild leaked/lost input authority", err)
	}
	bad := arg
	bad.Usage.Typmod = 12
	if err := node.ValidateArguments([]expr.Expr{bad}); err == nil {
		t.Fatal("collation admission weakened typmod identity")
	}
	bad.Usage = integer.Usage()
	if err := node.ValidateArguments([]expr.Expr{bad}); err == nil {
		t.Fatal("collation admission weakened formal type identity")
	}
}

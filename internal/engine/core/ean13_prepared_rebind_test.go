package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// isnSQLInstalled installs isn through SQL.
func isnSQLInstalled(t *testing.T) (*core.Engine, *catalog.Extension) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION isn")
	return e, e.Catalog.State.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "isn"}))
}

func TestEan13PreparedAuthorityRebind(t *testing.T)  { testIsnPreparedAuthorityRebind(t, "ean13", 0) }
func TestIsbnPreparedAuthorityRebind(t *testing.T)   { testIsnPreparedAuthorityRebind(t, "isbn", 3) }
func TestIsmnPreparedAuthorityRebind(t *testing.T)   { testIsnPreparedAuthorityRebind(t, "ismn", 9) }
func TestIsmn13PreparedAuthorityRebind(t *testing.T) { testIsnPreparedAuthorityRebind(t, "ismn13", 12) }
func TestIssnPreparedAuthorityRebind(t *testing.T)   { testIsnPreparedAuthorityRebind(t, "issn", 15) }
func TestIssn13PreparedAuthorityRebind(t *testing.T) { testIsnPreparedAuthorityRebind(t, "issn13", 18) }
func TestUpcPreparedAuthorityRebind(t *testing.T)    { testIsnPreparedAuthorityRebind(t, "upc", 21) }

func testIsnPreparedAuthorityRebind(t *testing.T, family string, slot int) {
	e, ext := isnSQLInstalled(t)
	testInstalledScalarPreparedAuthorityRebind(t, e, ext, family, slot)
}

func testInstalledScalarPreparedAuthorityRebind(t *testing.T, e *core.Engine, ext *catalog.Extension, family string, slot int) {
	t.Helper()
	base := e.CatalogRead().MustType(ext.Members[slot])
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "ALTER TYPE public."+family+" RENAME TO moved")
	current := e.CatalogRead().MustType(base.Address())
	raw := parser.CastExpr{Child: parser.ParameterExpr{Number: 1}, TypeName: parser.TypeName{Name: family, Schema: &parser.Identifier{Text: "public"}, Pos: parser.Position{Offset: 11, Character: 12}}, Syntax: parser.PostfixCast}
	key := analyze.PreparedTypeKey{Offset: 11, Kind: analyze.IdentityCast}
	shared := map[analyze.PreparedTypeKey]analyze.TypeOccurrence{key: {Kind: analyze.IdentityCast, Base: base.SQLType.Usage()}}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	path, err := core.EngineSequenceEnvironmentLocked(e, auth)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	analysis := core.Analysis{Domain: e.Catalog.Domain, Epoch: auth.Catalog.Epoch(), Session: auth.Session.SessionToken, Private: auth.Session.Transactions.Catalog, Path: path, Namespace: auth.Catalog.State().NamespaceGeneration(), Types: map[ids.ObjectAddress]*catalog.Type{base.Address(): base}, Dependencies: []catalog.ExpressionDependency{{Ref: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), base.Address()), Revision: base.Revision()}}}
	changed := *current
	changed.ObjectRevision++
	changed.Owner = authorization.PrincipalRef{}
	changed.ACL.Explicit = !changed.ACL.Explicit
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&changed); err != nil {
		t.Fatal(err)
	}
	auth.Catalog = e.StageRead(stage)
	valid := func(a *core.Analysis, rebind bool) bool {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		return core.EnginePreparedAnalysisDefinitionsValidLocked(e, auth, a, path, rebind)
	}
	if valid(&analysis, false) || !valid(&analysis, true) {
		t.Fatal("owner/ACL changes must invalidate executable analysis but permit nominal rebind")
	}
	for _, tc := range []struct {
		name   string
		change func(*core.Analysis)
	}{
		{"epoch", func(a *core.Analysis) { *(&a.Epoch)++ }},
		{"domain", func(a *core.Analysis) { a.Domain = new(ids.Domain) }},
		{"session", func(a *core.Analysis) { a.Session = ids.SessionToken{} }},
		{"private", func(a *core.Analysis) { a.Private = new(session.Journal) }},
		{"namespace", func(a *core.Analysis) { *(&a.Namespace)++ }},
		{"retired", func(a *core.Analysis) { a.NeedsReanalysis = true }},
		{"cast", func(a *core.Analysis) {
			a.Casts = map[catalog.CastPair]*catalog.Cast{{Source: sqltype.Text, Target: sqltype.Int4}: new(catalog.Cast)}
		}},
		{"path", func(a *core.Analysis) {
			(&a.Path).ImplicitCatalog = !a.Path.ImplicitCatalog
		}},
		{"missing dependency", func(a *core.Analysis) {
			a.Dependencies = []catalog.ExpressionDependency{{Ref: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID})}}
		}},
		{"unrelated dependency", func(a *core.Analysis) {
			a.Dependencies = append(append([]catalog.ExpressionDependency(nil), a.Dependencies...), catalog.ExpressionDependency{Ref: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), current.Namespace())})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := analysis
			tc.change(&other)
			if valid(&other, true) {
				t.Fatal("invalid environment or cast rescued nominal identity")
			}
		})
	}
	sibling := sqltype.Isbn
	if family == "isbn" {
		sibling = sqltype.Ean13
	}
	if family == "ltxtquery" {
		sibling = sqltype.Ltree
	}
	foreign, err := sibling(base.SQLType.ID(), base.Name(), base.SQLType.SQLName())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*catalog.Type)
	}{
		{"physical", func(d *catalog.Type) { d.Dimensions++ }},
		{"extension", func(d *catalog.Type) { d.Extension = ids.ObjectAddress{} }},
		{"array", func(d *catalog.Type) { d.Array++ }},
		{"family", func(d *catalog.Type) { d.SQLType = foreign }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := *base
			tc.change(&old)
			other := analysis
			other.Types = map[ids.ObjectAddress]*catalog.Type{base.Address(): &old}
			if valid(&other, true) {
				t.Fatal("changed executable or provider definition rescued nominal identity")
			}
		})
	}
	expected := &core.Description{Parameters: analyze.ParameterInfo{Count: 1, Types: []sqltype.Usage{base.SQLType.Usage()}}, CatalogOwner: e.CatalogRead().Owner(), CatalogEpoch: e.CatalogRead().Epoch(), Types: shared, Analysis: &analysis}
	expected.PlanGeneration = auth.Session.DiscardPlansGeneration
	e.Mu.Lock()
	eligible := core.EnginePreparedInstalledScalarAuthorityRebindLocked(e, auth, expected, path)
	*(&expected.PlanGeneration)++
	discarded := core.EnginePreparedInstalledScalarAuthorityRebindLocked(e, auth, expected, path)
	e.Mu.Unlock()
	if !eligible || discarded {
		t.Fatal("DISCARD must disallow nominal rebind")
	}
	expected.PlanGeneration = auth.Session.DiscardPlansGeneration
	withoutCarry := *expected
	isolatedAnalysis := analysis
	withoutCarry.Analysis = &isolatedAnalysis
	if _, ok := withoutCarry.RetireAnalysisTypeCastsForRebind(false)[key]; ok {
		t.Fatal("ineligible candidate retained nominal identity")
	}
	candidate := *expected
	candidate.Types = expected.RetireAnalysisTypeCastsForRebind(true)
	if _, ok := expected.Types[key]; ok || !expected.Analysis.NeedsReanalysis || shared[key].Base.Type != base.SQLType {
		t.Fatal("candidate mutated shared captures or failed sticky retirement")
	}
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, &candidate)
	node, err := binder.Cast(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil || node.ResultUsage().Type != current.SQLType {
		t.Fatal("candidate lost current nominal descriptor", err)
	}
	// A candidate that never publishes cannot rescue the original description.
	binder = newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, expected)
	if _, err := binder.Cast(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err == nil {
		t.Fatal("unpublished candidate revived the original old spelling")
	}
	for _, carry := range []bool{false, true} {
		retired := *expected
		retired.Types = expected.Types
		if _, ok := retired.RetireAnalysisTypeCastsForRebind(carry)[key]; ok {
			t.Fatal("explicit retirement or DISCARD revived old captures")
		}
	}
}

func TestEan13PreparedLiteralReown(t *testing.T)  { testInstalledScalarPreparedLiteralReown(t, "ean13", 0) }
func TestIsbnPreparedLiteralReown(t *testing.T)   { testInstalledScalarPreparedLiteralReown(t, "isbn", 3) }
func TestIsmnPreparedLiteralReown(t *testing.T)   { testInstalledScalarPreparedLiteralReown(t, "ismn", 9) }
func TestIsmn13PreparedLiteralReown(t *testing.T) { testInstalledScalarPreparedLiteralReown(t, "ismn13", 12) }
func TestIssnPreparedLiteralReown(t *testing.T)   { testInstalledScalarPreparedLiteralReown(t, "issn", 15) }
func TestIssn13PreparedLiteralReown(t *testing.T) { testInstalledScalarPreparedLiteralReown(t, "issn13", 18) }
func TestUpcPreparedLiteralReown(t *testing.T)    { testInstalledScalarPreparedLiteralReown(t, "upc", 21) }
func TestLqueryPreparedLiteralReown(t *testing.T) {
	testInstalledScalarPreparedLiteralReown(t, "lquery", 0)
}
func TestLtreePreparedLiteralReown(t *testing.T) {
	testInstalledScalarPreparedLiteralReown(t, "ltree", 3)
}

func testInstalledScalarPreparedLiteralReown(t *testing.T, family string, slot int) {
	var e *core.Engine
	var ext *catalog.Extension
	if family == "lquery" || family == "ltree" {
		e = engine.New()
		op, done := enginetest.RoleOperation(t, e, "postgres")
		enginetest.RoleExecute(t, e, op, "CREATE EXTENSION ltree")
		done()
		ext = e.Catalog.State.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "ltree"}))
	} else {
		e, ext = isnSQLInstalled(t)
	}
	base := e.CatalogRead().MustType(ext.Members[slot])
	inputs := []string{"1234567890120!", "0123456789012"}
	outputs := []string{"123-456789012-8!", "012-345678901-2", ""}
	constructor := sqltype.Ean13
	if family == "lquery" {
		inputs = []string{"Top.*{0,2}.sport*@.!football|tennis{1,}.Russ*|Spain", "foo{1}"}
		outputs = []string{"Top.*{,2}.sport@*.!football|tennis{1,}.Russ*|Spain", "foo{1}", ""}
		constructor = sqltype.Lquery
	}
	if family == "ltree" {
		inputs = []string{"Top.Countries.Europe", ""}
		outputs = []string{"Top.Countries.Europe", "", ""}
		constructor = sqltype.Ltree
	}
	if family == "isbn" {
		inputs = []string{"9780123456780!", "123456789X"}
		outputs = []string{"0-12-345678-9!", "1-234-56789-X", ""}
		constructor = sqltype.Isbn
	}
	if family == "ismn" {
		inputs = []string{"M123456780!", "9790123456785"}
		outputs = []string{"M-1234-5678-5!", "M-1234-5678-5", ""}
		constructor = sqltype.Ismn
	}
	if family == "ismn13" {
		inputs = []string{"M123456780!", "9790123456785"}
		outputs = []string{"979-0-1234-5678-5!", "979-0-1234-5678-5", ""}
		constructor = sqltype.Ismn13
	}
	if family == "issn" {
		inputs = []string{"9771234567898!", "12345679"}
		outputs = []string{"977-1234-567-89-8!", "1234-5679", ""}
		constructor = sqltype.Issn
	}
	if family == "issn13" {
		inputs = []string{"9771234567898!", "12345679"}
		outputs = []string{"977-1234-567-89-8!", "977-1234-567-00-3", ""}
		constructor = sqltype.Issn13
	}
	if family == "upc" {
		inputs = []string{"123456789010!", "220356483481"}
		outputs = []string{"123456789012!", "220356483481", ""}
		constructor = sqltype.Upc
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead()
	makeValue := func(t *testing.T, d *sqltype.Descriptor, text string) sqltype.Value {
		t.Helper()
		v, err := d.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	original := []analyze.TemporalConstant{
		{Offset: 7, TypeID: base.SQLType.ID(), Typmod: -1, Typed: true, TypedTypmod: -1, Value: makeValue(t, base.SQLType, inputs[0])},
		{Offset: 31, TypeID: base.SQLType.ID(), Typmod: -1, Value: makeValue(t, base.SQLType, inputs[1])},
		{Offset: 59, TypeID: base.SQLType.ID(), Typmod: -1, Value: sqltype.Null(base.SQLType)},
		{Offset: 83, TypeID: sqltype.Date, Typmod: -1, Scalar: 42},
	}
	saved := append([]analyze.TemporalConstant(nil), original...)
	enginetest.RoleExecute(t, e, op, "CREATE SCHEMA ean_literal")
	enginetest.RoleExecute(t, e, op, "ALTER TYPE public."+family+" RENAME TO moved")
	enginetest.RoleExecute(t, e, op, "ALTER TYPE public.moved SET SCHEMA ean_literal")
	current := e.CatalogRead().MustType(base.Address())
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	path, err := core.EngineSequenceEnvironmentLocked(e, auth)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	analysis := core.Analysis{
		Domain: e.Catalog.Domain, Epoch: auth.Catalog.Epoch(), Session: auth.Session.SessionToken,
		Private: auth.Session.Transactions.Catalog, Path: path, Namespace: auth.Catalog.State().NamespaceGeneration(),
		Types:        map[ids.ObjectAddress]*catalog.Type{base.Address(): base},
		Dependencies: []catalog.ExpressionDependency{{Ref: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), base.Address()), Revision: base.Revision()}},
	}
	key := analyze.PreparedTypeKey{Offset: 7, Kind: analyze.IdentityCast}
	shared := map[analyze.PreparedTypeKey]analyze.TypeOccurrence{key: {Kind: analyze.IdentityCast, Base: base.SQLType.Usage()}}
	expected := core.Description{Temporals: original, Types: shared, Analysis: &analysis, PlanGeneration: auth.Session.DiscardPlansGeneration}
	reown := func(a core.AuthorizationView, d *core.Description) []analyze.TemporalConstant {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		return core.EnginePreparedInstalledScalarLiteralTemporalsLocked(e, a, d, path)
	}
	assertRemapped := func(t *testing.T, got []analyze.TemporalConstant) {
		t.Helper()
		if len(got) != len(original) || &got[0] == &original[0] {
			t.Fatal("changed candidate must own its slice")
		}
		for i, text := range outputs {
			want := original[i]
			if i == 2 {
				want.Value = sqltype.Null(current.SQLType)
			} else {
				want.Value = makeValue(t, current.SQLType, text)
			}
			// SameDatum checks the inline payload, not just formatted output.
			if !sqltype.SameDatum(got[i].Value, want.Value) {
				t.Fatal("candidate changed payload or NULL framing", i)
			}
			want.Value = got[i].Value
			if !reflect.DeepEqual(got[i], want) {
				t.Fatal("candidate changed capture usage or position", i)
			}
		}
		if !reflect.DeepEqual(got[3], original[3]) || !reflect.DeepEqual(original, saved) {
			t.Fatal("candidate mutated unrelated or shared captures")
		}
	}
	assertRemapped(t, reown(auth, &expected))
	sameAnalysis := analysis
	sameAnalysis.Types = map[ids.ObjectAddress]*catalog.Type{base.Address(): current}
	sameAnalysis.Dependencies = []catalog.ExpressionDependency{{Ref: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), base.Address()), Revision: current.Revision()}}
	same := expected
	same.Analysis = &sameAnalysis
	same.Temporals = append([]analyze.TemporalConstant(nil), original...)
	for i := range 3 {
		if i == 2 {
			same.Temporals[i].Value = sqltype.Null(current.SQLType)
		} else {
			same.Temporals[i].Value = makeValue(t, current.SQLType, inputs[i])
		}
	}
	if got := reown(auth, &same); &got[0] != &same.Temporals[0] {
		t.Fatal("unchanged generation copied temporal slice")
	}
	foreign, err := constructor(base.SQLType.ID(), base.Name(), base.SQLType.SQLName())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*testing.T, *core.Description, *core.Analysis)
	}{
		{"missing source", func(_ *testing.T, _ *core.Description, a *core.Analysis) {
			a.Types = nil
		}},
		{"missing dependency", func(_ *testing.T, _ *core.Description, a *core.Analysis) {
			a.Dependencies = nil
		}},
		{"foreign dependency domain", func(_ *testing.T, _ *core.Description, a *core.Analysis) {
			a.Dependencies = []catalog.ExpressionDependency{{Ref: new(ids.Domain).Ref(auth.Catalog.Epoch(), base.Address()), Revision: base.Revision()}}
		}},
		{"foreign dependency epoch", func(_ *testing.T, _ *core.Description, a *core.Analysis) {
			a.Dependencies = []catalog.ExpressionDependency{{Ref: e.Catalog.Domain.Ref(auth.Catalog.Epoch()+1, base.Address()), Revision: base.Revision()}}
		}},
		{"alien source pointer", func(t *testing.T, d *core.Description, _ *core.Analysis) {
			d.Temporals[0].Value = makeValue(t, foreign, inputs[0])
		}},
		{"wrong type id", func(_ *testing.T, d *core.Description, _ *core.Analysis) { d.Temporals[0].TypeID++ }},
		{"discard", func(_ *testing.T, d *core.Description, _ *core.Analysis) {
			*(&d.PlanGeneration)++
		}},
		{"absent analysis", func(_ *testing.T, d *core.Description, _ *core.Analysis) {
			d.Analysis = nil
		}},
		{"retired", func(_ *testing.T, _ *core.Description, a *core.Analysis) {
			a.NeedsReanalysis = true
		}},
		{"domain", func(_ *testing.T, _ *core.Description, a *core.Analysis) {
			a.Domain = new(ids.Domain)
		}},
		{"epoch", func(_ *testing.T, _ *core.Description, a *core.Analysis) {
			*(&a.Epoch)++
		}},
		{"session", func(_ *testing.T, _ *core.Description, a *core.Analysis) {
			a.Session = ids.SessionToken{}
		}},
		{"private", func(_ *testing.T, _ *core.Description, a *core.Analysis) {
			a.Private = new(session.Journal)
		}},
		{"namespace", func(_ *testing.T, _ *core.Description, a *core.Analysis) {
			*(&a.Namespace)++
		}},
		{"path", func(_ *testing.T, _ *core.Description, a *core.Analysis) {
			(&a.Path).ImplicitCatalog = !a.Path.ImplicitCatalog
		}},
		{"cast", func(_ *testing.T, _ *core.Description, a *core.Analysis) {
			a.Casts = map[catalog.CastPair]*catalog.Cast{{Source: sqltype.Text, Target: sqltype.Int4}: new(catalog.Cast)}
		}},
		{"unrelated definition", func(_ *testing.T, _ *core.Description, a *core.Analysis) {
			a.Dependencies = append(append([]catalog.ExpressionDependency(nil), a.Dependencies...), catalog.ExpressionDependency{Ref: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), current.Namespace())})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, d := analysis, expected
			d.Analysis = &a
			d.Temporals = append([]analyze.TemporalConstant(nil), original...)
			tc.change(t, &d, &a)
			snapshot := append([]analyze.TemporalConstant(nil), d.Temporals...)
			got := reown(auth, &d)
			if &got[0] != &d.Temporals[0] || !reflect.DeepEqual(got, snapshot) || !reflect.DeepEqual(original, saved) {
				t.Fatal("invalid authority minted or mutated candidate")
			}
		})
	}
	for _, name := range []string{"owner/ACL", "missing destination", "physical", "extension", "array", "family", "procedure", "procedure membership", "companion", "manifest"} {
		t.Run(name, func(t *testing.T) {
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			destination := *current
			destination.ObjectRevision++
			switch name {
			case "owner/ACL":
				owner, ok := e.Roles().Lookup("pg_read_all_data")
				if !ok {
					t.Fatal("missing predefined owner role")
				}
				if err := role.StageOwnership(stage, current, owner.Ref); err != nil {
					t.Fatal(err)
				}
				// Use the ordinary ownership stage: its companion and synthetic
				// ACL must be valid before this can witness nominal authority.
				destination = *e.StageRead(stage).MustType(current.Address())
			case "missing destination":
				if err := stage.Remove(e.Catalog.Domain.Ref(auth.Catalog.Epoch(), current.Address())); err != nil {
					t.Fatal(err)
				}
			case "physical":
				destination.Dimensions++
			case "extension":
				destination.Extension = ids.ObjectAddress{}
			case "array":
				destination.Array++
			case "family":
				sibling := sqltype.Isbn
				if family == "isbn" {
					sibling = sqltype.Ean13
				}
				destination.SQLType, err = sibling(base.SQLType.ID(), current.Name(), current.SQLType.SQLName())
				if err != nil {
					t.Fatal(err)
				}
			case "procedure":
				proc := *auth.Catalog.MustProcedure(ext.Members[slot+1])
				proc.Strict = false
				if err := stage.Replace(&proc); err != nil {
					t.Fatal(err)
				}
			case "procedure membership":
				proc := *auth.Catalog.MustProcedure(ext.Members[slot+1])
				proc.Extension = ids.ObjectAddress{}
				if err := stage.Replace(&proc); err != nil {
					t.Fatal(err)
				}
			case "companion":
				array := *auth.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(current.Array)})
				array.Owner = authorization.PrincipalRef{}
				if err := stage.Replace(&array); err != nil {
					t.Fatal(err)
				}
			case "manifest":
				broken := *ext
				broken.Version = "invalid"
				if err := stage.Replace(&broken); err != nil {
					t.Fatal(err)
				}
			}
			if name != "missing destination" {
				if err := stage.Replace(&destination); err != nil {
					t.Fatal(err)
				}
			}
			a := auth
			a.Catalog = e.StageRead(stage)
			localAnalysis, description := analysis, expected
			description.Analysis = &localAnalysis
			description.Temporals = append([]analyze.TemporalConstant(nil), original...)
			got := reown(a, &description)
			if name == "owner/ACL" {
				role, err := extension.MemberTypeRole(a.Catalog, ext, a.Catalog.MustType(current.Address()))
				if err != nil || role != family {
					t.Fatal("owner change must preserve genuine installed authority", err)
				}
				if destination.Owner == current.Owner || authorization.EqualACL(destination.ACL, current.ACL) {
					t.Fatal("owner stage must change owner and ACL")
				}
				e.Mu.Lock()
				valid := core.EnginePreparedAnalysisValidLocked(e, a, &localAnalysis, path)
				e.Mu.Unlock()
				if valid {
					t.Fatal("owner/ACL delta reused executable analysis")
				}
				assertRemapped(t, got)
			} else if &got[0] != &description.Temporals[0] || !reflect.DeepEqual(got, saved) {
				t.Fatal("invalid destination granted datum authority")
			}
		})
	}
	t.Run("retirement", func(t *testing.T) {
		localAnalysis, description := analysis, expected
		description.Analysis = &localAnalysis
		candidate := description
		candidate.Temporals = reown(auth, &description)
		candidate.Types = description.RetireAnalysisTypeCastsForRebind(true)
		if !description.Analysis.NeedsReanalysis || &candidate.Temporals[0] == &original[0] {
			t.Fatal("retirement failed to isolate candidate")
		}
		if _, ok := description.Types[key]; ok || candidate.Types[key].Base.Type != base.SQLType || shared[key].Base.Type != base.SQLType {
			t.Fatal("retirement mutated shared captures or lost candidate identity")
		}
		// A failed attempt never publishes its slice or revives the old source cell.
		if got := reown(auth, &description); &got[0] != &original[0] || !reflect.DeepEqual(got, saved) {
			t.Fatal("failed rebind revived retired datum authority")
		}
	})
	if analysis.NeedsReanalysis || expected.Types[key].Base.Type != base.SQLType {
		t.Fatal("subtest retirement contaminated original analysis")
	}
	if before.MustType(base.Address()) != base || base.SQLType == current.SQLType || !reflect.DeepEqual(original, saved) {
		t.Fatal("candidate mutated retained catalog or statement roots")
	}
	if _, err := original[0].Value.Retag(current.SQLType); err == nil {
		t.Fatal("candidate import weakened ordinary Retag")
	}
	if _, err := typedef.ReownDomainDatum(original[0].Value, current.SQLType, nil, auth.Catalog.State().ScalarImports(), nil); err == nil {
		t.Fatal("candidate import escaped into catalog roots")
	}
}

package engine

import (
	"context"
	"errors"
	"maps"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func inheritedRestoreFixture(t *testing.T) (*Engine, *session.Operation, map[string]ids.ObjectRef, map[table.RestoredGenerationKey]table.RestoredGenerationRecipe) {
	t.Helper()
	source := New()
	ownershipUnitExec(t, source, "CREATE ROLE captured_owner")
	e := source.restoreStage()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	declarations := []string{
		"CREATE TABLE public.p(a INT,g INT GENERATED ALWAYS AS(NULL) VIRTUAL)",
		"CREATE TABLE public.q(a INT)",
		"CREATE TABLE public.c(CONSTRAINT local_nn NOT NULL a) INHERITS(public.p,public.q)",
		"CREATE TABLE public.unrelated(a INT)",
	}
	refs := make(map[string]ids.ObjectRef)
	origins := make(map[table.RestoredGenerationKey]table.RestoredGenerationRecipe)
	for _, sql := range declarations {
		raw := statement(t, sql).(*parser.CreateTable)
		roleExecute(t, e, op, sql)
		auth, err := operationAuthorization(op)
		if err != nil {
			t.Fatal(err)
		}
		ref, err := e.resolveRelationReference(auth, raw.Name, false)
		if err != nil {
			t.Fatal(err)
		}
		refs[raw.Name.Parts[len(raw.Name.Parts)-1].Text] = ref
		table.CaptureRestoredPlaceholders(e.CatalogRead(), ref, raw, origins)
	}
	return e, op, refs, origins
}

func TestCastDefinitionRestoreCheckAdmission(t *testing.T) {
	e, op, _, _ := inheritedRestoreFixture(t)
	raw := statement(t, "ALTER TABLE public.unrelated ADD CONSTRAINT positive CHECK(a>0)").(*parser.AlterTable)
	before := e.catalog.State
	if err := e.restoreCheck(op, raw, nil); err != nil || e.catalog.State != before {
		t.Fatal("ordinary CHECK admission must be quiet and need no generation repair", err)
	}
	// The private replay admission never grants ordinary execution importer
	// authority, and inheriting tables still need the real repair recipe.
	importTargets := e.importTargets
	e.importTargets = nil
	if err := e.restoreCheck(op, raw, nil); err == nil {
		t.Fatal("valid CHECK admitted outside the private importer")
	}
	e.importTargets = importTargets
	inherited := statement(t, "ALTER TABLE public.c ADD CONSTRAINT positive CHECK(a>0)").(*parser.AlterTable)
	if err := e.restoreCheck(op, inherited, nil); err == nil {
		t.Fatal("inherited CHECK admitted without generation repair")
	}
}

func TestCastDefinitionRestoreLocalCheckAdmission(t *testing.T) {
	e, op, refs, _ := inheritedRestoreFixture(t)
	const declaration = "ALTER TABLE ONLY public.c ADD CONSTRAINT local_positive CHECK(a>0) NO INHERIT"
	raw := statement(t, declaration).(*parser.AlterTable)
	before := e.catalog.State
	if err := e.restoreCheck(op, raw, nil); err != nil || e.catalog.State != before {
		t.Fatal("fresh local NO INHERIT CHECK admission must be effect-free", err)
	}
	for _, mutate := range []func(*parser.AlterTable){
		func(s *parser.AlterTable) { s.Only = false },
		func(s *parser.AlterTable) {
			s.Name.Parts[1].Text = "p"
			s.Actions[0].Check.NoInherit = false
		},
		func(s *parser.AlterTable) { s.Actions[0].Check.NotValid = true },
		func(s *parser.AlterTable) { s.IfExists = true },
		func(s *parser.AlterTable) { s.Name.Parts[1].Text = "unrelated" },
	} {
		candidate := statement(t, declaration).(*parser.AlterTable)
		mutate(candidate)
		if err := e.restoreCheck(op, candidate, nil); err == nil || e.catalog.State != before {
			t.Fatal("non-local replay shape gained admission", candidate, err)
		}
	}
	roleExecute(t, e, op, declaration)
	child := e.CatalogRead().MustRelation(refs["c"].Address)
	local, exists := child.Inheritance.Checks["local_positive"]
	if !exists || !local.Local || len(local.Origins) != 0 {
		t.Fatal("local replay gained inherited origins", local)
	}
	found := false
	for _, check := range child.Constraints.Check {
		if check.Name == "local_positive" {
			found = true
			if !check.NoInherit || check.NotValid {
				t.Fatal("local replay changed CHECK flags", check)
			}
		}
	}
	if !found {
		t.Fatal("ordinary ALTER did not install local CHECK")
	}
	if err := e.restoreCheck(op, raw, nil); err == nil {
		t.Fatal("existing CHECK admitted as a fresh local definition")
	}
	roleExecute(t, e, op, "ALTER TABLE public.p ADD CONSTRAINT inherited_check CHECK(a>-5)")
	inherited := statement(t, "ALTER TABLE ONLY public.c ADD CONSTRAINT inherited_check CHECK(a>-5) NO INHERIT").(*parser.AlterTable)
	if err := e.restoreCheck(op, inherited, nil); err == nil {
		t.Fatal("inherited CHECK admitted for merge into local definition")
	}
	// A leaf child can also own a local inheritable CHECK. Preserve its
	// original NO INHERIT=false rather than changing catalog semantics.
	const leafDeclaration = "ALTER TABLE ONLY public.c ADD CONSTRAINT leaf_check CHECK(a<10)"
	leaf := statement(t, leafDeclaration).(*parser.AlterTable)
	before = e.catalog.State
	if err := e.restoreCheck(op, leaf, nil); err != nil || e.catalog.State != before {
		t.Fatal("fresh leaf local inheritable CHECK rejected", err)
	}
	roleExecute(t, e, op, leafDeclaration)
	child = e.CatalogRead().MustRelation(refs["c"].Address)
	local, exists = child.Inheritance.Checks["leaf_check"]
	if !exists || !local.Local || len(local.Origins) != 0 {
		t.Fatal("leaf CHECK gained inherited origins", local)
	}
	found = false
	for _, check := range child.Constraints.Check {
		if check.Name == "leaf_check" {
			found = true
			if check.NoInherit || check.NotValid {
				t.Fatal("leaf CHECK lost original inheritance or validity flag", check)
			}
		}
	}
	if !found {
		t.Fatal("ordinary ALTER did not install leaf CHECK")
	}
}

func TestCastDefinitionRestoreRecursiveCheckAdmission(t *testing.T) {
	e, op, refs, _ := inheritedRestoreFixture(t)
	for _, declaration := range []string{
		"CREATE TABLE public.rp(a INT)",
		"CREATE TABLE public.rc() INHERITS(public.rp)",
		"CREATE TABLE public.rg() INHERITS(public.rc)",
	} {
		raw := statement(t, declaration).(*parser.CreateTable)
		roleExecute(t, e, op, declaration)
		auth, err := operationAuthorization(op)
		if err != nil {
			t.Fatal(err)
		}
		ref, err := e.resolveRelationReference(auth, raw.Name, false)
		if err != nil {
			t.Fatal(err)
		}
		refs[raw.Name.Parts[1].Text] = ref
	}
	const declaration = "ALTER TABLE public.rp ADD CONSTRAINT recursive_positive CHECK(a>0)"
	raw := statement(t, declaration).(*parser.AlterTable)
	before := e.catalog.State
	unrelated := table.RestoredGenerationRecipe{Ref: refs["unrelated"]}
	if err := e.restoreCheck(op, raw, []table.RestoredGenerationRecipe{unrelated}); err != nil || e.catalog.State != before {
		t.Fatal("fresh recursive admission must ignore unrelated repairs and publish nothing", err)
	}
	roleExecute(t, e, op, "ALTER TABLE ONLY public.rc ADD CONSTRAINT collision CHECK(a<10) NO INHERIT")
	collision := statement(t, "ALTER TABLE public.rp ADD CONSTRAINT collision CHECK(a<10)").(*parser.AlterTable)
	before = e.catalog.State
	if err := e.restoreCheck(op, collision, nil); err == nil || e.catalog.State != before {
		t.Fatal("descendant name collision admitted recursive merge", err)
	}
	roleExecute(t, e, op, declaration)
	for _, edge := range [][2]string{{"rc", "rp"}, {"rg", "rc"}} {
		child := e.CatalogRead().MustRelation(refs[edge[0]].Address)
		provenance, exists := child.Inheritance.Checks["recursive_positive"]
		if !exists || provenance.Local || len(provenance.Origins) != 1 || provenance.Origins[0].Parent != refs[edge[1]].Address {
			t.Fatal("recursive ALTER did not retain exact inherited provenance", edge, provenance)
		}
		found := false
		for _, check := range child.Constraints.Check {
			if check.Name == "recursive_positive" {
				found = true
				if check.NoInherit || check.NotValid {
					t.Fatal("recursive CHECK changed inheritance or validity flag", check)
				}
			}
		}
		if !found {
			t.Fatal("ordinary recursive ALTER omitted descendant CHECK", edge)
		}
	}
	if err := e.restoreCheck(op, raw, nil); err == nil {
		t.Fatal("existing root name admitted recursive merge")
	}
}

func TestCastDefinitionPlainGenerationRepair(t *testing.T) {
	e, op, _, _ := inheritedRestoreFixture(t)
	const declaration = "CREATE TABLE public.plain_generated(a INT,g INT GENERATED ALWAYS AS(NULL) STORED)"
	created := statement(t, declaration).(*parser.CreateTable)
	roleExecute(t, e, op, declaration)
	auth, err := operationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := e.resolveRelationReference(auth, created.Name, false)
	if err != nil {
		t.Fatal(err)
	}
	view := e.CatalogRead()
	tbl := view.MustRelation(ref.Address)
	attribute := tbl.Attributes[tbl.Index["g"]]
	key := table.RestoredGenerationKey{Relation: ref.Address, Attribute: attribute}
	origins := make(map[table.RestoredGenerationKey]table.RestoredGenerationRecipe)
	table.CaptureRestoredPlaceholders(view, ref, created, origins)
	record, exists := origins[key]
	if !exists || record.Attribute != attribute || record.Ref != ref || record.Kind != catalog.GenerationStored {
		t.Fatal("plain NULL generation capture lost exact physical identity", record)
	}
	const alteration = "ALTER TABLE ONLY public.plain_generated ALTER g SET EXPRESSION AS(a+1)"
	raw := statement(t, alteration).(*parser.AlterTable)
	before := e.catalog.State
	if err := e.pairRestoredGeneration(op, raw, origins); err != nil || e.catalog.State != before {
		t.Fatal("genuine plain generation repair pairing rejected or published", err)
	}
	bad := maps.Clone(origins)
	record.Attribute++
	bad[key] = record
	if err := e.pairRestoredGeneration(op, raw, bad); err == nil || e.catalog.State != before {
		t.Fatal("mismatched generation attribute gained pairing authority", err)
	}
	if err := e.pairRestoredGeneration(op, raw, nil); err == nil {
		t.Fatal("unpaired plain generation repair admitted")
	}
	roleExecute(t, e, op, alteration)
	repaired, err := e.captureRestoredGeneration(op, raw)
	if err != nil || repaired.Ref != ref || repaired.Attribute != attribute || repaired.Kind != catalog.GenerationStored {
		t.Fatal("real plain generation repair lost identity or storage kind", repaired, err)
	}
}

func TestRestoreInheritedDefinitionChecks(t *testing.T) {
	e, op, refs, origins := inheritedRestoreFixture(t)
	raw := statement(t, "ALTER TABLE ONLY public.p ALTER g SET EXPRESSION AS(a+1)").(*parser.AlterTable)
	key := table.RestoredGenerationKey{Relation: refs["p"].Address, Attribute: 2}
	if len(origins) != 2 {
		t.Fatal("actual root/inherited typed-NULL declarations were not captured", origins)
	}
	if err := e.pairRestoredGeneration(op, raw, origins); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*table.RestoredGenerationRecipe){
		"epoch":     func(r *table.RestoredGenerationRecipe) { r.Ref.Epoch++ },
		"owner":     func(r *table.RestoredGenerationRecipe) { r.Ref = New().catalog.Domain.Ref(r.Ref.Epoch, r.Ref.Address) },
		"attribute": func(r *table.RestoredGenerationRecipe) { r.Attribute++ },
		"identity":  func(r *table.RestoredGenerationRecipe) { r.Generation++ },
		"kind":      func(r *table.RestoredGenerationRecipe) { r.Kind = catalog.GenerationStored },
		"usage":     func(r *table.RestoredGenerationRecipe) { r.Usage = testUsage(e, sqltype.Int8) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := maps.Clone(origins)
			record := bad[key]
			mutate(&record)
			bad[key] = record
			before := e.catalog.State
			if err := e.pairRestoredGeneration(op, raw, bad); err == nil || e.catalog.State != before {
				t.Fatal("mismatched generation gained authority", err)
			}
		})
	}
	missing := maps.Clone(origins)
	delete(missing, key)
	if err := e.pairRestoredGeneration(op, raw, missing); err == nil {
		t.Fatal("unpaired generation accepted")
	}
	// Perform the genuine supported repair and capture its actual final identity.
	roleExecute(t, e, op, "ALTER TABLE ONLY public.p ALTER g SET EXPRESSION AS(a+1)")
	repair, err := e.captureRestoredGeneration(op, raw)
	if err != nil {
		t.Fatal(err)
	}
	check := statement(t, "ALTER TABLE public.q ADD CONSTRAINT positive CHECK(a>0)").(*parser.AlterTable)
	if err := e.restoreCheck(op, check, []table.RestoredGenerationRecipe{repair}); err != nil {
		t.Fatal("actual connected other-parent CHECK rejected", err)
	}
	// Start every private rejection with an admitted parser shape. Unsupported
	// public syntax must not fail in statement() before this recognizer runs.
	const admitted = "ALTER TABLE public.q ADD CONSTRAINT positive CHECK(a>0)"
	for _, test := range []struct {
		name   string
		change func(*parser.AlterTable)
	}{
		{"missing", func(raw *parser.AlterTable) { raw.Name.Parts[len(raw.Name.Parts)-1].Text = "missing" }},
		{"only", func(raw *parser.AlterTable) { raw.Only = true }},
		{"if_exists", func(raw *parser.AlterTable) { raw.IfExists = true }},
		{"parameter", func(raw *parser.AlterTable) { raw.Actions[0].Check.Expression = parser.ParameterExpr{Number: 1} }},
		{"multiple_actions", func(raw *parser.AlterTable) {
			other := statement(t, "ALTER TABLE public.q ADD CONSTRAINT other CHECK(a<10)").(*parser.AlterTable)
			raw.Actions = append(raw.Actions, other.Actions[0])
		}},
		{"not_valid", func(raw *parser.AlterTable) { raw.Actions[0].Check.NotValid = true }},
	} {
		candidate := statement(t, admitted).(*parser.AlterTable)
		test.change(candidate)
		before := e.catalog.State
		if err := e.restoreCheck(op, candidate, []table.RestoredGenerationRecipe{repair}); err == nil || e.catalog.State != before {
			t.Fatal("unpaired CHECK shape admitted", test.name, err)
		}
	}
	foreign := repair
	foreign.Ref = New().catalog.Domain.Ref(repair.Ref.Epoch, repair.Ref.Address)
	for _, bad := range []table.RestoredGenerationRecipe{
		{Ref: repair.Ref, Attribute: repair.Attribute, Generation: repair.Generation + 1, Usage: repair.Usage, Kind: repair.Kind},
		foreign,
		{Ref: repair.Ref, Attribute: repair.Attribute, Generation: repair.Generation, Usage: testUsage(e, sqltype.Int8), Kind: repair.Kind},
	} {
		if err := e.restoreCheck(op, check, []table.RestoredGenerationRecipe{bad}); err == nil {
			t.Fatal("foreign definition authorized CHECK", bad)
		}
	}
	// Real inherited origins, not mere graph connectivity, remain mandatory.
	before := e.catalog.State
	child := *before.MustRelation(refs["c"].Address)
	child.Inheritance = catalog.CloneInheritance(child.Inheritance)
	child.Inheritance.Columns[2] = catalog.InheritedColumn{Origins: []catalog.InheritanceOrigin{{Parent: refs["q"].Address, Attribute: 1}}}
	changed := catalog.CloneState(before)
	changed.SetDescriptor(child.Address(), &child)
	e.catalog.PublishLocked(changed)
	childRaw := statement(t, "ALTER TABLE ONLY public.c ALTER g SET EXPRESSION AS(a+1)").(*parser.AlterTable)
	if err := e.pairRestoredGeneration(op, childRaw, origins); err == nil || e.catalog.State != changed {
		t.Fatal("forged generation origin accepted", err)
	}
	e.catalog.PublishLocked(before)
	targets := e.importTargets
	e.importTargets = nil
	if err := e.restoreCheck(op, check, []table.RestoredGenerationRecipe{repair}); err == nil {
		t.Fatal("ordinary CHECK gained importer authority")
	}
	e.importTargets = targets
	canceled, cancel := context.WithCancel(context.Background())
	cancelOp, done, err := testOperation(e, canceled)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	cancel()
	if err := e.restoreCheck(cancelOp, check, []table.RestoredGenerationRecipe{repair}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled CHECK gained authority", err)
	}
	roleExecute(t, e, op, "ALTER TABLE public.q ADD CONSTRAINT positive CHECK(a>0)")
	current := e.CatalogRead().MustRelation(refs["c"].Address)
	if provenance := current.Inheritance.Checks["positive"]; provenance.Local || len(provenance.Origins) != 1 || provenance.Origins[0].Parent != refs["q"].Address {
		t.Fatal("actual ADD did not rebuild exact other-parent support", provenance)
	}
	retained, err := e.closeRestoredRecipesForData(op, statement(t, "INSERT INTO public.c(a) VALUES(1)").(*parser.Insert), []table.RestoredGenerationRecipe{repair})
	if err != nil || len(retained) != 0 {
		t.Fatal("data did not close actual component repair lifetime", retained, err)
	}
}

func TestRestoreInheritedOwnerPair(t *testing.T) {
	e, op, refs, origins := inheritedRestoreFixture(t)
	owner, ok := e.Roles().Lookup("captured_owner")
	if !ok {
		t.Fatal("missing captured role")
	}
	pair, err := e.captureInheritedOwner(op, refs["c"], owner.Ref)
	if err != nil {
		t.Fatal(err)
	}
	raw := statement(t, "ALTER TABLE public.c OWNER TO captured_owner").(*parser.AlterOwner)
	if err := e.restoreInheritedOwner(op, raw, pair); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"ALTER TABLE public.unrelated OWNER TO captured_owner",
		"ALTER TABLE public.c OWNER TO postgres",
		"ALTER TABLE c OWNER TO captured_owner",
		"ALTER TABLE public.c OWNER TO CURRENT_USER",
	} {
		before := e.catalog.State
		if err := e.restoreInheritedOwner(op, statement(t, sql).(*parser.AlterOwner), pair); err == nil || e.catalog.State != before {
			t.Fatal("foreign owner target accepted", sql, err)
		}
	}
	for name, mutate := range map[string]func(*table.RestoredInheritedOwner){
		"missing": func(r *table.RestoredInheritedOwner) { *r = table.RestoredInheritedOwner{} },
		"epoch":   func(r *table.RestoredInheritedOwner) { r.Ref.Epoch++ },
		"creator": func(r *table.RestoredInheritedOwner) { r.Creator = owner.Ref },
		"target":  func(r *table.RestoredInheritedOwner) { r.Ref = refs["unrelated"] },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := pair
			mutate(&candidate)
			before := e.catalog.State
			if err := e.restoreInheritedOwner(op, raw, candidate); err == nil || e.catalog.State != before {
				t.Fatal("mismatched owner identity admitted", err)
			}
		})
	}
	targets := e.importTargets
	e.importTargets = nil
	if err := e.restoreInheritedOwner(op, raw, pair); err == nil {
		t.Fatal("ordinary OWNER gained importer authority")
	}
	e.importTargets = targets
	before := e.CatalogRead().MustRelation(refs["c"].Address)
	nn := table.RestoredNotNull{Ref: refs["c"], Attribute: 1, Constraint: before.Constraints.NotNull[0].OID}
	roleExecute(t, e, op, "ALTER TABLE public.c OWNER TO captured_owner")
	after := e.CatalogRead().MustRelation(refs["c"].Address)
	if after.Owner != owner.Ref || before.Owner == owner.Ref || after.Constraints.NotNull[0].OID != nn.Constraint {
		t.Fatal("actual owner transfer changed source capture or physical NN identity")
	}
	if err := e.restoreInheritedOwner(op, raw, pair); err == nil {
		t.Fatal("consumed owner pairing reused")
	}
	generation := statement(t, "ALTER TABLE ONLY public.c ALTER g SET EXPRESSION AS(a+1)").(*parser.AlterTable)
	if err := e.pairRestoredGeneration(op, generation, origins); err != nil {
		t.Fatal("legal owner metadata copy invalidated actual generation identity", err)
	}
	if err := e.restoreNoInheritNotNull(op, restoreNNRaw("public.c", "a", "local_nn"), nn); err != nil {
		t.Fatal("legal owner metadata copy invalidated actual NN identity", err)
	}
	if current := e.CatalogRead().MustRelation(refs["c"].Address); current.Owner != owner.Ref || !current.Constraints.NotNull[0].NoInherit || current.Constraints.NotNull[0].OID != nn.Constraint {
		t.Fatal("paired NN repair changed final captured owner/identity")
	}
}

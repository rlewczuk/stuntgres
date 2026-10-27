package partitioncmds_test

import (
	"context"
	"errors"
	"maps"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partitioncmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func inheritedRestoreFixture(t *testing.T) (*core.Engine, *session.Operation, map[string]ids.ObjectRef, map[table.RestoredGenerationKey]table.RestoredGenerationRecipe) {
	t.Helper()
	source := engine.New()
	ownershipUnitExec(t, source, "CREATE ROLE captured_owner")
	e := source.RestoreStage()
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
		raw := enginetest.Statement(t, sql).(*parser.CreateTable)
		roleExecute(t, e, op, sql)
		auth, err := core.OperationAuthorization(op)
		if err != nil {
			t.Fatal(err)
		}
		ref, err := e.ResolveRelationReference(auth, raw.Name, false)
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
	raw := enginetest.Statement(t, "ALTER TABLE public.unrelated ADD CONSTRAINT positive CHECK(a>0)").(*parser.AlterTable)
	before := e.Catalog.State
	if err := partitioncmds.RestoreCheck(e, op, raw, nil); err != nil || e.Catalog.State != before {
		t.Fatal("ordinary CHECK admission must be quiet and need no generation repair", err)
	}
	// Fresh leaf-local CHECKs use ordinary ALTER, not generation-repair
	// authority. They still require the isolated importer.
	importTargets := e.ImportTargets
	e.ImportTargets = nil
	if err := partitioncmds.RestoreCheck(e, op, raw, nil); err == nil {
		t.Fatal("valid CHECK admitted outside the private importer")
	}
	e.ImportTargets = importTargets
	inherited := enginetest.Statement(t, "ALTER TABLE public.c ADD CONSTRAINT positive CHECK(a>0)").(*parser.AlterTable)
	if err := partitioncmds.RestoreCheck(e, op, inherited, nil); err != nil || e.Catalog.State != before {
		t.Fatal("fresh leaf CHECK must need no generation repair", err)
	}
	roleExecute(t, e, op, "ALTER TABLE public.p ADD CONSTRAINT positive CHECK(a>0)")
	before = e.Catalog.State
	if err := partitioncmds.RestoreCheck(e, op, inherited, nil); err == nil || e.Catalog.State != before {
		t.Fatal("unpaired inherited CHECK merge gained fresh-definition authority", err)
	}
}

func TestCastDefinitionRestoreLocalCheckAdmission(t *testing.T) {
	e, op, refs, _ := inheritedRestoreFixture(t)
	const declaration = "ALTER TABLE ONLY public.c ADD CONSTRAINT local_positive CHECK(a>0) NO INHERIT"
	raw := enginetest.Statement(t, declaration).(*parser.AlterTable)
	before := e.Catalog.State
	if err := partitioncmds.RestoreCheck(e, op, raw, nil); err != nil || e.Catalog.State != before {
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
		candidate := enginetest.Statement(t, declaration).(*parser.AlterTable)
		mutate(candidate)
		if err := partitioncmds.RestoreCheck(e, op, candidate, nil); err == nil || e.Catalog.State != before {
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
	if err := partitioncmds.RestoreCheck(e, op, raw, nil); err == nil {
		t.Fatal("existing CHECK admitted as a fresh local definition")
	}
	roleExecute(t, e, op, "ALTER TABLE public.p ADD CONSTRAINT inherited_check CHECK(a>-5)")
	inherited := enginetest.Statement(t, "ALTER TABLE ONLY public.c ADD CONSTRAINT inherited_check CHECK(a>-5) NO INHERIT").(*parser.AlterTable)
	if err := partitioncmds.RestoreCheck(e, op, inherited, nil); err == nil {
		t.Fatal("inherited CHECK admitted for merge into local definition")
	}
	// A leaf child can also own a local inheritable CHECK. Preserve its
	// original NO INHERIT=false rather than changing catalog semantics.
	const leafDeclaration = "ALTER TABLE ONLY public.c ADD CONSTRAINT leaf_check CHECK(a<10)"
	leaf := enginetest.Statement(t, leafDeclaration).(*parser.AlterTable)
	before = e.Catalog.State
	if err := partitioncmds.RestoreCheck(e, op, leaf, nil); err != nil || e.Catalog.State != before {
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
		raw := enginetest.Statement(t, declaration).(*parser.CreateTable)
		roleExecute(t, e, op, declaration)
		auth, err := core.OperationAuthorization(op)
		if err != nil {
			t.Fatal(err)
		}
		ref, err := e.ResolveRelationReference(auth, raw.Name, false)
		if err != nil {
			t.Fatal(err)
		}
		refs[raw.Name.Parts[1].Text] = ref
	}
	const declaration = "ALTER TABLE public.rp ADD CONSTRAINT recursive_positive CHECK(a>0)"
	raw := enginetest.Statement(t, declaration).(*parser.AlterTable)
	before := e.Catalog.State
	unrelated := table.RestoredGenerationRecipe{Ref: refs["unrelated"]}
	if err := partitioncmds.RestoreCheck(e, op, raw, []table.RestoredGenerationRecipe{unrelated}); err != nil || e.Catalog.State != before {
		t.Fatal("fresh recursive admission must ignore unrelated repairs and publish nothing", err)
	}
	roleExecute(t, e, op, "ALTER TABLE ONLY public.rc ADD CONSTRAINT collision CHECK(a<10) NO INHERIT")
	collision := enginetest.Statement(t, "ALTER TABLE public.rp ADD CONSTRAINT collision CHECK(a<10)").(*parser.AlterTable)
	before = e.Catalog.State
	if err := partitioncmds.RestoreCheck(e, op, collision, nil); err == nil || e.Catalog.State != before {
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
	if err := partitioncmds.RestoreCheck(e, op, raw, nil); err == nil {
		t.Fatal("existing root name admitted recursive merge")
	}
}

func TestCastDefinitionPlainGenerationRepair(t *testing.T) {
	e, op, _, _ := inheritedRestoreFixture(t)
	const declaration = "CREATE TABLE public.plain_generated(a INT,g INT GENERATED ALWAYS AS(NULL) STORED)"
	created := enginetest.Statement(t, declaration).(*parser.CreateTable)
	roleExecute(t, e, op, declaration)
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := e.ResolveRelationReference(auth, created.Name, false)
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
	raw := enginetest.Statement(t, alteration).(*parser.AlterTable)
	before := e.Catalog.State
	if err := partitioncmds.PairRestoredGeneration(e, op, raw, origins); err != nil || e.Catalog.State != before {
		t.Fatal("genuine plain generation repair pairing rejected or published", err)
	}
	bad := maps.Clone(origins)
	record.Attribute++
	bad[key] = record
	if err := partitioncmds.PairRestoredGeneration(e, op, raw, bad); err == nil || e.Catalog.State != before {
		t.Fatal("mismatched generation attribute gained pairing authority", err)
	}
	if err := partitioncmds.PairRestoredGeneration(e, op, raw, nil); err == nil {
		t.Fatal("unpaired plain generation repair admitted")
	}
	roleExecute(t, e, op, alteration)
	repaired, err := partitioncmds.CaptureRestoredGeneration(e, op, raw)
	if err != nil || repaired.Ref != ref || repaired.Attribute != attribute || repaired.Kind != catalog.GenerationStored {
		t.Fatal("real plain generation repair lost identity or storage kind", repaired, err)
	}
}

func TestRestoreInheritedDefinitionChecks(t *testing.T) {
	e, op, refs, origins := inheritedRestoreFixture(t)
	raw := enginetest.Statement(t, "ALTER TABLE ONLY public.p ALTER g SET EXPRESSION AS(a+1)").(*parser.AlterTable)
	key := table.RestoredGenerationKey{Relation: refs["p"].Address, Attribute: 2}
	if len(origins) != 2 {
		t.Fatal("actual root/inherited typed-NULL declarations were not captured", origins)
	}
	if err := partitioncmds.PairRestoredGeneration(e, op, raw, origins); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*table.RestoredGenerationRecipe){
		"epoch": func(r *table.RestoredGenerationRecipe) { r.Ref.Epoch++ },
		"owner": func(r *table.RestoredGenerationRecipe) {
			r.Ref = engine.New().Catalog.Domain.Ref(r.Ref.Epoch, r.Ref.Address)
		},
		"attribute": func(r *table.RestoredGenerationRecipe) { r.Attribute++ },
		"identity":  func(r *table.RestoredGenerationRecipe) { r.Generation++ },
		"kind":      func(r *table.RestoredGenerationRecipe) { r.Kind = catalog.GenerationStored },
		"usage":     func(r *table.RestoredGenerationRecipe) { r.Usage = enginetest.Usage(e, sqltype.Int8) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := maps.Clone(origins)
			record := bad[key]
			mutate(&record)
			bad[key] = record
			before := e.Catalog.State
			if err := partitioncmds.PairRestoredGeneration(e, op, raw, bad); err == nil || e.Catalog.State != before {
				t.Fatal("mismatched generation gained authority", err)
			}
		})
	}
	missing := maps.Clone(origins)
	delete(missing, key)
	if err := partitioncmds.PairRestoredGeneration(e, op, raw, missing); err == nil {
		t.Fatal("unpaired generation accepted")
	}
	// Perform the genuine supported repair and capture its actual final identity.
	roleExecute(t, e, op, "ALTER TABLE ONLY public.p ALTER g SET EXPRESSION AS(a+1)")
	repair, err := partitioncmds.CaptureRestoredGeneration(e, op, raw)
	if err != nil {
		t.Fatal(err)
	}
	check := enginetest.Statement(t, "ALTER TABLE public.q ADD CONSTRAINT positive CHECK(a>0)").(*parser.AlterTable)
	if err := partitioncmds.RestoreCheck(e, op, check, []table.RestoredGenerationRecipe{repair}); err != nil {
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
			other := enginetest.Statement(t, "ALTER TABLE public.q ADD CONSTRAINT other CHECK(a<10)").(*parser.AlterTable)
			raw.Actions = append(raw.Actions, other.Actions[0])
		}},
		{"not_valid", func(raw *parser.AlterTable) { raw.Actions[0].Check.NotValid = true }},
	} {
		candidate := enginetest.Statement(t, admitted).(*parser.AlterTable)
		test.change(candidate)
		before := e.Catalog.State
		if err := partitioncmds.RestoreCheck(e, op, candidate, []table.RestoredGenerationRecipe{repair}); err == nil || e.Catalog.State != before {
			t.Fatal("unpaired CHECK shape admitted", test.name, err)
		}
	}
	foreign := repair
	foreign.Ref = engine.New().Catalog.Domain.Ref(repair.Ref.Epoch, repair.Ref.Address)
	for _, bad := range []table.RestoredGenerationRecipe{
		{Ref: repair.Ref, Attribute: repair.Attribute, Generation: repair.Generation + 1, Usage: repair.Usage, Kind: repair.Kind},
		foreign,
		{Ref: repair.Ref, Attribute: repair.Attribute, Generation: repair.Generation, Usage: enginetest.Usage(e, sqltype.Int8), Kind: repair.Kind},
	} {
		if err := partitioncmds.RestoreCheck(e, op, check, []table.RestoredGenerationRecipe{bad}); err == nil {
			t.Fatal("foreign definition authorized CHECK", bad)
		}
	}
	// Real inherited origins, not mere graph connectivity, remain mandatory.
	before := e.Catalog.State
	child := *before.MustRelation(refs["c"].Address)
	child.Inheritance = catalog.CloneInheritance(child.Inheritance)
	child.Inheritance.Columns[2] = catalog.InheritedColumn{Origins: []catalog.InheritanceOrigin{{Parent: refs["q"].Address, Attribute: 1}}}
	changed := catalog.CloneState(before)
	changed.SetDescriptor(child.Address(), &child)
	e.Catalog.PublishLocked(changed)
	childRaw := enginetest.Statement(t, "ALTER TABLE ONLY public.c ALTER g SET EXPRESSION AS(a+1)").(*parser.AlterTable)
	if err := partitioncmds.PairRestoredGeneration(e, op, childRaw, origins); err == nil || e.Catalog.State != changed {
		t.Fatal("forged generation origin accepted", err)
	}
	e.Catalog.PublishLocked(before)
	targets := e.ImportTargets
	e.ImportTargets = nil
	if err := partitioncmds.RestoreCheck(e, op, check, []table.RestoredGenerationRecipe{repair}); err == nil {
		t.Fatal("ordinary CHECK gained importer authority")
	}
	e.ImportTargets = targets
	canceled, cancel := context.WithCancel(context.Background())
	cancelOp, done, err := enginetest.Operation(e, canceled)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	cancel()
	if err := partitioncmds.RestoreCheck(e, cancelOp, check, []table.RestoredGenerationRecipe{repair}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled CHECK gained authority", err)
	}
	roleExecute(t, e, op, "ALTER TABLE public.q ADD CONSTRAINT positive CHECK(a>0)")
	current := e.CatalogRead().MustRelation(refs["c"].Address)
	if provenance := current.Inheritance.Checks["positive"]; provenance.Local || len(provenance.Origins) != 1 || provenance.Origins[0].Parent != refs["q"].Address {
		t.Fatal("actual ADD did not rebuild exact other-parent support", provenance)
	}
	retained, err := partitioncmds.CloseRestoredRecipesForData(e, op, enginetest.Statement(t, "INSERT INTO public.c(a) VALUES(1)").(*parser.Insert), []table.RestoredGenerationRecipe{repair})
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
	pair, err := partitioncmds.CaptureInheritedOwner(e, op, refs["c"], owner.Ref)
	if err != nil {
		t.Fatal(err)
	}
	raw := enginetest.Statement(t, "ALTER TABLE public.c OWNER TO captured_owner").(*parser.AlterOwner)
	if err := partitioncmds.RestoreInheritedOwner(e, op, raw, pair); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"ALTER TABLE public.unrelated OWNER TO captured_owner",
		"ALTER TABLE public.c OWNER TO postgres",
		"ALTER TABLE c OWNER TO captured_owner",
		"ALTER TABLE public.c OWNER TO CURRENT_USER",
	} {
		before := e.Catalog.State
		if err := partitioncmds.RestoreInheritedOwner(e, op, enginetest.Statement(t, sql).(*parser.AlterOwner), pair); err == nil || e.Catalog.State != before {
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
			before := e.Catalog.State
			if err := partitioncmds.RestoreInheritedOwner(e, op, raw, candidate); err == nil || e.Catalog.State != before {
				t.Fatal("mismatched owner identity admitted", err)
			}
		})
	}
	targets := e.ImportTargets
	e.ImportTargets = nil
	if err := partitioncmds.RestoreInheritedOwner(e, op, raw, pair); err == nil {
		t.Fatal("ordinary OWNER gained importer authority")
	}
	e.ImportTargets = targets
	before := e.CatalogRead().MustRelation(refs["c"].Address)
	nn := table.RestoredNotNull{Ref: refs["c"], Attribute: 1, Constraint: before.Constraints.NotNull[0].OID}
	roleExecute(t, e, op, "ALTER TABLE public.c OWNER TO captured_owner")
	after := e.CatalogRead().MustRelation(refs["c"].Address)
	if after.Owner != owner.Ref || before.Owner == owner.Ref || after.Constraints.NotNull[0].OID != nn.Constraint {
		t.Fatal("actual owner transfer changed source capture or physical NN identity")
	}
	if err := partitioncmds.RestoreInheritedOwner(e, op, raw, pair); err == nil {
		t.Fatal("consumed owner pairing reused")
	}
	generation := enginetest.Statement(t, "ALTER TABLE ONLY public.c ALTER g SET EXPRESSION AS(a+1)").(*parser.AlterTable)
	if err := partitioncmds.PairRestoredGeneration(e, op, generation, origins); err != nil {
		t.Fatal("legal owner metadata copy invalidated actual generation identity", err)
	}
	if err := tablecmds.RestoreNoInheritNotNull(e, op, restoreNNRaw("public.c", "a", "local_nn"), nn); err != nil {
		t.Fatal("legal owner metadata copy invalidated actual NN identity", err)
	}
	if current := e.CatalogRead().MustRelation(refs["c"].Address); current.Owner != owner.Ref || !current.Constraints.NotNull[0].NoInherit || current.Constraints.NotNull[0].OID != nn.Constraint {
		t.Fatal("paired NN repair changed final captured owner/identity")
	}
}

func TestRestoreOrdinaryTableOwnerPair(t *testing.T) {
	e, op, refs, _ := inheritedRestoreFixture(t)
	owner, ok := e.Roles().Lookup("captured_owner")
	if !ok {
		t.Fatal("missing captured role")
	}
	before := e.CatalogRead().MustRelation(refs["unrelated"].Address)
	neighbor := e.CatalogRead().MustRelation(refs["q"].Address)
	namespace := e.CatalogRead().MustNamespace(before.Namespace())
	pair, err := partitioncmds.CaptureInheritedOwner(e, op, refs["unrelated"], owner.Ref)
	if err != nil || pair.Ref != refs["unrelated"] || pair.Owner != owner.Ref || pair.Creator != before.Owner {
		t.Fatal("ordinary table lost exact fresh ownership pair", pair, err)
	}
	raw := enginetest.Statement(t, "ALTER TABLE public.unrelated OWNER TO captured_owner").(*parser.AlterOwner)
	state := e.Catalog.State
	for _, sql := range []string{
		"ALTER TABLE public.q OWNER TO captured_owner",
		"ALTER TABLE public.unrelated OWNER TO postgres",
		"ALTER TABLE unrelated OWNER TO captured_owner",
	} {
		if err := partitioncmds.RestoreInheritedOwner(e, op, enginetest.Statement(t, sql).(*parser.AlterOwner), pair); err == nil || e.Catalog.State != state {
			t.Fatal("failed pair changed private catalog", sql, err)
		}
	}
	if err := partitioncmds.RestoreInheritedOwner(e, op, raw, pair); err != nil || e.Catalog.State != state {
		t.Fatal("exact pair admission must not change ownership", err)
	}
	targets := e.ImportTargets
	e.ImportTargets = nil
	if err := partitioncmds.RestoreInheritedOwner(e, op, raw, pair); err == nil || e.Catalog.State != state {
		t.Fatal("ordinary operation acquired private ownership authority", err)
	}
	e.ImportTargets = targets
	roleExecute(t, e, op, "ALTER TABLE public.unrelated OWNER TO captured_owner")
	after := e.CatalogRead().MustRelation(refs["unrelated"].Address)
	if after.Owner != owner.Ref || before.Owner == owner.Ref || e.CatalogRead().MustRelation(refs["q"].Address) != neighbor || e.CatalogRead().MustNamespace(before.Namespace()) != namespace {
		t.Fatal("owner transfer changed old capture, neighbor or schema authority")
	}
	state = e.Catalog.State
	if err := partitioncmds.RestoreInheritedOwner(e, op, raw, pair); err == nil || e.Catalog.State != state {
		t.Fatal("consumed ordinary owner pair was reusable", err)
	}
}

func TestRestoreTableOwnerPairReferenceLifetime(t *testing.T) {
	e, op, refs, _ := inheritedRestoreFixture(t)
	owner, ok := e.Roles().Lookup("captured_owner")
	if !ok {
		t.Fatal("missing captured role")
	}
	pair, err := partitioncmds.CaptureInheritedOwner(e, op, refs["unrelated"], owner.Ref)
	if err != nil {
		t.Fatal(err)
	}
	raw := enginetest.Statement(t, "ALTER TABLE public.unrelated OWNER TO captured_owner").(*parser.AlterOwner)
	reject := func(t *testing.T, candidate table.RestoredInheritedOwner) {
		t.Helper()
		state, actor := e.Catalog.State, op.RoleState()
		target := e.CatalogRead().MustRelation(refs["unrelated"].Address)
		neighbor := e.CatalogRead().MustRelation(refs["q"].Address)
		namespace := e.CatalogRead().MustNamespace(target.Namespace())
		revision := target.Revision()
		if err := partitioncmds.RestoreInheritedOwner(e, op, raw, candidate); err == nil {
			t.Fatal("foreign or expired declaration reference admitted")
		}
		if e.Catalog.State != state || op.RoleState() != actor ||
			e.CatalogRead().MustRelation(target.Address()) != target || target.Revision() != revision ||
			e.CatalogRead().MustRelation(neighbor.Address()) != neighbor ||
			e.CatalogRead().MustNamespace(namespace.Address()) != namespace {
			t.Fatal("rejected reference changed catalog backing, revision, neighbor, namespace or actor")
		}
	}
	for name, mutate := range map[string]func(*table.RestoredInheritedOwner){
		"foreign-domain": func(p *table.RestoredInheritedOwner) {
			p.Ref = engine.New().Catalog.Domain.Ref(p.Ref.Epoch, p.Ref.Address)
		},
		"expired-epoch":    func(p *table.RestoredInheritedOwner) { p.Ref.Epoch++ },
		"column-reference": func(p *table.RestoredInheritedOwner) { p.Ref.Address.SubID = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := pair
			mutate(&candidate)
			reject(t, candidate)
		})
	}
	old := e.CatalogRead().MustRelation(pair.Ref.Address)
	roleExecute(t, e, op, "DROP TABLE public.unrelated")
	roleExecute(t, e, op, "CREATE TABLE public.unrelated(a INT)")
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	current, err := e.ResolveRelationReference(auth, raw.Name, false)
	if err != nil {
		t.Fatal(err)
	}
	if current == pair.Ref || current.Domain() != pair.Ref.Domain() || current.Epoch != pair.Ref.Epoch {
		t.Fatal("drop/recreate fixture did not distinguish same-name declaration lifetimes", current, pair.Ref)
	}
	refs["unrelated"] = current
	reject(t, pair)
	if old.Owner != pair.Creator {
		t.Fatal("expired declaration backing changed")
	}
	fresh, err := partitioncmds.CaptureInheritedOwner(e, op, current, owner.Ref)
	if err != nil || fresh.Ref != current {
		t.Fatal("replacement did not capture its own destination reference", fresh, err)
	}
	state, actor := e.Catalog.State, op.RoleState()
	if err := partitioncmds.RestoreInheritedOwner(e, op, raw, fresh); err != nil || e.Catalog.State != state || op.RoleState() != actor {
		t.Fatal("fresh destination declaration admission changed catalog or actor", err)
	}
}

func TestRestoreTableOwnerPairKindBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, declaration, target string
	}{
		{"view", "CREATE VIEW public.not_a_table AS SELECT 1 AS a", "public.not_a_table"},
		{"sequence", "CREATE SEQUENCE public.not_a_table", "public.not_a_table"},
		{"temporary", "CREATE TEMP TABLE not_a_table(a INT)", "not_a_table"},
		{"partitioned", "CREATE TABLE public.not_a_table(a INT) PARTITION BY RANGE(a)", "public.not_a_table"},
		{"partition", "CREATE TABLE public.not_a_table PARTITION OF public.owner_partition_root DEFAULT", "public.not_a_table"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, op, refs, _ := inheritedRestoreFixture(t)
			if tc.name == "partition" {
				roleExecute(t, e, op, "CREATE TABLE public.owner_partition_root(a INT) PARTITION BY RANGE(a)")
			}
			roleExecute(t, e, op, tc.declaration)
			owner, ok := e.Roles().Lookup("captured_owner")
			if !ok {
				t.Fatal("missing captured role")
			}
			raw := enginetest.Statement(t, "ALTER TABLE "+tc.target+" OWNER TO captured_owner").(*parser.AlterOwner)
			auth, err := core.OperationAuthorization(op)
			if err != nil {
				t.Fatal(err)
			}
			ref, err := e.ResolveRelationReference(auth, raw.Name, false)
			if err != nil {
				t.Fatal(err)
			}
			target := e.CatalogRead().MustRelation(ref.Address)
			namespace := e.CatalogRead().MustNamespace(target.Namespace())
			if target.RelationKind == catalog.TableRelation && target.Partition == nil && target.Membership == nil && namespace.Temporary == (ids.SessionToken{}) {
				t.Fatal("fixture accidentally supplied an ordinary permanent table")
			}
			state, actor := e.Catalog.State, op.RoleState()
			neighbor := e.CatalogRead().MustRelation(refs["unrelated"].Address)
			pair, err := partitioncmds.CaptureInheritedOwner(e, op, ref, owner.Ref)
			if err == nil || pair != (table.RestoredInheritedOwner{}) {
				t.Fatal("wrong-kind or temporary declaration acquired owner pair", pair, err)
			}
			if e.Catalog.State != state || op.RoleState() != actor ||
				e.CatalogRead().MustRelation(ref.Address) != target ||
				e.CatalogRead().MustNamespace(namespace.Address()) != namespace ||
				e.CatalogRead().MustRelation(neighbor.Address()) != neighbor {
				t.Fatal("rejected capture changed target, namespace, neighbor or actor")
			}
			ordinary, err := partitioncmds.CaptureInheritedOwner(e, op, refs["unrelated"], owner.Ref)
			if err != nil {
				t.Fatal(err)
			}
			ordinary.Ref = ref
			if err := partitioncmds.RestoreInheritedOwner(e, op, raw, ordinary); err == nil ||
				e.Catalog.State != state || op.RoleState() != actor ||
				e.CatalogRead().MustRelation(ref.Address) != target ||
				e.CatalogRead().MustRelation(neighbor.Address()) != neighbor {
				t.Fatal("substituted nonordinary target gained owner-pair authority", err)
			}
		})
	}
}

func TestRestoreTableOwnerPairActorBoundary(t *testing.T) {
	for _, creator := range []string{"postgres", "replay_admin"} {
		t.Run(creator, func(t *testing.T) {
			e, bootstrap, _, _ := inheritedRestoreFixture(t)
			roleExecute(t, e, bootstrap, "CREATE ROLE replay_admin LOGIN SUPERUSER")
			op, done := roleOperation(t, e, creator)
			defer done()
			roleExecute(t, e, op, "CREATE TABLE public.actor_table(a INT)")
			owner, ok := e.Roles().Lookup("captured_owner")
			if !ok {
				t.Fatal("missing captured role")
			}
			raw := enginetest.Statement(t, "ALTER TABLE public.actor_table OWNER TO captured_owner").(*parser.AlterOwner)
			auth, err := core.OperationAuthorization(op)
			if err != nil {
				t.Fatal(err)
			}
			ref, err := e.ResolveRelationReference(auth, raw.Name, false)
			if err != nil {
				t.Fatal(err)
			}
			old := e.CatalogRead().MustRelation(ref.Address)
			revision := old.Revision()
			state, actor := e.Catalog.State, op.RoleState()
			pair, err := partitioncmds.CaptureInheritedOwner(e, op, ref, owner.Ref)
			if err != nil || pair.Ref != ref || pair.Creator != actor.CurrentUser || pair.Owner != owner.Ref {
				t.Fatal("authenticated superuser capture lost exact actor or destination identity", pair, err)
			}
			if err := partitioncmds.RestoreInheritedOwner(e, op, raw, pair); err != nil {
				t.Fatal(err)
			}
			if e.Catalog.State != state || op.RoleState() != actor || old.Revision() != revision {
				t.Fatal("successful capture/admission changed actor or descriptor revision")
			}
			roleExecute(t, e, op, "SET SESSION AUTHORIZATION captured_owner")
			state, namedActor := e.Catalog.State, op.RoleState()
			if _, err := partitioncmds.CaptureInheritedOwner(e, op, ref, owner.Ref); err == nil {
				t.Fatal("named nonsuperuser acquired bootstrap capture authority")
			}
			if err := partitioncmds.RestoreInheritedOwner(e, op, raw, pair); err == nil ||
				e.Catalog.State != state || op.RoleState() != namedActor ||
				e.CatalogRead().MustRelation(ref.Address) != old || old.Revision() != revision {
				t.Fatal("failed named-actor admission changed catalog, actor or old revision", err)
			}
			roleExecute(t, e, op, "SET SESSION AUTHORIZATION DEFAULT")
			if op.RoleState() != actor {
				t.Fatal("authenticated prior actor was not restored exactly")
			}
			roleExecute(t, e, op, "ALTER TABLE public.actor_table OWNER TO captured_owner")
			current := e.CatalogRead().MustRelation(ref.Address)
			if current == old || current.Owner != owner.Ref || old.Owner != actor.CurrentUser ||
				old.Revision() != revision || current.Revision() <= revision || op.RoleState() != actor {
				t.Fatal("actual transfer changed old backing or leaked actor authority")
			}
		})
	}
}

func restoreNNRaw(relation, column, name string) *parser.Update {
	query := &parser.Select{Targets: []parser.Expr{parser.ColumnExpr{Name: restoreNNName("attnum")}}, From: &parser.TableReference{Name: restoreNNName("pg_catalog", "pg_attribute")}, Where: parser.LogicalExpr{Op: parser.LogicalAnd, Left: restoreNNEqual("attrelid", restoreNNRegclass(relation)), Right: restoreNNEqual("attname", restoreNNText(column))}}
	return &parser.Update{Name: restoreNNName("pg_catalog", "pg_constraint"), Assignments: []parser.UpdateAssignment{{Target: parser.Identifier{Text: "connoinherit"}, Value: parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true}}}}, Where: parser.LogicalExpr{Op: parser.LogicalAnd, Left: parser.LogicalExpr{Op: parser.LogicalAnd, Left: restoreNNEqual("conrelid", restoreNNRegclass(relation)), Right: restoreNNEqual("conname", restoreNNText(name))}, Right: restoreNNEqual("conkey", parser.ArrayExpr{Elements: []parser.Expr{parser.SubqueryExpr{Kind: parser.SubqueryScalar, Query: query}}})}}
}

func restoreNNName(parts ...string) parser.ObjectName {
	name := parser.ObjectName{}
	for _, part := range parts {
		name.Parts = append(name.Parts, parser.Identifier{Text: part})
	}
	return name
}

func restoreNNEqual(column string, value parser.Expr) parser.Expr {
	return parser.OperatorExpr{Name: restoreNNName("="), Args: []parser.Expr{parser.ColumnExpr{Name: restoreNNName(column)}, value}}
}

func restoreNNRegclass(text string) parser.Expr {
	return parser.RegclassExpr{Child: restoreNNText(text)}
}

func restoreNNText(text string) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: text}}
}

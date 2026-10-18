package engine

import (
	"maps"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

// Direct descriptor/stage fixtures deliberately do not construct the condition
// through SQL. They independently exercise the historical issuer and exact
// current composite-key/Usage validation, without a second physical provider.
func physicalNotNullFixture(t *testing.T) (*Engine, *CatalogStage, *relationDescriptor, *relationDescriptor) {
	t.Helper()
	e := New()
	base := e.CatalogRead()
	owner := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	namespace := ids.ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
	usage := testUsage(e, sqltype.Int4)
	mint := func() ids.OID {
		e.mu.Lock()
		defer e.mu.Unlock()
		oid, err := e.allocateOIDLocked()
		if err != nil {
			t.Fatal(err)
		}
		return oid
	}
	parent := &relationDescriptor{address: ids.ObjectAddress{ClassID: RelationClassID, ObjectID: mint()}, namespace: namespace, name: "p", owner: owner, revision: 1, relationKind: tableRelation, columns: []ColumnDescriptor{{Name: "b", Usage: usage, Nullable: true, NullabilityKnown: true}}, attributes: []int32{2}, index: map[string]int{"b": 0}, attributeHighWater: 2, droppedAttributes: map[int32]attributeMetadata{1: {number: 1, dropped: true}}}
	address := ids.ObjectAddress{ClassID: RelationClassID, ObjectID: mint()}
	indexAddress := ids.ObjectAddress{ClassID: RelationClassID, ObjectID: mint()}
	primary := uniqueConstraint{OID: 20000, Name: "c_key", Attributes: []int32{1, 2}, Primary: true, Index: indexAddress, IsLocal: true}
	history := retiredInheritanceOrigin{parent: e.domain.Ref(base.Epoch(), parent.address), attribute: 1, column: "a", name: "nn", constraint: 20001, lifetime: uint64(base.Epoch()), own: 20002}
	old := &relationDescriptor{address: address, namespace: namespace, name: "c", owner: owner, revision: 1, relationKind: tableRelation, hasIndexes: true, columns: []ColumnDescriptor{{Name: "a", Usage: usage, Nullable: false, NullabilityKnown: true}, {Name: "b", Usage: usage, Nullable: false, NullabilityKnown: true}}, attributes: []int32{1, 2}, index: map[string]int{"a": 0, "b": 1}, attributeHighWater: 2, notNullConstraints: []notNullConstraint{{OID: 20002, Name: "nn", Attribute: 1}, {OID: 20003, Name: "b_nn", Attribute: 2}}, uniqueConstraints: []uniqueConstraint{primary}, inheritance: &tableInheritance{parents: []ids.ObjectAddress{parent.address}, columns: map[int32]inheritedColumn{1: {local: true}, 2: {local: true}}, notNull: map[int32]inheritedConstraint{1: {retired: []retiredInheritanceOrigin{history}}, 2: {local: true}}}}
	index := &relationDescriptor{address: indexAddress, namespace: namespace, name: "c_key", owner: owner, revision: 1, relationKind: indexRelation, columns: slices.Clone(old.columns), attributes: []int32{1, 2}, index: map[string]int{"a": 0, "b": 1}, attributeHighWater: 2, sqlIndex: &indexDefinition{parent: address, keys: []indexKey{{attribute: 1}, {attribute: 2}}, unique: true, ready: true, valid: true, live: true, constraint: uniqueBacking(&primary)}}
	parent.hasSubclasses = true
	old.inheritance.columns[2] = inheritedColumn{local: true, origins: []inheritanceOrigin{{parent: parent.address, attribute: 2}}}
	created := e.BeginCatalogStage(base)
	t.Cleanup(func() { e.DiscardCatalogStage(created) })
	for _, descriptor := range []Descriptor{parent, old, index} {
		if err := created.Create(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	view := e.stageRead(created)
	if err := validateRetiredInheritance(view, old, old.inheritance.notNull[1], old.notNullConstraints[0].OID, false); err != nil {
		t.Fatal(err)
	}
	if err := validateInheritance(view, old); err != nil {
		t.Fatal(err)
	}
	// Replacement is based on the authentic completed creation overlay, not
	// on a stage whose base never contained this newly created relation.
	stage := e.BeginCatalogStage(view)
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	clean := releaseRetiredTypeConstraints(old, []parser.AlterTableAction{{Action: parser.AlterColumnType, Column: parser.Identifier{Text: "a"}}})
	if clean == old || len(clean.physicalNotNull) != 1 || len(clean.notNullConstraints) != 1 || clean.columns[0].Nullable {
		t.Fatal("genuine historical cleanup did not retain exactly its physical flag")
	}
	if err := stage.Replace(clean); err != nil {
		t.Fatal(err)
	}
	if err := validatePhysicalNotNull(e.stageRead(stage), clean, physicalOrdinaryNotNull(clean)); err != nil {
		t.Fatal(err)
	}
	return e, stage, old, clean
}

func TestPhysicalNotNullCauseAndCurrentSupport(t *testing.T) {
	e, stage, old, clean := physicalNotNullFixture(t)
	view := e.stageRead(stage)
	proof := clean.physicalNotNull[1]
	if proof.cause.self != proof.cause || proof.cause.removedNN != old.notNullConstraints[0].OID || proof.cause.primary != old.uniqueConstraints[0].OID || proof.cause.index != old.uniqueConstraints[0].Index || proof.cause.origin != proof.cause.ref || proof.cause.usage != old.columns[0].Usage || len(proof.support.keys) != 2 {
		t.Fatal("issuer guessed or truncated actual source identities/Usage")
	}
	if len(old.physicalNotNull) != 0 || len(old.notNullConstraints) != 2 || len(old.inheritance.notNull[1].retired) != 1 {
		t.Fatal("issuer mutated the retained source capture")
	}
	for name, mutate := range map[string]func(*physicalNotNullProof){
		"caller flag":     func(p *physicalNotNullProof) { p.cause = nil },
		"copied receipt":  func(p *physicalNotNullProof) { copy := *p.cause; p.cause = &copy },
		"current Usage":   func(p *physicalNotNullProof) { p.usage = testUsage(e, sqltype.Int8) },
		"current primary": func(p *physicalNotNullProof) { copy := *p.support; copy.constraint++; p.support = &copy },
		"current index":   func(p *physicalNotNullProof) { copy := *p.support; copy.index.ObjectID++; p.support = &copy },
		"nonfirst key Usage": func(p *physicalNotNullProof) {
			copy := *p.support
			copy.keys = slices.Clone(copy.keys)
			copy.keys[1].usage = testUsage(e, sqltype.Int8)
			p.support = &copy
		},
		"nonfirst key rank": func(p *physicalNotNullProof) {
			copy := *p.support
			copy.keys = slices.Clone(copy.keys)
			copy.keys[1].attribute = 1
			p.support = &copy
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := *clean
			bad.physicalNotNull = maps.Clone(clean.physicalNotNull)
			changed := proof
			mutate(&changed)
			bad.physicalNotNull[1] = changed
			if err := validatePhysicalNotNull(view, &bad, physicalOrdinaryNotNull(&bad)); err == nil {
				t.Fatal("forged physical not-null support accepted")
			}
		})
	}
	foreign := *clean
	foreign.address.ObjectID++
	if physicalNotNullOriginValid(view, &foreign, 1, proof) {
		t.Fatal("receipt retargeted another relation")
	}
	if physicalNotNullOriginValid(New().CatalogRead(), clean, 1, proof) {
		t.Fatal("receipt crossed catalog owner lifetime")
	}
	fake := old.notNullConstraints[0]
	fake.OID++
	if _, issued := physicalNotNullFromType(old, clean, fake); issued {
		t.Fatal("nonexistent NN tuple issued a physical receipt")
	}
	withoutPrimary := *old
	withoutPrimary.uniqueConstraints = nil
	if _, issued := physicalNotNullFromType(&withoutPrimary, clean, old.notNullConstraints[0]); issued {
		t.Fatal("historical count alone issued a physical flag")
	}
}

func TestPhysicalNotNullRetirementAndSupersession(t *testing.T) {
	e, stage, _, clean := physicalNotNullFixture(t)
	proof := clean.physicalNotNull[1]
	retired := *clean
	retired.uniqueConstraints = nil
	transitionPhysicalNotNull(clean, &retired)
	kept := retired.physicalNotNull[1]
	if kept.cause != proof.cause || kept.support != nil || kept.retired != proof.support || retired.columns[0].Nullable {
		t.Fatal("actual key retirement cleared flag or retained live provider authority")
	}
	if err := validatePhysicalNotNull(e.stageRead(stage), &retired, physicalOrdinaryNotNull(&retired)); err != nil {
		t.Fatal(err)
	}
	forged := retired
	forged.uniqueConstraints = clean.uniqueConstraints
	if err := validatePhysicalNotNull(e.stageRead(stage), &forged, physicalOrdinaryNotNull(&forged)); err == nil {
		t.Fatal("archived key rebound as a live provider")
	}
	ordinary := *clean
	ordinary.notNullConstraints = append(slices.Clone(clean.notNullConstraints), notNullConstraint{OID: 20004, Name: "new_nn", Attribute: 1})
	prunePhysicalNotNull(&ordinary)
	if len(ordinary.physicalNotNull) != 0 || len(clean.physicalNotNull) != 1 {
		t.Fatal("genuine new NN failed to supersede only its certificate immutably")
	}
	dropped := *clean
	dropped.attributes = []int32{2}
	dropped.columns = slices.Clone(clean.columns[1:])
	dropped.index = map[string]int{"b": 0}
	prunePhysicalNotNull(&dropped)
	if len(dropped.physicalNotNull) != 0 || len(clean.physicalNotNull) != 1 {
		t.Fatal("actual stable attribute retirement retained or mutated proof")
	}
}

func TestPhysicalNotNullRebuiltCompositeUsage(t *testing.T) {
	e, source, _, clean := physicalNotNullFixture(t)
	base := e.stageRead(source)
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	e.mu.Lock()
	indexOID, indexErr := e.allocateOIDLocked()
	constraintOID, constraintErr := e.allocateOIDLocked()
	e.mu.Unlock()
	if indexErr != nil || constraintErr != nil {
		t.Fatal(indexErr, constraintErr)
	}
	next := *clean
	next.revision++
	next.columns = slices.Clone(clean.columns)
	next.columns[1].Usage = testUsage(e, sqltype.Int8)
	next.uniqueConstraints = slices.Clone(clean.uniqueConstraints)
	next.uniqueConstraints[0].OID = constraintOID
	next.uniqueConstraints[0].Index = ids.ObjectAddress{ClassID: RelationClassID, ObjectID: indexOID}
	oldIndex := base.state.descriptors[clean.uniqueConstraints[0].Index].(*relationDescriptor)
	index := *oldIndex
	index.address = next.uniqueConstraints[0].Index
	index.revision++
	index.columns = slices.Clone(next.columns)
	definition := *oldIndex.sqlIndex
	definition.constraint = uniqueBacking(&next.uniqueConstraints[0])
	index.sqlIndex = &definition
	if err := stage.Remove(e.domain.Ref(base.Epoch(), oldIndex.address)); err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(&index); err != nil {
		t.Fatal(err)
	}
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := rebindPhysicalNotNullType(stage, &next, []columnTransformation{{attribute: 2}})
	if err != nil {
		t.Fatal(err)
	}
	proof := rebuilt.physicalNotNull[1]
	if proof.cause != clean.physicalNotNull[1].cause || proof.support.constraint != constraintOID || proof.support.index != index.address || proof.support.keys[1].usage != next.columns[1].Usage || proof.support.keys[0].usage != next.columns[0].Usage {
		t.Fatal("real group replacement lost immutable origin or full composite current Usage")
	}
	if err := validatePhysicalNotNull(e.stageRead(stage), rebuilt, physicalOrdinaryNotNull(rebuilt)); err != nil {
		t.Fatal(err)
	}
	if clean.physicalNotNull[1].support.constraint == constraintOID || clean.columns[1].Usage == next.columns[1].Usage {
		t.Fatal("rebind mutated genuine earlier capture")
	}
}

func TestPhysicalNotNullLocalHistoryActualRemint(t *testing.T) {
	e, source, old, _ := physicalNotNullFixture(t)
	local := *old
	local.inheritance = cloneInheritance(old.inheritance)
	provenance := local.inheritance.notNull[1]
	provenance.local = true
	local.inheritance.notNull[1] = provenance
	stage := e.BeginCatalogStage(source.base)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&local); err != nil {
		t.Fatal(err)
	}
	if err := validateInheritance(e.stageRead(stage), &local); err != nil {
		t.Fatal(err)
	}
	before := local.notNullConstraints[0]
	archive := provenance.retired[0]
	rebuilt, err := e.rebuildAlterNotNullsLocked(stage, &local, []columnTransformation{{attribute: 1}})
	if err != nil {
		t.Fatal(err)
	}
	after := rebuilt.notNullConstraints[0]
	if after.OID == 0 || after.OID == before.OID || after.Attribute != before.Attribute || after.Name != before.Name {
		t.Fatal("actual NN reconstruction failed to mint the same surviving fact")
	}
	current := rebuilt.inheritance.notNull[1]
	if !current.local || current.count() != len(provenance.origins) || len(current.retired) != 0 || local.inheritance.notNull[1].retired[0] != archive {
		t.Fatal("fresh NN inherited destroyed historical contributions or mutated the old archive")
	}
	if err := validateInheritance(e.stageRead(stage), rebuilt); err != nil {
		t.Fatal(err)
	}
	forged := *rebuilt
	forged.inheritance = cloneInheritance(rebuilt.inheritance)
	stale := provenance
	stale.retired = slices.Clone(provenance.retired)
	stale.retired[0].own++
	forged.inheritance.notNull[1] = stale
	if err := retireRebuiltNotNullHistory(&local, &forged); err == nil {
		t.Fatal("stale own identity rescued by actual NN reconstruction")
	}
}

func TestInheritanceRetiredLocalCheckReconstruction(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, sql := range []string{
		"CREATE TABLE p(a INT,CONSTRAINT ck CHECK(a>0))",
		"CREATE TABLE c(a INT) INHERITS(p)",
		"ALTER TABLE c ADD CONSTRAINT ck CHECK(a>0)",
		"ALTER TABLE p DROP COLUMN a",
	} {
		roleExecute(t, e, op, sql)
	}
	old := retiredUnitTable(t, e, "c")
	before := old.checkConstraints[0]
	provenance := old.inheritance.checks["ck"]
	if !provenance.local || len(provenance.retired) != 1 {
		t.Fatal("actual local retired CHECK fixture did not reach its source boundary")
	}
	archive := provenance.retired[0]
	roleExecute(t, e, op, "ALTER TABLE c ALTER a TYPE BIGINT USING a+10")
	current := retiredUnitTable(t, e, "c")
	after := current.checkConstraints[0]
	reconstructed := current.inheritance.checks["ck"]
	if after.OID == 0 || after.OID == before.OID || !reconstructed.local || reconstructed.count() != 0 || len(reconstructed.retired) != 0 {
		t.Fatal("real CHECK rebuild transferred destroyed historical support")
	}
	if old.inheritance.checks["ck"].retired[0] != archive {
		t.Fatal("CHECK reconstruction mutated the captured old archive")
	}
	if err := validateInheritance(e.CatalogRead(), current); err != nil {
		t.Fatal(err)
	}
	forged := *current
	forged.inheritance = cloneInheritance(current.inheritance)
	stale := provenance
	stale.retired = slices.Clone(provenance.retired)
	stale.retired[0].own++
	forged.inheritance.checks["ck"] = stale
	if err := retireRebuiltCheckHistory(old, &forged); err == nil {
		t.Fatal("CHECK reconstruction rescued a stale own identity")
	}
}

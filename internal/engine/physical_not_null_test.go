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
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	usage := testUsage(e, sqltype.Int4)
	mint := func() ids.OID {
		e.mu.Lock()
		defer e.mu.Unlock()
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			t.Fatal(err)
		}
		return oid
	}
	parent := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: mint()}, namespace: namespace, name: "p", Owner: owner, revision: 1, RelationKind: tableRelation, Columns: []ColumnDescriptor{{Name: "b", Usage: usage, Nullable: true, NullabilityKnown: true}}, Attributes: []int32{2}, Index: map[string]int{"b": 0}, AttributeHighWater: 2, DroppedAttributes: map[int32]attributeMetadata{1: {Number: 1, Dropped: true}}}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: mint()}
	indexAddress := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: mint()}
	primary := uniqueConstraint{OID: 20000, Name: "c_key", Attributes: []int32{1, 2}, Primary: true, Index: indexAddress, IsLocal: true}
	history := retiredInheritanceOrigin{Parent: e.catalog.Domain.Ref(base.Epoch(), parent.address), Attribute: 1, Column: "a", Name: "nn", Constraint: 20001, Lifetime: uint64(base.Epoch()), Own: 20002}
	old := &relationDescriptor{address: address, namespace: namespace, name: "c", Owner: owner, revision: 1, RelationKind: tableRelation, HasIndexes: true, Columns: []ColumnDescriptor{{Name: "a", Usage: usage, Nullable: false, NullabilityKnown: true}, {Name: "b", Usage: usage, Nullable: false, NullabilityKnown: true}}, Attributes: []int32{1, 2}, Index: map[string]int{"a": 0, "b": 1}, AttributeHighWater: 2, NotNullConstraints: []notNullConstraint{{OID: 20002, Name: "nn", Attribute: 1}, {OID: 20003, Name: "b_nn", Attribute: 2}}, UniqueConstraints: []uniqueConstraint{primary}, Inheritance: &tableInheritance{Parents: []ids.ObjectAddress{parent.address}, Columns: map[int32]inheritedColumn{1: {Local: true}, 2: {Local: true}}, NotNull: map[int32]inheritedConstraint{1: {Retired: []retiredInheritanceOrigin{history}}, 2: {Local: true}}}}
	index := &relationDescriptor{address: indexAddress, namespace: namespace, name: "c_key", Owner: owner, revision: 1, RelationKind: indexRelation, Columns: slices.Clone(old.Columns), Attributes: []int32{1, 2}, Index: map[string]int{"a": 0, "b": 1}, AttributeHighWater: 2, SQLIndex: &indexDefinition{Parent: address, Keys: []indexKey{{Attribute: 1}, {Attribute: 2}}, Unique: true, Ready: true, Valid: true, Live: true, Constraint: uniqueBacking(&primary)}}
	parent.HasSubclasses = true
	old.Inheritance.Columns[2] = inheritedColumn{Local: true, Origins: []inheritanceOrigin{{Parent: parent.address, Attribute: 2}}}
	created := e.BeginCatalogStage(base)
	t.Cleanup(func() { e.DiscardCatalogStage(created) })
	for _, descriptor := range []Descriptor{parent, old, index} {
		if err := created.Create(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	view := e.stageRead(created)
	if err := validateRetiredInheritance(view, old, old.Inheritance.NotNull[1], old.NotNullConstraints[0].OID, false); err != nil {
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
	if clean == old || len(clean.PhysicalNotNull) != 1 || len(clean.NotNullConstraints) != 1 || clean.Columns[0].Nullable {
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
	proof := clean.PhysicalNotNull[1]
	if proof.Cause.Self != proof.Cause || proof.Cause.RemovedNN != old.NotNullConstraints[0].OID || proof.Cause.Primary != old.UniqueConstraints[0].OID || proof.Cause.Index != old.UniqueConstraints[0].Index || proof.Cause.Origin != proof.Cause.Ref || proof.Cause.Usage != old.Columns[0].Usage || len(proof.Support.Keys) != 2 {
		t.Fatal("issuer guessed or truncated actual source identities/Usage")
	}
	if len(old.PhysicalNotNull) != 0 || len(old.NotNullConstraints) != 2 || len(old.Inheritance.NotNull[1].Retired) != 1 {
		t.Fatal("issuer mutated the retained source capture")
	}
	for name, mutate := range map[string]func(*physicalNotNullProof){
		"caller flag":     func(p *physicalNotNullProof) { p.Cause = nil },
		"copied receipt":  func(p *physicalNotNullProof) { copy := *p.Cause; p.Cause = &copy },
		"current Usage":   func(p *physicalNotNullProof) { p.Usage = testUsage(e, sqltype.Int8) },
		"current primary": func(p *physicalNotNullProof) { copy := *p.Support; copy.Constraint++; p.Support = &copy },
		"current index":   func(p *physicalNotNullProof) { copy := *p.Support; copy.Index.ObjectID++; p.Support = &copy },
		"nonfirst key Usage": func(p *physicalNotNullProof) {
			copy := *p.Support
			copy.Keys = slices.Clone(copy.Keys)
			copy.Keys[1].Usage = testUsage(e, sqltype.Int8)
			p.Support = &copy
		},
		"nonfirst key rank": func(p *physicalNotNullProof) {
			copy := *p.Support
			copy.Keys = slices.Clone(copy.Keys)
			copy.Keys[1].Attribute = 1
			p.Support = &copy
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := *clean
			bad.PhysicalNotNull = maps.Clone(clean.PhysicalNotNull)
			changed := proof
			mutate(&changed)
			bad.PhysicalNotNull[1] = changed
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
	fake := old.NotNullConstraints[0]
	fake.OID++
	if _, issued := physicalNotNullFromType(old, clean, fake); issued {
		t.Fatal("nonexistent NN tuple issued a physical receipt")
	}
	withoutPrimary := *old
	withoutPrimary.UniqueConstraints = nil
	if _, issued := physicalNotNullFromType(&withoutPrimary, clean, old.NotNullConstraints[0]); issued {
		t.Fatal("historical count alone issued a physical flag")
	}
}

func TestPhysicalNotNullRetirementAndSupersession(t *testing.T) {
	e, stage, _, clean := physicalNotNullFixture(t)
	proof := clean.PhysicalNotNull[1]
	retired := *clean
	retired.UniqueConstraints = nil
	transitionPhysicalNotNull(clean, &retired)
	kept := retired.PhysicalNotNull[1]
	if kept.Cause != proof.Cause || kept.Support != nil || kept.Retired != proof.Support || retired.Columns[0].Nullable {
		t.Fatal("actual key retirement cleared flag or retained live provider authority")
	}
	if err := validatePhysicalNotNull(e.stageRead(stage), &retired, physicalOrdinaryNotNull(&retired)); err != nil {
		t.Fatal(err)
	}
	forged := retired
	forged.UniqueConstraints = clean.UniqueConstraints
	if err := validatePhysicalNotNull(e.stageRead(stage), &forged, physicalOrdinaryNotNull(&forged)); err == nil {
		t.Fatal("archived key rebound as a live provider")
	}
	ordinary := *clean
	ordinary.NotNullConstraints = append(slices.Clone(clean.NotNullConstraints), notNullConstraint{OID: 20004, Name: "new_nn", Attribute: 1})
	prunePhysicalNotNull(&ordinary)
	if len(ordinary.PhysicalNotNull) != 0 || len(clean.PhysicalNotNull) != 1 {
		t.Fatal("genuine new NN failed to supersede only its certificate immutably")
	}
	dropped := *clean
	dropped.Attributes = []int32{2}
	dropped.Columns = slices.Clone(clean.Columns[1:])
	dropped.Index = map[string]int{"b": 0}
	prunePhysicalNotNull(&dropped)
	if len(dropped.PhysicalNotNull) != 0 || len(clean.PhysicalNotNull) != 1 {
		t.Fatal("actual stable attribute retirement retained or mutated proof")
	}
}

func TestPhysicalNotNullRebuiltCompositeUsage(t *testing.T) {
	e, source, _, clean := physicalNotNullFixture(t)
	base := e.stageRead(source)
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	e.mu.Lock()
	indexOID, indexErr := e.catalog.allocateOIDLocked()
	constraintOID, constraintErr := e.catalog.allocateOIDLocked()
	e.mu.Unlock()
	if indexErr != nil || constraintErr != nil {
		t.Fatal(indexErr, constraintErr)
	}
	next := *clean
	next.revision++
	next.Columns = slices.Clone(clean.Columns)
	next.Columns[1].Usage = testUsage(e, sqltype.Int8)
	next.UniqueConstraints = slices.Clone(clean.UniqueConstraints)
	next.UniqueConstraints[0].OID = constraintOID
	next.UniqueConstraints[0].Index = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: indexOID}
	oldIndex := base.MustRelation(clean.UniqueConstraints[0].Index)
	index := *oldIndex
	index.address = next.UniqueConstraints[0].Index
	index.revision++
	index.Columns = slices.Clone(next.Columns)
	definition := *oldIndex.SQLIndex
	definition.Constraint = uniqueBacking(&next.UniqueConstraints[0])
	index.SQLIndex = &definition
	if err := stage.Remove(e.catalog.Domain.Ref(base.Epoch(), oldIndex.address)); err != nil {
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
	proof := rebuilt.PhysicalNotNull[1]
	if proof.Cause != clean.PhysicalNotNull[1].Cause || proof.Support.Constraint != constraintOID || proof.Support.Index != index.address || proof.Support.Keys[1].Usage != next.Columns[1].Usage || proof.Support.Keys[0].Usage != next.Columns[0].Usage {
		t.Fatal("real group replacement lost immutable origin or full composite current Usage")
	}
	if err := validatePhysicalNotNull(e.stageRead(stage), rebuilt, physicalOrdinaryNotNull(rebuilt)); err != nil {
		t.Fatal(err)
	}
	if clean.PhysicalNotNull[1].Support.Constraint == constraintOID || clean.Columns[1].Usage == next.Columns[1].Usage {
		t.Fatal("rebind mutated genuine earlier capture")
	}
}

func TestPhysicalNotNullLocalHistoryActualRemint(t *testing.T) {
	e, source, old, _ := physicalNotNullFixture(t)
	local := *old
	local.Inheritance = cloneInheritance(old.Inheritance)
	provenance := local.Inheritance.NotNull[1]
	provenance.Local = true
	local.Inheritance.NotNull[1] = provenance
	stage := e.BeginCatalogStage(source.base)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&local); err != nil {
		t.Fatal(err)
	}
	if err := validateInheritance(e.stageRead(stage), &local); err != nil {
		t.Fatal(err)
	}
	before := local.NotNullConstraints[0]
	archive := provenance.Retired[0]
	rebuilt, err := e.rebuildAlterNotNullsLocked(stage, &local, []columnTransformation{{attribute: 1}})
	if err != nil {
		t.Fatal(err)
	}
	after := rebuilt.NotNullConstraints[0]
	if after.OID == 0 || after.OID == before.OID || after.Attribute != before.Attribute || after.Name != before.Name {
		t.Fatal("actual NN reconstruction failed to mint the same surviving fact")
	}
	current := rebuilt.Inheritance.NotNull[1]
	if !current.Local || current.count() != len(provenance.Origins) || len(current.Retired) != 0 || local.Inheritance.NotNull[1].Retired[0] != archive {
		t.Fatal("fresh NN inherited destroyed historical contributions or mutated the old archive")
	}
	if err := validateInheritance(e.stageRead(stage), rebuilt); err != nil {
		t.Fatal(err)
	}
	forged := *rebuilt
	forged.Inheritance = cloneInheritance(rebuilt.Inheritance)
	stale := provenance
	stale.Retired = slices.Clone(provenance.Retired)
	stale.Retired[0].Own++
	forged.Inheritance.NotNull[1] = stale
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
	before := old.CheckConstraints[0]
	provenance := old.Inheritance.Checks["ck"]
	if !provenance.Local || len(provenance.Retired) != 1 {
		t.Fatal("actual local retired CHECK fixture did not reach its source boundary")
	}
	archive := provenance.Retired[0]
	roleExecute(t, e, op, "ALTER TABLE c ALTER a TYPE BIGINT USING a+10")
	current := retiredUnitTable(t, e, "c")
	after := current.CheckConstraints[0]
	reconstructed := current.Inheritance.Checks["ck"]
	if after.OID == 0 || after.OID == before.OID || !reconstructed.Local || reconstructed.count() != 0 || len(reconstructed.Retired) != 0 {
		t.Fatal("real CHECK rebuild transferred destroyed historical support")
	}
	if old.Inheritance.Checks["ck"].Retired[0] != archive {
		t.Fatal("CHECK reconstruction mutated the captured old archive")
	}
	if err := validateInheritance(e.CatalogRead(), current); err != nil {
		t.Fatal(err)
	}
	forged := *current
	forged.Inheritance = cloneInheritance(current.Inheritance)
	stale := provenance
	stale.Retired = slices.Clone(provenance.Retired)
	stale.Retired[0].Own++
	forged.Inheritance.Checks["ck"] = stale
	if err := retireRebuiltCheckHistory(old, &forged); err == nil {
		t.Fatal("CHECK reconstruction rescued a stale own identity")
	}
}

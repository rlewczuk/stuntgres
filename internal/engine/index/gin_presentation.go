package index

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// ginPresentationClass is deliberately not a relaxed BindClassSupport. Its
// predecessor is the owning catalog transition, and its old capture must still
// bind strictly there. Only class/family presentation may differ in the target;
// the actual method, members and executable callback generations must agree.
func ginPresentationClass(before, after catalog.Read, captured sqltype.IndexClass) (sqltype.IndexClass, error) {
	old, err := routine.BindClassSupport(before, captured)
	if err != nil {
		return sqltype.IndexClass{}, err
	}
	current, ok := after.IndexClass(ids.OID(old.OID()))
	if !ok || !old.IsGin() || !old.SameIdentity(current) {
		return sqltype.IndexClass{}, fmt.Errorf("stuntgres: GIN presentation changed class identity")
	}
	current, err = routine.BindClassSupport(after, current)
	if err != nil {
		return sqltype.IndexClass{}, err
	}
	oldClass, _ := before.OperatorClass(ids.OID(old.OID()))
	newClass, _ := after.OperatorClass(ids.OID(old.OID()))
	class := *newClass
	class.ObjectName, class.ObjectNamespace, class.Owner, class.ObjectRevision = oldClass.ObjectName, oldClass.ObjectNamespace, oldClass.Owner, oldClass.ObjectRevision
	if oldClass.Origin != nil && oldClass.Origin.Installation.Implementation == extension.StockBtreeGin {
		class.Extension = oldClass.Extension
	}
	if class != *oldClass {
		return sqltype.IndexClass{}, fmt.Errorf("stuntgres: GIN presentation changed class authority")
	}
	address := ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: ids.OID(old.FamilyOID())}
	oldFamily, _ := before.OperatorFamily(address)
	newFamily, _ := after.OperatorFamily(address)
	family := *newFamily
	family.ObjectName, family.ObjectNamespace, family.Owner, family.ObjectRevision = oldFamily.ObjectName, oldFamily.ObjectNamespace, oldFamily.Owner, oldFamily.ObjectRevision
	if oldFamily.Origin != nil && oldFamily.Origin.Installation.Implementation == extension.StockBtreeGin {
		family.Extension = oldFamily.Extension
	}
	if family != *oldFamily {
		return sqltype.IndexClass{}, fmt.Errorf("stuntgres: GIN presentation changed family authority")
	}
	method := ids.OID(old.MethodOID())
	oldMethod, _ := before.AccessMethod(method)
	newMethod, _ := after.AccessMethod(method)
	if oldMethod != newMethod {
		return sqltype.IndexClass{}, fmt.Errorf("stuntgres: GIN presentation changed method authority")
	}
	handler := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oldMethod.Handler}
	if before.Get(handler) != after.Get(handler) {
		return sqltype.IndexClass{}, fmt.Errorf("stuntgres: GIN presentation changed handler authority")
	}
	input, familyID := ids.OID(old.InputOID()), ids.OID(old.FamilyOID())
	for slot := 1; slot <= 6; slot++ {
		oldMember, oldOK := before.FamilySupport(familyID, input, input, int16(slot))
		newMember, newOK := after.FamilySupport(familyID, input, input, int16(slot))
		if oldOK != newOK || oldOK && *oldMember != *newMember {
			return sqltype.IndexClass{}, fmt.Errorf("stuntgres: GIN presentation changed support membership")
		}
		if oldOK {
			routine := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oldMember.Function}
			if !ginPresentationProcedure(before, after, routine) {
				return sqltype.IndexClass{}, fmt.Errorf("stuntgres: GIN presentation changed support authority")
			}
		}
	}
	// Required search slots and operand types belong to the authenticated
	// bindings, not the class input (CIDR support and search types differ).
	for number := 1; number <= 5; number++ {
		prior, next := old.Search(number), current.Search(number)
		if prior == (sqltype.IndexSearch{}) && next == (sqltype.IndexSearch{}) {
			continue
		}
		if prior == (sqltype.IndexSearch{}) || next == (sqltype.IndexSearch{}) {
			return sqltype.IndexClass{}, fmt.Errorf("stuntgres: GIN presentation changed search membership")
		}
		if prior != next {
			return sqltype.IndexClass{}, fmt.Errorf("stuntgres: GIN presentation changed search executable generation")
		}
		oldAddress := ids.ObjectAddress{ClassID: ids.AccessMethodOperatorClassID, ObjectID: ids.OID(prior.Member)}
		newAddress := ids.ObjectAddress{ClassID: ids.AccessMethodOperatorClassID, ObjectID: ids.OID(next.Member)}
		oldMember, oldOK := before.Get(oldAddress).(*catalog.OperatorFamilyOperator)
		newMember, newOK := after.Get(newAddress).(*catalog.OperatorFamilyOperator)
		if !oldOK || !newOK || *oldMember != *newMember {
			return sqltype.IndexClass{}, fmt.Errorf("stuntgres: GIN presentation changed search membership")
		}
		operator := ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: oldMember.Operator}
		if before.Get(operator) != after.Get(operator) {
			return sqltype.IndexClass{}, fmt.Errorf("stuntgres: GIN presentation changed operator authority")
		}
	}
	return current, nil
}

// An admitted class/family ALTER updates current live key captures in the same
// private catalog stage. This cannot refresh an arbitrary detached old value.
func refreshGinPresentationKeys(h FamilyDDLHost, before catalog.Read, changed ids.ObjectAddress) error {
	var method ids.OID
	switch descriptor := before.Get(changed).(type) {
	case *catalog.OperatorClass:
		method = descriptor.Method
	case *catalog.OperatorFamily:
		method = descriptor.Method
	}
	authority, ok := before.AccessMethod(method)
	if !ok || authority.Native != catalog.GinAccessMethod {
		return nil
	}
	after := h.StageRead()
	for _, descriptor := range before.Relations() {
		if descriptor.SQLIndex == nil || descriptor.SQLIndex.Method != catalog.GinMethod {
			continue
		}
		var next *catalog.Relation
		for ordinal, key := range descriptor.SQLIndex.Keys {
			affected := changed.ClassID == ids.OperatorClassCatalogID && uint32(changed.ObjectID) == key.Class.OID() || changed.ClassID == ids.OperatorFamilyClassID && uint32(changed.ObjectID) == key.Class.FamilyOID()
			if !affected {
				continue
			}
			if err := h.Context().Err(); err != nil {
				return err
			}
			class, err := ginPresentationClass(before, after, key.Class)
			if err != nil {
				return err
			}
			if class == key.Class {
				continue
			}
			if next == nil {
				copy, definition := *descriptor, *descriptor.SQLIndex
				definition.Keys = slices.Clone(definition.Keys)
				copy.SQLIndex = &definition
				next = &copy
			}
			next.SQLIndex.Keys[ordinal].Class = class
		}
		if next != nil {
			var err error
			next.ObjectRevision, err = h.Revision()
			if err != nil {
				return err
			}
			if err := h.Stage().Replace(next); err != nil {
				return err
			}
		}
	}
	return nil
}

// This only selects a possible presentation transport. It is not authority to
// reuse data: RebindGinPresentationRoot must certify both catalog generations.
func ginPresentationIndexSemantics(a, b *catalog.Relation) bool {
	if a == nil || b == nil || a.Address() != b.Address() || a.SQLIndex == nil || b.SQLIndex == nil || a.SQLIndex.Method != catalog.GinMethod || b.SQLIndex.Method != catalog.GinMethod || len(a.Columns) != len(b.Columns) {
		return false
	}
	old, next := a.SQLIndex, b.SQLIndex
	if old.MethodIdentity() != next.MethodIdentity() || old.Parent != next.Parent || old.Unique != next.Unique || old.NullsNotDistinct != next.NullsNotDistinct || old.Predicate != next.Predicate || len(old.Keys) != len(next.Keys) || !slices.Equal(old.Include, next.Include) || !slices.Equal(old.Exclusion, next.Exclusion) {
		return false
	}
	for i, key := range old.Keys {
		current := next.Keys[i]
		if !key.Class.SameIdentity(current.Class) {
			return false
		}
		current.Class = key.Class
		if current != key {
			return false
		}
	}
	for i, column := range a.Columns {
		if column.Usage != b.Columns[i].Usage {
			return false
		}
	}
	return true
}

// RebindGinPresentationRoot certifies an owning predecessor-to-candidate
// transition. Entries and dictionary/posting backing remain the predecessor's;
// the mapped construction lineage preserves both clean history and prior debt.
func RebindGinPresentationRoot(before, after catalog.Read, oldParent, parent, oldIndex, index *catalog.Relation, root *storage.IndexRoot) (*storage.IndexRoot, error) {
	changed := len(oldIndex.SQLIndex.Keys) != len(index.SQLIndex.Keys)
	if !changed {
		for i, key := range oldIndex.SQLIndex.Keys {
			changed = changed || key.Class != index.SQLIndex.Keys[i].Class
		}
	}
	if !changed {
		return root, nil
	}
	bindings, err := BindGinIndex(after, parent, index.SQLIndex)
	if err != nil {
		return nil, err
	}
	if root == nil || root.Gin == nil || len(root.Gin.Columns) != len(bindings) {
		return nil, fmt.Errorf("stuntgres: missing GIN presentation predecessor")
	}
	same := true
	for i, binding := range bindings {
		same = same && binding.Same(root.Gin.Columns[i].Binding)
	}
	if same {
		return root, nil
	} // A certified build candidate already uses target authority.
	prior, err := BindGinIndex(before, oldParent, oldIndex.SQLIndex)
	if err != nil {
		return nil, err
	}
	if len(prior) != len(bindings) {
		return nil, fmt.Errorf("stuntgres: GIN presentation changed key count")
	}
	for i, binding := range prior {
		if !binding.Same(root.Gin.Columns[i].Binding) {
			return nil, fmt.Errorf("stuntgres: foreign GIN presentation predecessor")
		}
		class, err := ginPresentationClass(before, after, oldIndex.SQLIndex.Keys[i].Class)
		if err != nil {
			return nil, err
		}
		if class != bindings[i].Class {
			return nil, fmt.Errorf("stuntgres: inconsistent GIN presentation target")
		}
		comparison := bindings[i]
		comparison.Class = binding.Class
		if !comparison.Same(binding) {
			return nil, fmt.Errorf("stuntgres: GIN presentation changed token semantics")
		}
	}
	next, payload := *root, *root.Gin
	payload.Columns = slices.Clone(payload.Columns)
	payload.Pending = slices.Clone(payload.Pending)
	for i := range bindings {
		payload.Columns[i].Binding = bindings[i]
		if len(payload.Pending) != 0 {
			payload.Pending[i].Binding = bindings[i]
		}
	}
	next.Gin = &payload
	if ginPresentationIndexSemantics(oldIndex, index) {
		next.Construction = transportConstruction(root, oldIndex, index)
	}
	return &next, nil
}

// TransportGinPresentationOutputs follows a certified presentation-root
// transport. Already-produced keys/predicate results retain their immutable
// backing; neither expressions nor domain conversions run again for this ALTER.
func TransportGinPresentationOutputs(ctx context.Context, rows []storage.RowVersion, source, target *catalog.Relation) ([]storage.RowVersion, bool, error) {
	if !ginPresentationIndexSemantics(source, target) {
		return nil, false, fmt.Errorf("stuntgres: invalid GIN presentation output transport")
	}
	out, copied := rows, false
	for i, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		output, exists := row.IndexOutputs[source.Address()]
		if !exists {
			continue
		}
		if OutputMatches(output, target) {
			continue
		}
		if !OutputMatches(output, source) {
			return nil, false, fmt.Errorf("stuntgres: stale GIN presentation output")
		}
		if !copied {
			out, copied = slices.Clone(rows), true
		}
		row.IndexOutputs = maps.Clone(row.IndexOutputs)
		output.Index = target
		row.IndexOutputs[target.Address()] = output
		out[i] = row
	}
	return out, copied, ctx.Err()
}

// RetainGinPresentation keeps an existing routine-source fence as well as the
// newly certified target binding. A metadata transport is not a clean builder
// and must not erase the provenance/debt of an earlier private routine change.
func (c *OverlayCapture) RetainGinPresentation(changes *session.Journal, root *storage.IndexRoot) error {
	previous := JournalIndexesOrZero(changes).Candidates[c.Index.Address()]
	if previous.routineSource != nil && (previous.Root != c.PresentationRoot || !previous.MatchesIndex(c.PresentationSource, &c.Store)) {
		return catalog.Conflict{}
	}
	RetainIndexBuild(changes, c.Index, &c.Store, root)
	if previous.routineSource != nil {
		next := JournalIndexes(changes).Candidates[c.Index.Address()]
		next.routineSource = &routineRootSource{pins: previous.routineSource.pins, candidate: next, source: c.PresentationSource, previous: previous.routineSource}
		JournalIndexes(changes).Candidates[c.Index.Address()] = next
	}
	return nil
}

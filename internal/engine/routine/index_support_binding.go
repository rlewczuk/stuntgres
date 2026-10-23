package routine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// BindClassSupport captures actual members from one immutable catalog view.
// The current descriptor is the sole authority for class and family identity.
func BindClassSupport(view catalog.Read, class sqltype.IndexClass) (sqltype.IndexClass, error) {
	method, ok := view.AccessMethod(ids.OID(class.MethodOID()))
	if !ok {
		return class, fmt.Errorf("stuntgres: unavailable index access method")
	}
	return bindClassSupport(view, class, method)
}

func bindClassSupport(view catalog.Read, class sqltype.IndexClass, method *catalog.AccessMethod) (sqltype.IndexClass, error) {
	fact, found := view.OperatorClass(ids.OID(class.OID()))
	if !found {
		return class, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "index operator class is unsupported"}
	}
	if class.MethodOID() != uint32(fact.Method) || class.FamilyOID() != uint32(fact.Family) {
		return class, fmt.Errorf("stuntgres: inconsistent index operator class identity")
	}
	current, ok := view.IndexClassForMethod(fact.Address().ObjectID, method)
	if !ok {
		return class, fmt.Errorf("stuntgres: unavailable index operator class")
	}
	if !class.SameIdentity(current) {
		return class, fmt.Errorf("stuntgres: inconsistent index operator class capture")
	}
	class = current
	family, ok := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: fact.Family})
	if !ok || family.Method != fact.Method {
		return class, fmt.Errorf("stuntgres: invalid index family method")
	}
	if class.IsGiST() {
		return class, nil
	}
	slots := 1
	if class.IsHash() {
		slots = 2
	}
	for slot := 1; slot <= slots; slot++ {
		support, err := selectNativeIndexSupport(view, uint32(fact.Family), uint32(fact.InputType), uint32(fact.InputType), int16(slot))
		if err != nil {
			return class, err
		}
		if support.Provider == sqltype.CubeIndexSupport {
			member, _ := view.FamilySupport(fact.Family, fact.InputType, fact.InputType, int16(slot))
			if member == nil || member.OwningClass != fact.Address().ObjectID || fact.Extension == (ids.ObjectAddress{}) {
				return class, fmt.Errorf("stuntgres: cube support requires its installed class")
			}
		}
		class = class.WithSupport(slot, support)
	}
	return class, nil
}

func BindIndexOrdering(view catalog.Read, usage sqltype.Usage, class sqltype.IndexClass) (sqltype.IndexComparison, error) {
	class, err := BindClassSupport(view, class)
	if err != nil {
		return sqltype.IndexComparison{}, err
	}
	return sqltype.BindIndexOrdering(usage, class)
}
func BindPartitionHash(view catalog.Read, usage sqltype.Usage, class sqltype.IndexClass) (sqltype.PartitionHash, error) {
	class, err := BindClassSupport(view, class)
	if err != nil {
		return sqltype.PartitionHash{}, err
	}
	return sqltype.BindPartitionHash(usage, class)
}

func selectNativeIndexSupport(view catalog.Read, family, left, right uint32, slot int16) (sqltype.IndexSupport, error) {
	member, ok := view.FamilySupport(ids.OID(family), ids.OID(left), ids.OID(right), slot)
	if !ok {
		return sqltype.IndexSupport{}, fmt.Errorf("stuntgres: missing registered index support")
	}
	if member.Family != ids.OID(family) || member.Left != ids.OID(left) || member.Right != ids.OID(right) || member.Number != slot || member.Revision() == 0 {
		return sqltype.IndexSupport{}, fmt.Errorf("stuntgres: inconsistent index support member")
	}
	proc, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: member.Function})
	var cube catalog.ExtensionImplementation
	if ok && proc.Implementation != nil && view.Owner() != nil {
		cube = view.Owner().Extensions["cube"]
	}
	if cube != nil && proc.Implementation == cube {
		installation, exists := view.Extension(proc.Extension)
		if !exists {
			return sqltype.IndexSupport{}, fmt.Errorf("stuntgres: missing cube support installation")
		}
		if err := cube.ValidateExtension(view, installation); err != nil {
			return sqltype.IndexSupport{}, err
		}
		symbol, err := extension.ProcedureSymbol(view, proc)
		if err != nil {
			return sqltype.IndexSupport{}, err
		}
		single := strconv.FormatUint(uint64(left), 10)
		fact, exists := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: ids.OID(family)})
		if !exists || fact.Method != 403 || fact.Extension != proc.Extension || slot != 1 || left != right || symbol != "cube_cmp" || string(proc.Signature) != single+","+single || proc.Result != 23 || member.OwningClass == 0 {
			return sqltype.IndexSupport{}, fmt.Errorf("stuntgres: invalid installed cube support selection")
		}
		class, exists := view.OperatorClass(member.OwningClass)
		if !exists || class.Family != fact.Address().ObjectID || class.InputType != ids.OID(left) || class.Extension != fact.Extension || class.Method != 403 {
			return sqltype.IndexSupport{}, fmt.Errorf("stuntgres: invalid installed cube support class")
		}
		return sqltype.IndexSupport{Provider: sqltype.CubeIndexSupport, Family: family, Member: uint32(member.Address().ObjectID), Routine: uint32(member.Function), Left: left, Right: right, Revision: uint64(member.Revision()), FamilyRevision: uint64(fact.Revision()), RoutineRevision: uint64(proc.Revision())}, nil
	}
	if !ok || proc.Builtin == nil {
		return sqltype.IndexSupport{}, fmt.Errorf("stuntgres: index support has no native routine")
	}
	n, ok := sqltype.NativeIndexRoutineByOID(uint32(member.Function))
	result := uint32(23)
	if n.Kind == sqltype.IndexExtendedHashRoutine {
		result = 20
	}
	if !ok || proc.Builtin.Identity.ID != member.Function || proc.Builtin.Identity.Namespace != ids.PGCatalogNamespaceOID || uint32(proc.Result) != result || string(proc.Signature) != n.Signature {
		return sqltype.IndexSupport{}, fmt.Errorf("stuntgres: invalid native index support identity/signature")
	}
	familyDescriptor, exists := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: ids.OID(family)})
	if !exists {
		return sqltype.IndexSupport{}, fmt.Errorf("stuntgres: missing selected support family")
	}
	return sqltype.IndexSupport{Family: family, Member: uint32(member.Address().ObjectID), Routine: uint32(member.Function), Left: left, Right: right, Revision: uint64(member.Revision()), FamilyRevision: uint64(familyDescriptor.Revision()), RoutineRevision: uint64(proc.Revision())}, nil
}

func BindIndexForeignKey(view catalog.Read, parent, child sqltype.Usage, class sqltype.IndexClass) (sqltype.IndexForeignKeyComparison, error) {
	class, err := BindClassSupport(view, class)
	if err != nil {
		return sqltype.IndexForeignKeyComparison{}, err
	}
	same := class.Support(1)
	childID := uint32(child.Type.BaseType().ID())
	if child.Type.BaseType().IsEnum() {
		childID = 3500
	}
	var support sqltype.IndexForeignKeySupport
	_, cross := view.FamilyOperator(ids.OID(same.Family), ids.OID(same.Left), ids.OID(childID), 3, 's')
	_, childEquality := view.FamilyOperator(ids.OID(same.Family), ids.OID(childID), ids.OID(childID), 3, 's')
	if cross && childEquality {
		support.Cross, err = selectNativeIndexSupport(view, same.Family, same.Left, childID, 1)
		if err != nil {
			return sqltype.IndexForeignKeyComparison{}, err
		}
		support.Child, err = selectNativeIndexSupport(view, same.Family, childID, childID, 1)
		if err != nil {
			return sqltype.IndexForeignKeyComparison{}, err
		}
	}
	return sqltype.BindIndexForeignKey(parent, child, class, support, view.LookupType)
}

// BindExclusionOperator proves logical eligibility through the actual family
// operator row. GiST keeps its bounded logical predicates, not a physical tree.
func BindExclusionOperator(view catalog.Read, usage sqltype.Usage, class sqltype.IndexClass, name string) (sqltype.ExclusionOperator, error) {
	class, err := BindClassSupport(view, class)
	if err != nil {
		return sqltype.ExclusionOperator{}, err
	}
	fact, ok := view.OperatorClass(ids.OID(class.OID()))
	if !ok {
		return sqltype.ExclusionOperator{}, fmt.Errorf("stuntgres: missing exclusion operator class")
	}
	family := uint32(fact.Family)
	// Resolve membership for the logical operator's inputs, independently of
	// binary-compatible index admission. An integer column admitted by oid_ops
	// still uses =(integer,integer), not the family's =(oid,oid).
	t := usage.Type.BaseType()
	input := t.ID()
	switch input {
	case sqltype.VarChar:
		input = sqltype.Text
	case sqltype.Regclass, sqltype.Regtype, sqltype.Regrole:
		input = sqltype.Oid
	case sqltype.Cidr:
		input = sqltype.Inet
	}
	if t.IsRange() {
		input = 3831
	}
	if sqltype.IsRegObjectAlias(input) || sqltype.IsRoutineAlias(input) || sqltype.IsOperatorAlias(input) || sqltype.IsRegTS(input) {
		input = sqltype.Oid
	}
	if t.IsEnum() {
		input = 3500 // Released polymorphic anyenum operator input.
	}
	strategy := int16(0)
	if name == "=" {
		strategy = 3
		if class.IsHash() {
			strategy = 1
		}
		if class.IsGiST() && input == sqltype.Inet {
			strategy = 18
		}
	} else if name == "&&" && class.IsGiST() {
		strategy = 3
	}
	var member uint32
	if strategy != 0 {
		row, ok := view.FamilyOperator(ids.OID(family), ids.OID(input), ids.OID(input), strategy, 's')
		if ok {
			operator, exists := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: row.Operator})
			if exists && operator.Name() == name && operator.Left == ids.OID(input) && operator.Right == ids.OID(input) {
				member = uint32(row.Address().ObjectID)
			}
		}
	}
	return sqltype.BindExclusionOperator(usage, class, name, member)
}

package routine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// BindClassSupport captures actual members from one immutable catalog view.
// Class lookup remains builtin-only; input adaptation belongs to sqltype.
func BindClassSupport(view catalog.Read, class sqltype.IndexClass) (sqltype.IndexClass, error) {
	var fact sqltype.IndexClassMetadata
	found := false
	for i := range sqltype.IndexClassMetadataCount() {
		m, _ := sqltype.IndexClassMetadataAt(i)
		if m.OID == class.OID() {
			fact, found = m, true
			break
		}
	}
	if !found {
		return class, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "index operator class is unsupported"}
	}
	family, ok := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: ids.OID(fact.Family)})
	if !ok || uint32(family.Method) != fact.Method {
		return class, fmt.Errorf("stuntgres: invalid index family method")
	}
	if fact.Method == 783 {
		return class, nil
	}
	slots := 1
	if fact.Method == 405 {
		slots = 2
	}
	for slot := 1; slot <= slots; slot++ {
		support, err := selectNativeIndexSupport(view, fact.Family, fact.Input, fact.Input, int16(slot))
		if err != nil {
			return class, err
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
	proc, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: member.Function})
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
	var family uint32
	var method uint32
	for i := range sqltype.IndexClassMetadataCount() {
		m, _ := sqltype.IndexClassMetadataAt(i)
		if m.OID == class.OID() {
			family, method = m.Family, m.Method
			break
		}
	}
	input := usage.Type.BaseType().ID()
	if input == sqltype.VarChar {
		input = sqltype.Text
	}
	// CIDR keys use the compatible INET physical class input. The network
	// family has INET operators, not invented CIDR member rows.
	if input == sqltype.Cidr && class.Accepts(usage.Type) {
		input = sqltype.Inet
	}
	if usage.Type.BaseType().IsEnum() {
		input = 3500
	}
	if method == 783 && family == 3919 {
		input = 3831
	}
	if sqltype.IsRegObjectAlias(input) || sqltype.IsOperatorAlias(input) || sqltype.IsRoutineAlias(input) || sqltype.IsRegTS(input) || input == sqltype.Regclass || input == sqltype.Regtype || input == sqltype.Regrole {
		input = sqltype.Oid
	}
	strategy := int16(0)
	if name == "=" {
		strategy = 3
		if method == 405 {
			strategy = 1
		}
		if method == 783 && family == 3550 {
			strategy = 18
		}
	} else if name == "&&" && method == 783 {
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

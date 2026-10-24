package index

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func familyTypes(h FamilyDDLHost, auth ddl.Authority, raw []parser.TypeName) ([]ids.OID, error) {
	idsOut := make([]ids.OID, len(raw))
	for i, t := range raw {
		id, err := h.ResolveRegtype(auth, t)
		if err != nil {
			return nil, err
		}
		idsOut[i] = id
	}
	return idsOut, nil
}
func familyTypeNames(v catalog.Read, args []ids.OID, separator string) string {
	names := make([]string, len(args))
	for i, id := range args {
		if t, ok := v.LookupType(sqltype.ID(id)); ok {
			names[i] = t.DiagnosticName()
		} else {
			names[i] = strconv.FormatUint(uint64(id), 10)
		}
	}
	return strings.Join(names, separator)
}
func familySignature(args []ids.OID) catalog.ProcedureSignature {
	parts := make([]string, len(args))
	for i, id := range args {
		parts[i] = strconv.FormatUint(uint64(id), 10)
	}
	return catalog.ProcedureSignature(strings.Join(parts, ","))
}
func familyRoutine(h FamilyDDLHost, auth ddl.Authority, item parser.OperatorFamilyItem) (catalog.RoutineIdentity, []ids.OID, ids.OID, error) {
	v := h.AccessView(auth).Catalog
	args, err := familyTypes(h, auth, item.Arguments)
	if err != nil {
		return catalog.RoutineIdentity{}, nil, 0, err
	}
	signature := familySignature(args)
	var sig *catalog.ProcedureSignature
	if item.Signature {
		sig = &signature
	}
	identity, count, err := h.SelectFamilyFunction(auth, item.Name, sig)
	if err != nil {
		return identity, nil, 0, err
	}
	if count > 1 {
		return identity, nil, 0, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42725", Message: fmt.Sprintf(`function name "%s" is not unique`, catalog.ObjectNameText(item.Name)), Hint: "Specify the argument list to select the function unambiguously."}
	}
	if count == 0 {
		return identity, nil, 0, familyError("42883", "function "+catalog.ObjectNameText(item.Name)+"("+familyTypeNames(v, args, ", ")+") does not exist")
	}
	if !item.Signature {
		args = nil
		if identity.Signature != "" {
			for _, s := range strings.Split(string(identity.Signature), ",") {
				id, err := strconv.ParseUint(s, 10, 32)
				if err != nil {
					return identity, nil, 0, err
				}
				args = append(args, ids.OID(id))
			}
		}
	}
	if d, ok := v.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: identity.ID}); ok {
		if d.RoutineKind != catalog.FunctionRoutine {
			return identity, nil, 0, familyError("42809", catalog.ObjectNameText(item.Name)+" is not a function")
		}
		return identity, args, d.Result, nil
	}
	result, ok := catalog.BuiltinRoutineResultByOID(identity.ID)
	if !ok {
		return identity, nil, 0, fmt.Errorf("stuntgres: unavailable released family routine metadata %d", identity.ID)
	}
	return identity, args, result, nil
}
func familyOperator(h FamilyDDLHost, auth ddl.Authority, item parser.OperatorFamilyItem) (*catalog.Operator, error) {
	return familyOperatorInput(h, auth, item, 0)
}
func familyOperatorInput(h FamilyDDLHost, auth ddl.Authority, item parser.OperatorFamilyItem, input ids.OID) (*catalog.Operator, error) {
	if !item.Signature && input == 0 {
		return nil, familyError("42601", "operator argument types must be specified in ALTER OPERATOR FAMILY")
	}
	if item.Signature && len(item.Arguments) != 2 {
		return nil, familyError("42P17", "index operators must be binary")
	}
	args := make([]ids.OID, 2)
	if !item.Signature {
		args[0], args[1] = input, input
	}
	for i, t := range item.Arguments {
		if strings.EqualFold(t.Name, "none") && t.Schema == nil && !t.Quoted {
			continue
		}
		id, err := h.ResolveRegtype(auth, t)
		if err != nil {
			return nil, err
		}
		args[i] = id
	}
	path, err := h.SearchNamespaces(auth, item.Name, ids.OperatorClassID)
	if err != nil {
		// Operator identity lookup treats an absent explicit namespace as no
		// matching operator, unlike family and support-function identity lookup.
		if diag, ok := err.(*sqlstate.Error); !ok || diag.Code != "3F000" {
			return nil, err
		}
		path = nil
	}
	v := h.AccessView(auth).Catalog
	key := catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID}, ObjectName: item.Name.Parts[len(item.Name.Parts)-1].Text, Left: args[0], Right: args[1]}
	for _, ns := range path {
		key.ObjectNamespace = ns
		if d, ok := v.Operator(v.NameAddress(catalog.DescriptorNameKey(&key))); ok {
			return d, nil
		}
	}
	return nil, familyError("42883", "operator does not exist: "+routine.OperatorInputName(v, args[0], args[1], item.Name))
}
func familyAssociation(h FamilyDDLHost, auth ddl.Authority, raw []parser.TypeName) (ids.OID, ids.OID, error) {
	if len(raw) == 0 {
		return 0, 0, nil
	}
	left, err := h.ResolveRegtype(auth, raw[0])
	if err != nil {
		return 0, 0, err
	}
	right := left
	if len(raw) > 1 {
		right, err = h.ResolveRegtype(auth, raw[1])
		if err != nil {
			return 0, 0, err
		}
	}
	if len(raw) > 2 {
		return 0, 0, familyError("42601", "one or two argument types must be specified")
	}
	return left, right, nil
}

func stageFamilyMembers(h FamilyDDLHost, auth ddl.Authority, family *catalog.OperatorFamily, s *parser.AlterOperatorFamily) error {
	return stageOperatorMembers(h, auth, family, s, nil, nil)
}

// stageOperatorMembers shares resolution and storage phases while retaining
// CREATE CLASS's input fallback, storage admission and unique-index diagnostics.
func stageOperatorMembers(h FamilyDDLHost, auth ddl.Authority, family *catalog.OperatorFamily, s *parser.AlterOperatorFamily, class *catalog.OperatorClass, beforeStore func() error) error {
	view := h.AccessView(auth).Catalog
	method, exists := view.AccessMethod(family.Method)
	if !exists || method.MethodKind() != "i" {
		return fmt.Errorf("stuntgres: missing family method")
	}
	physical, _ := method.IndexImplementation()
	physicalOID := physical.OID()
	// Phase 1 follows SQL order, resolving and validating the entire list before
	// consulting stored members. DROP records keys, not stored descriptors.
	var operators, procedures []catalog.Descriptor
	seen := make(map[ids.NameKey]bool, len(s.Items))
	storageSeen := false
	for _, item := range s.Items {
		if item.Storage != nil {
			if storageSeen {
				return familyError("42P17", "storage type specified more than once")
			}
			storageSeen = true
			storage, err := h.ResolveRegtype(auth, *item.Storage)
			if err != nil {
				return err
			}
			if storage != class.InputType {
				if physical != catalog.GistMethod {
					return familyError("42P17", fmt.Sprintf(`storage type cannot be different from data type for access method "%s"`, familyMethodName(view, family.Method)))
				}
				class.StorageType = storage
			}
			continue
		}
		maximum := int64(5)
		kind := "operator"
		if physical == catalog.HashMethod {
			maximum = 1
		} else if physical == catalog.GistMethod {
			maximum = 32767
		}
		if item.Function {
			kind = "function"
			switch physicalOID {
			case 403:
				maximum = 6
			case 405:
				maximum = 3
			case 783:
				maximum = 12
			}
		}
		number, err := strconv.ParseInt(item.Number, 10, 64)
		if err != nil || number < 1 || number > maximum {
			return familyError("42P17", fmt.Sprintf("invalid %s number %s, must be between 1 and %d", kind, item.Number, maximum))
		}
		var left, right ids.OID
		var next catalog.Descriptor
		if s.Action == "drop" {
			left, right, err = familyAssociation(h, auth, item.Association)
			if err != nil {
				return err
			}
			if left == 0 {
				return familyError("42601", "associated data types must be specified")
			}
			if item.Function {
				next = &catalog.OperatorFamilySupport{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodProcedureClassID}, Family: family.Address().ObjectID, Left: left, Right: right, Number: int16(number)}
			} else {
				next = &catalog.OperatorFamilyOperator{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodOperatorClassID}, Family: family.Address().ObjectID, Left: left, Right: right, Number: int16(number)}
			}
		} else if item.Function {
			identity, args, result, err := familyRoutine(h, auth, item)
			if err != nil {
				return err
			}
			left, right, err = familyAssociation(h, auth, item.Association)
			if err != nil {
				return err
			}
			input := ids.OID(0)
			if class != nil {
				input = class.InputType
			}
			left, right, err = classSupportTypes(physicalOID, int16(number), args, result, left, right, input)
			if err != nil {
				return err
			}
			next = &catalog.OperatorFamilySupport{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodProcedureClassID}, Family: family.Address().ObjectID, Left: left, Right: right, Number: int16(number), Function: identity.ID}
		} else {
			input := ids.OID(0)
			if class != nil {
				input = class.InputType
			}
			operator, err := familyOperatorInput(h, auth, item, input)
			if err != nil {
				return err
			}
			purpose := byte('s')
			var sortFamily ids.OID
			if item.Purpose == "o" {
				sort, err := findFamily(h, auth, item.SortFamily, 403)
				if err != nil {
					return err
				}
				sortFamily = sort.Address().ObjectID
				purpose = 'o'
			}
			if operator.Left == 0 || operator.Right == 0 {
				return familyError("42P17", "index operators must be binary")
			}
			if purpose == 'o' {
				if physical != catalog.GistMethod {
					return familyError("42P17", fmt.Sprintf(`access method "%s" does not support ordering operators`, familyMethodName(view, family.Method)))
				}
			} else if operator.Result != 16 {
				return familyError("42P17", "index search operators must return boolean")
			}
			left, right = operator.Left, operator.Right
			next = &catalog.OperatorFamilyOperator{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodOperatorClassID}, Family: family.Address().ObjectID, Left: left, Right: right, Number: int16(number), Purpose: purpose, Operator: operator.Address().ObjectID, Method: family.Method, SortFamily: sortFamily}
		}
		key := catalog.DescriptorNameKey(next)
		if seen[key] {
			return familyError("42P17", fmt.Sprintf("%s number %d for (%s) appears more than once", kind, number, familyTypeNames(view, []ids.OID{left, right}, ",")))
		}
		seen[key] = true
		if item.Function {
			procedures = append(procedures, next)
		} else {
			operators = append(operators, next)
		}
	}
	if beforeStore != nil {
		if err := beforeStore(); err != nil {
			return err
		}
	}

	// Phase 2 follows PostgreSQL storage/deletion order: all operators before
	// procedures, preserving encounter order within each kind.
	type operatorIdentity struct {
		operator ids.OID
		purpose  byte
	}
	var operatorMembers map[operatorIdentity]bool
	if s.Action == "add" && len(operators) != 0 {
		operatorMembers = make(map[operatorIdentity]bool)
		for _, descriptor := range view.Descriptors() {
			if member, ok := descriptor.(*catalog.OperatorFamilyOperator); ok && member.Family == family.Address().ObjectID {
				operatorMembers[operatorIdentity{member.Operator, member.Purpose}] = true
			}
		}
	}
	for _, members := range [2][]catalog.Descriptor{operators, procedures} {
		for i, requested := range members {
			kind := "operator"
			var left, right ids.OID
			var number int16
			var stored catalog.Descriptor
			switch d := requested.(type) {
			case *catalog.OperatorFamilyOperator:
				left, right, number = d.Left, d.Right, d.Number
				if member, ok := view.FamilyOperator(d.Family, left, right, number, 's'); ok {
					stored = member
				} else if member, ok := view.FamilyOperator(d.Family, left, right, number, 'o'); ok {
					stored = member
				}
			case *catalog.OperatorFamilySupport:
				kind = "function"
				left, right, number = d.Left, d.Right, d.Number
				if member, ok := view.FamilySupport(d.Family, left, right, number); ok {
					stored = member
				}
			}
			if s.Action == "drop" {
				if stored == nil {
					return familyError("42704", fmt.Sprintf(`%s %d(%s) does not exist in operator family "%s"`, kind, number, familyTypeNames(view, []ids.OID{left, right}, ","), catalog.ObjectNameText(s.Name)))
				}
				if catalog.OperatorFamilySeeded(stored) {
					object, _ := h.DependencyObject(auth, stored.Address())
					return familyError("2BP01", "cannot drop "+object+" because it is required by the database system")
				}
				if err := h.CheckFamilyMemberDrop(auth, stored.Address()); err != nil {
					return err
				}
				members[i] = stored
				continue
			}
			if stored != nil {
				if class != nil {
					return classMemberCollision(requested)
				}
				return familyError("42710", fmt.Sprintf(`%s %d(%s) already exists in operator family "%s"`, kind, number, familyTypeNames(view, []ids.OID{left, right}, ","), catalog.ObjectNameText(s.Name)))
			}
			if member, ok := requested.(*catalog.OperatorFamilyOperator); ok {
				identity := operatorIdentity{member.Operator, member.Purpose}
				if operatorMembers[identity] {
					return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "pg_amop_opr_fam_index"`, Detail: fmt.Sprintf("Key (amopopr, amoppurpose, amopfamily)=(%d, %c, %d) already exists.", member.Operator, member.Purpose, member.Family), Schema: "pg_catalog", Table: "pg_amop", Constraint: "pg_amop_opr_fam_index"}
				}
				operatorMembers[identity] = true
			}
		}
	}

	// Phase 3 alone allocates identities and stages mutations. CatalogStage is
	// the sole publication/rollback authority; no valid prefix is published.
	for _, members := range [2][]catalog.Descriptor{operators, procedures} {
		for _, d := range members {
			if s.Action == "drop" {
				h.DropTarget(d.Address())
				continue
			}
			oid, err := h.AllocateOID()
			if err != nil {
				return err
			}
			revision, err := h.Revision()
			if err != nil {
				return err
			}
			switch d := d.(type) {
			case *catalog.OperatorFamilyOperator:
				d.ObjectAddress.ObjectID = oid
				d.ObjectRevision = revision
				d.InternalOwner = catalog.FamilyMemberInternalOwner(h.StageRead(), family.Method, d.Family, d.Left, d.Right, d.Number, false, class)
			case *catalog.OperatorFamilySupport:
				d.ObjectAddress.ObjectID = oid
				d.ObjectRevision = revision
				d.InternalOwner = catalog.FamilyMemberInternalOwner(h.StageRead(), family.Method, d.Family, d.Left, d.Right, d.Number, true, class)
			}
			if err = h.Stage().Create(d); err != nil {
				return err
			}
		}
	}
	if s.Action == "drop" && len(s.Items) != 0 {
		_ = h.RecordDependencyPath(auth)
	}
	return nil
}

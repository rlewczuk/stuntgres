package routine

import (
	"strconv"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func checkOperatorArgumentUsage(h DDLHost, auth Authority, id ids.OID) error {
	if id == 0 {
		return nil
	}
	return h.AccessView(auth).CanUse(auth, ddlRef(h, auth, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id}))
}

func operatorArgument(h DDLHost, auth Authority, raw parser.TypeName) (ids.OID, error) {
	if raw.Name == "" || strings.EqualFold(raw.Name, "none") && raw.Schema == nil && !raw.Quoted {
		return 0, nil
	}
	if raw.Setof {
		return 0, sqlError("42P13", "SETOF type not allowed for operator argument")
	}
	id, err := h.ResolveType(auth, raw)
	if err != nil {
		return 0, err
	}
	return id, checkOperatorArgumentUsage(h, auth, id)
}

func findUtilityOperator(h DDLHost, auth Authority, s parser.OperatorSignature) (*catalog.Operator, error) {
	left, err := operatorArgument(h, auth, s.Left)
	if err != nil {
		return nil, err
	}
	right, err := operatorArgument(h, auth, s.Right)
	if err != nil {
		return nil, err
	}
	path, err := h.OperatorNamespaces(auth, s.Name)
	if err != nil {
		return nil, err
	}
	view := h.AccessView(auth).Catalog
	key := catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID}, Left: left, Right: right, ObjectName: s.Name.Parts[len(s.Name.Parts)-1].Text}
	for _, namespace := range path {
		key.ObjectNamespace = namespace
		a := view.NameAddress(catalog.DescriptorNameKey(&key))
		if d, ok := view.Operator(a); ok {
			return d, nil
		}
	}
	return nil, sqlError("42883", "operator does not exist: "+OperatorInputName(view, left, right, s.Name))
}

func operatorRoutine(h DDLHost, auth Authority, name parser.ObjectName, inputs []ids.OID) (*catalog.Procedure, error) {
	view := h.AccessView(auth).Catalog
	ids := make([]string, len(inputs))
	types := make([]string, len(inputs))
	for i, id := range inputs {
		ids[i] = strconv.FormatUint(uint64(id), 10)
		if t, ok := view.LookupType(sqltype.ID(id)); ok {
			types[i] = t.DiagnosticName()
		}
	}
	d, err := h.LookupFunction(auth, name, catalog.ProcedureSignature(strings.Join(ids, ",")))
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, sqlError("42883", "function "+catalog.ObjectNameText(name)+"("+strings.Join(types, ", ")+") does not exist")
	}
	if d.RoutineKind != 'f' {
		return nil, sqlError("42809", catalog.ObjectNameText(name)+" is not a function")
	}
	if err = ddlCheck(h, auth, access.Requirement{Object: ddlRef(h, auth, d.Address()), Privileges: authorization.ExecutePrivilege}); err != nil {
		return nil, err
	}
	return d, nil
}

func operatorEstimator(h DDLHost, auth Authority, name parser.ObjectName, join bool) (ids.OID, error) {
	if len(name.Parts) == 0 {
		return 0, nil
	}
	signature := catalog.ProcedureSignature("2281,26,2281,23")
	builtin, superuser := true, false
	if join {
		signature = "2281,26,2281,21,2281"
	}
	identity, count, err := h.SelectFunction(auth, name, signature)
	if err != nil {
		return 0, err
	}
	if join {
		alternate, other, err := h.SelectFunction(auth, name, "2281,26,2281,21")
		if err != nil {
			return 0, err
		}
		identity, count, err = operatorEstimatorVariant(name, identity, count, alternate, other)
		if err != nil {
			return 0, err
		}
	}
	if count == 0 {
		inputs := []ids.OID{2281, 26, 2281, 23}
		if join {
			inputs = []ids.OID{2281, 26, 2281, 21, 2281}
		}
		_, err := operatorRoutine(h, auth, name, inputs)
		return 0, err
	}
	view := h.AccessView(auth)
	result := ReleasedOperatorEstimatorResult(identity.ID)
	if d, ok := view.Catalog.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: identity.ID}); ok {
		if d.RoutineKind != catalog.FunctionRoutine {
			return 0, sqlError("42809", catalog.ObjectNameText(name)+" is not a function")
		}
		result = d.Result
		if err = ddlCheck(h, auth, access.Requirement{Object: ddlRef(h, auth, d.Address()), Privileges: authorization.ExecutePrivilege}); err != nil {
			return 0, err
		}
		builtin = d.Seeded()
		if !builtin {
			actor, err := view.Roles.Descriptor(view.State.CurrentUser)
			if err != nil {
				return 0, err
			}
			superuser = actor.Superuser
		}
	}
	if err := validateOperatorEstimatorMetadata(name, join, result, builtin, superuser); err != nil {
		return 0, err
	}
	return identity.ID, nil
}

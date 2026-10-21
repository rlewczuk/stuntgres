package fdw

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// handlerProcedure resolves the HANDLER or VALIDATOR function of a
// wrapper: it must exist with signature, be executable by the actor and
// return fdw_handler or void. A nil name (NO HANDLER, NO VALIDATOR) is nil.
func handlerProcedure(h Host, auth ddl.Authority, name *parser.ObjectName, signature catalog.ProcedureSignature) (*catalog.Procedure, error) {
	if name == nil {
		return nil, nil
	}
	p, err := h.LookupFunction(auth, *name, signature)
	if err != nil {
		return nil, err
	}
	if p == nil {
		args := ""
		if signature == catalog.ForeignValidatorSignature {
			args = "text[], oid"
		}
		return nil, sqlError("42883", "function "+catalog.ObjectNameText(*name)+"("+args+") does not exist")
	}
	if err := ddl.Check(h, auth, access.Requirement{Object: ObjectRef(h.AccessView(auth), p), Privileges: authorization.ExecutePrivilege}); err != nil {
		return nil, err
	}
	result := ids.OID(3115)
	if signature == catalog.ForeignValidatorSignature {
		result = 2278
	}
	if p.Result != result {
		return nil, sqlError("42804", "foreign-data wrapper function has incorrect return type")
	}
	return p, nil
}

// stageValidatorJob builds the job against the stage base and
// records its validator procedure among the stage's observed dependencies.
func stageValidatorJob(h Host, wrapper *catalog.ForeignObject, kind catalog.ForeignObjectKind, options []catalog.ForeignOption) (Validation, error) {
	job, validator, err := ValidatorJob(h.StageBase(), wrapper, kind, options)
	if validator != nil {
		h.AddDomainBase(validator)
	}
	return job, err
}

package fdw

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// handlerProcedure resolves the HANDLER or VALIDATOR function of a wrapper.
// Both require their exact input signature and EXECUTE privilege; only handlers
// require fdw_handler results. Nil denotes NO HANDLER or NO VALIDATOR.
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
	if signature == catalog.ForeignHandlerSignature && p.Result != ids.OID(3115) {
		return nil, sqlError("42809", "function "+catalog.ObjectNameText(*name)+" must return type fdw_handler")
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

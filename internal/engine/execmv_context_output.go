package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func (r *finalRows) FormatACLRole(authority *sqltype.ACLAuthority, id uint32) (string, error) {
	if authority == nil || authority != r.ACLAuthority {
		return "", fmt.Errorf("stuntgres: foreign ACL output identity")
	}
	name, ok := r.AclitemOutput[id]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing ACL output snapshot")
	}
	return name, nil
}

func (r *finalRows) FormatRegcollation(id uint32) (string, error) {
	return r.regObjectText(ids.CollationClassID, id)
}

func (r *finalRows) FormatRegnamespace(id uint32) (string, error) {
	return r.regObjectText(ids.NamespaceClassID, id)
}

func (r *finalRows) regObjectText(class ids.OID, id uint32) (string, error) {
	text, exists := r.RegobjectOutput[ids.ObjectAddress{ClassID: class, ObjectID: ids.OID(id)}]
	if !exists {
		return "", fmt.Errorf("stuntgres: missing collation/namespace output snapshot")
	}
	return text, nil
}

type operatorGroups map[string][]*catalog.Operator

type operatorOutputKey struct {
	ID    uint32
	Alias sqltype.ID
}

func (r *finalRows) formatOperator(id uint32, alias sqltype.ID) (string, error) {
	text, ok := r.OperatorOutput[operatorOutputKey{id, alias}]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing operator output snapshot")
	}
	return text, nil
}

func (r *finalRows) FormatRegoper(id uint32) (string, error) {
	return r.formatOperator(id, sqltype.Regoper)
}

func (r *finalRows) FormatRegoperator(id uint32) (string, error) {
	return r.formatOperator(id, sqltype.Regoperator)
}

func (r *finalRows) FormatRegproc(id uint32) (string, error) {
	text, ok := r.RegprocOutput[id]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing retained regproc presentation")
	}
	return text, nil
}

func (r *finalRows) FormatRegprocedure(id uint32) (string, error) {
	text, ok := r.RegprocedureOutput[id]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing retained regprocedure presentation")
	}
	return text, nil
}

func (r *finalRows) FormatRegrole(id uint32) (string, error) {
	text, ok := r.RegroleOutput[id]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing regrole output snapshot")
	}
	return text, nil
}

type regTSOutputKey struct {
	ID    uint32
	Alias sqltype.ID
}

func (r *finalRows) formatRegTS(id uint32, alias sqltype.ID) (string, error) {
	text, ok := r.RegTSOutput[regTSOutputKey{id, alias}]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing text search output snapshot")
	}
	return text, nil
}

func (r *finalRows) FormatRegconfig(id uint32) (string, error) {
	return r.formatRegTS(id, sqltype.Regconfig)
}

func (r *finalRows) FormatRegdictionary(id uint32) (string, error) {
	return r.formatRegTS(id, sqltype.Regdictionary)
}

func (r *finalRows) FormatRegtype(id uint32) (string, error) {
	text, ok := r.RegtypeOutput[id]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing regtype output snapshot")
	}
	return text, nil
}

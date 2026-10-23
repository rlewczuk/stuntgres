package regobj

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// SpellingCache is a statement's reg* output spellings: regproc,
// regprocedure, operator and text-search names by OID (and alias), and the
// operator groups. The statement's single evaluator owner fills it on first
// use. It never belongs to a held plan and is discarded with the statement
// scope. Callers pass the statement's captured catalog view and path on
// every call; a cached spelling is returned as first spelled.
type SpellingCache struct {
	regproc        map[uint32]string
	regprocedure   map[uint32]string
	operators      map[exec.OperatorOutputKey]string
	operatorGroups exec.OperatorGroups
	regTS          map[exec.RegTSOutputKey]string
}

// Regproc returns the regproc spelling of id.
func (c *SpellingCache) Regproc(view catalog.Read, path []ids.ObjectAddress, id uint32) string {
	if text, ok := c.regproc[id]; ok {
		return text
	}
	text := ProcSpelling(view, path, ids.OID(id))
	if c.regproc == nil {
		c.regproc = make(map[uint32]string)
	}
	c.regproc[id] = text
	return text
}

// Regprocedure returns the regprocedure spelling of id, with its signature.
func (c *SpellingCache) Regprocedure(view catalog.Read, path []ids.ObjectAddress, id uint32) string {
	if text, ok := c.regprocedure[id]; ok {
		return text
	}
	text := RoutineSpelling(view, path, ids.OID(id), true, false)
	if c.regprocedure == nil {
		c.regprocedure = make(map[uint32]string)
	}
	c.regprocedure[id] = text
	return text
}

// Operator returns the regoper or regoperator spelling of id. The operator
// groups are collected on the first miss.
func (c *SpellingCache) Operator(view catalog.Read, path []ids.ObjectAddress, id uint32, alias sqltype.ID) string {
	key := exec.OperatorOutputKey{ID: id, Alias: alias}
	if text, ok := c.operators[key]; ok {
		return text
	}
	if c.operatorGroups == nil {
		c.operatorGroups = CollectOperatorGroups(view)
	}
	text := OperatorSpelling(view, path, c.operatorGroups, ids.OID(id), alias)
	if c.operators == nil {
		c.operators = make(map[exec.OperatorOutputKey]string)
	}
	c.operators[key] = text
	return text
}

// TextSearch returns the regconfig or regdictionary spelling of id.
func (c *SpellingCache) TextSearch(view catalog.Read, path []ids.ObjectAddress, id uint32, alias sqltype.ID) string {
	key := exec.RegTSOutputKey{ID: id, Alias: alias}
	if text, ok := c.regTS[key]; ok {
		return text
	}
	text := TextSearchSpelling(view, path, id, alias, false)
	if c.regTS == nil {
		c.regTS = make(map[exec.RegTSOutputKey]string)
	}
	c.regTS[key] = text
	return text
}

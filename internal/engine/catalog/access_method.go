package catalog

import "stuntgres/internal/engine/ids"

// AccessMethod is a compiled implementation fact, not a mutable catalog service.
// OID is the SQL identity; Implementation selects only physical mechanics.
type AccessMethod struct {
	OID            ids.OID
	Name           string
	Handler        ids.OID
	Kind           string
	Implementation IndexMethod
}

var accessMethodFacts = [...]AccessMethod{
	{2, "heap", 3, "t", BtreeMethod},
	{403, "btree", 330, "i", BtreeMethod},
	{405, "hash", 331, "i", HashMethod},
	{783, "gist", 332, "i", GistMethod},
}

// AccessMethods returns value facts without exposing mutable shared storage.
func AccessMethods() [4]AccessMethod { return accessMethodFacts }
func AccessMethodByOID(oid ids.OID) (AccessMethod, bool) {
	return accessMethodByOID(accessMethodFacts[:], oid)
}
func accessMethodByOID(facts []AccessMethod, oid ids.OID) (AccessMethod, bool) {
	for _, fact := range facts {
		if fact.OID == oid {
			return fact, true
		}
	}
	return AccessMethod{}, false
}
func AccessMethodByName(name string) (AccessMethod, bool) {
	for _, fact := range accessMethodFacts {
		if fact.Name == name {
			return fact, true
		}
	}
	return AccessMethod{}, false
}
func (m IndexMethod) OID() ids.OID {
	for _, fact := range accessMethodFacts {
		if fact.Kind == "i" && fact.Implementation == m {
			return fact.OID
		}
	}
	return 0
}

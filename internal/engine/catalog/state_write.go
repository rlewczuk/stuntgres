package catalog

import (
	"iter"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/util"
)

// Write access to an unpublished catalog state.
//
// These methods, the constructors and CloneState are the only code that
// changes the descriptor and name maps. Callers write only to states they are
// still building: the bootstrap seed, a clone, a stage candidate or a
// replacement before publication. A published state is never written again;
// the engine's publication swaps the whole state under its mutex.

// SetDescriptor stores d at a.
func (s *State) SetDescriptor(a ids.ObjectAddress, d Descriptor) {
	old, _ := s.findDescriptor(a)
	s.replaceDescriptor(a, old, d)
}

// addSeedDescriptor stores d at a, which the seed's bulk load has not filled
// yet, without SetDescriptor's lookup of the descriptor it replaces. A second
// install at one address panics. Outside a bulk load it is SetDescriptor.
func (s *State) addSeedDescriptor(a ids.ObjectAddress, d Descriptor) {
	if s.bulk == nil {
		s.SetDescriptor(a, d)
		return
	}
	n := s.descriptorCount()
	s.replaceDescriptor(a, nil, d)
	if s.descriptorCount() == n {
		panic("stuntgres: builtin seed installs one catalog address twice")
	}
}

// replaceDescriptor stores d at a, where old is the descriptor a held or nil.
func (s *State) replaceDescriptor(a ids.ObjectAddress, old, d Descriptor) {
	s.updateCastIndex(old, d)
	s.putDescriptor(a, d)
	s.updateRelations(a, old, d)
	s.updateUnseeded(a, d)
	s.updateDefaultClasses(old, d)
	s.updateEventTriggers(old, d)
	s.updateSubscriptions(old, d)
	s.updateReplicationOrigins(old, d)
	s.updatePublications(old, d)
	s.updateHelperOwners(old, d)
	s.updateExtensionMembers(a, old, d)
	s.updateTemporaryNamespaces(old, d)
	s.raiseOIDCeiling(a, d)
}

// RemoveDescriptor deletes the descriptor at a.
func (s *State) RemoveDescriptor(a ids.ObjectAddress) {
	old, _ := s.findDescriptor(a)
	s.updateCastIndex(old, nil)
	s.updateDefaultClasses(old, nil)
	s.updateRelations(a, old, nil)
	s.updateUnseeded(a, nil)
	s.updateEventTriggers(old, nil)
	s.updateSubscriptions(old, nil)
	s.updateReplicationOrigins(old, nil)
	s.updatePublications(old, nil)
	s.updateHelperOwners(old, nil)
	s.updateExtensionMembers(a, old, nil)
	s.updateTemporaryNamespaces(old, nil)
	s.dropDescriptor(a)
}

// BindName binds k to a.
func (s *State) BindName(k ids.NameKey, a ids.ObjectAddress) { s.putName(k, a) }

// UnbindName removes the binding of k.
func (s *State) UnbindName(k ids.NameKey) { s.dropName(k) }

// ResetDescriptors gives s an empty descriptor map. Names stay as they are.
func (s *State) ResetDescriptors() {
	s.mustNotBulkLoad()
	s.descriptors = util.PersistentMap[ids.ObjectAddress, Descriptor]{}
	s.relations = util.PersistentMap[ids.ObjectAddress, *Relation]{}
	s.unseeded, s.unseededAll = util.PersistentMap[ids.ObjectAddress, Descriptor]{}, false
	s.procedureNames = nil
	s.castPairs = nil
	s.defaultClasses = util.PersistentMap[defaultClassPair, []ids.OID]{}
	s.eventTriggers = util.PersistentMap[ids.ObjectAddress, *EventTrigger]{}
	s.subscriptions = util.PersistentMap[ids.ObjectAddress, *Subscription]{}
	s.replicationOrigins = util.PersistentMap[ids.ObjectAddress, *ReplicationOrigin]{}
	s.publications = util.PersistentMap[ids.ObjectAddress, *Publication]{}
	s.publicationRelations = util.PersistentMap[ids.ObjectAddress, *PublicationRelation]{}
	s.publicationNamespaces = util.PersistentMap[ids.ObjectAddress, *PublicationNamespace]{}
	s.helperOwners = util.PersistentMap[ids.ObjectAddress, *Procedure]{}
	s.extensionMembers = util.PersistentMap[ids.ObjectAddress, ids.ObjectAddress]{}
	s.temporaryNamespaces = util.PersistentMap[ids.ObjectAddress, *Namespace]{}
	s.oidCeiling = 0
}

// ResetMaps gives s empty descriptor and name maps and iterates the previous
// descriptors, which s no longer uses. Writers that refill s use a bulk load.
func (s *State) ResetMaps() iter.Seq2[ids.ObjectAddress, Descriptor] {
	s.mustNotBulkLoad()
	previous := s.descriptors
	s.descriptors = util.PersistentMap[ids.ObjectAddress, Descriptor]{}
	s.relations = util.PersistentMap[ids.ObjectAddress, *Relation]{}
	s.unseeded, s.unseededAll = util.PersistentMap[ids.ObjectAddress, Descriptor]{}, false
	s.names = util.PersistentMap[ids.NameKey, ids.ObjectAddress]{}
	s.procedureNames = nil
	s.castPairs = nil
	s.defaultClasses = util.PersistentMap[defaultClassPair, []ids.OID]{}
	s.eventTriggers = util.PersistentMap[ids.ObjectAddress, *EventTrigger]{}
	s.subscriptions = util.PersistentMap[ids.ObjectAddress, *Subscription]{}
	s.replicationOrigins = util.PersistentMap[ids.ObjectAddress, *ReplicationOrigin]{}
	s.publications = util.PersistentMap[ids.ObjectAddress, *Publication]{}
	s.publicationRelations = util.PersistentMap[ids.ObjectAddress, *PublicationRelation]{}
	s.publicationNamespaces = util.PersistentMap[ids.ObjectAddress, *PublicationNamespace]{}
	s.helperOwners = util.PersistentMap[ids.ObjectAddress, *Procedure]{}
	s.extensionMembers = util.PersistentMap[ids.ObjectAddress, ids.ObjectAddress]{}
	s.temporaryNamespaces = util.PersistentMap[ids.ObjectAddress, *Namespace]{}
	s.oidCeiling = 0
	return previous.All()
}

package statistics

import (
	"slices"
	"stuntgres/internal/engine/ids"
	"time"
)

// ResetDatabase changes one database's published state atomically. Authority is
// supplied by the owning catalog before taking this leaf lock. Shared relation
// identities are classified by that catalog, not by names or provider callbacks.
// Epoch zero is reserved for database facts; object facts use the catalog epoch.
func (p *Provider) ResetDatabase(database Key, epoch ids.CatalogEpoch, shared []ids.OID, at time.Time) {
	if database.Category != Database || database.Object.Domain() == nil || database.Object.Address.ClassID != ids.DatabaseClassID || database.Object.Epoch != 0 || database.Object.Address.SubID != 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for key := range p.facts {
		if key.Object.Domain() != database.Object.Domain() || key.Object.Epoch != epoch || key.Object.Address.SubID != 0 {
			continue
		}
		address := key.Object.Address
		selected := key.Category == Function && address.ClassID == ids.ProcedureClassID || key.Category == Relation && address.ClassID == ids.RelationClassID && !slices.Contains(shared, address.ObjectID)
		if !selected {
			continue
		}
		if p.keyRetired(key) {
			continue
		}
		p.facts[key] = Fact{}
	}
	p.resetDatabaseTimestamp(database, at, true)
}

// ResetObject counters and its database timestamp share one local publication
// cut. A nil database is used only for a shared relation's peer partition; the
// actual default target stamps dboid zero in its object publication cut.
// Absent/retired keys never acquire entries; the qualified key is retained.
func (p *Provider) ResetObject(object ids.ObjectRef, category Category, database *Key, at time.Time) {
	class := ids.RelationClassID
	if category == Function {
		class = ids.ProcedureClassID
	} else if category != Relation {
		return
	}
	if object.Domain() == nil || object.Address.ClassID != class || object.Address.SubID != 0 {
		return
	}
	if database != nil && (database.Category != Database || database.Object.Domain() != object.Domain() || database.Object.Address.ClassID != ids.DatabaseClassID || database.Object.Epoch != 0 || database.Object.Address.SubID != 0) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	key := Key{Object: object, Category: category}
	if !p.keyRetired(key) {
		if _, exists := p.facts[key]; exists {
			p.facts[key] = Fact{}
		}
	}
	if database != nil {
		p.resetDatabaseTimestamp(*database, at, false)
	}
}

func (p *Provider) resetDatabaseTimestamp(key Key, at time.Time, clear bool) {
	if p.keyRetired(key) {
		return
	}
	if p.facts == nil {
		p.facts = make(map[Key]Fact)
	}
	fact := p.facts[key]
	if clear {
		fact.Database = DatabaseFact{}
	}
	fact.Database.ResetAt = at
	p.facts[key] = fact
}

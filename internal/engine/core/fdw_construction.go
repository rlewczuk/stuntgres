package core

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// ForeignConstruction owns CREATE's statement-private relation phases. Only this
// token can advance its immutable shell; ordinary catalog validation stays strict.
type ForeignConstruction struct {
	scope    *StatementScope
	expected *catalog.Relation
	foreign  *catalog.ForeignTableDefinition
	columns  []int32
	nextColumn int
}

// StageForeignConstruction strictly validates the complete creation stage and
// transfers it through ordinary admission into a retained private journal. No
// user code runs between transfer and installing the true foreign-relation shell.
func (s *StatementScope) StageForeignConstruction(stage *CatalogStage, address ids.ObjectAddress, columns []int32) (*ForeignConstruction, error) {
	e := s.Engine()
	if s.foreignConstructions[address] != nil || stage.Base.Get(address) != nil {
		return nil, fmt.Errorf("stuntgres: invalid foreign construction ownership")
	}
	prepared, err := e.ValidateCatalogStage(s.Op.Context(), stage)
	if err != nil {
		return nil, err
	}
	complete, ok := stage.Get(address).(*catalog.Relation)
	if !ok || complete.RelationKind != catalog.ForeignRelation || complete.Foreign == nil || len(complete.Foreign.Columns) != 0 {
		return nil, fmt.Errorf("stuntgres: invalid foreign construction descriptor")
	}
	for i, attribute := range columns {
		if !complete.HasAttribute(attribute) || i > 0 && columns[i-1] >= attribute {
			return nil, fmt.Errorf("stuntgres: invalid foreign column construction schedule")
		}
	}
	if s.Catalog == nil {
		s.Catalog = s.PendingCatalog().Clone()
	}
	token := &ForeignConstruction{scope: s, expected: complete, foreign: complete.Foreign, columns: slices.Clone(columns)}
	if s.foreignConstructions == nil {
		s.foreignConstructions = make(map[ids.ObjectAddress]*ForeignConstruction)
	}
	s.foreignConstructions[address] = token
	if _, err := e.PublishCatalogOperation(s.Op, s, prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		return nil, err
	}
	// Publication is forced private by s.Catalog. Its return and this journal
	// substitution contain no SQL/application callback or reentrant user hook.
	e.Mu.Lock()
	defer e.Mu.Unlock()
	current, ok := e.PrivateCatalogLocked(s.Catalog).Relation(address)
	if !ok || current != complete {
		return nil, catalog.Conflict{}
	}
	shell := *current
	shell.Foreign = nil
	e.CatalogChangeLocked(s.Catalog, address, &shell)
	token.expected = &shell
	return token, nil
}

// Advance installs only the validated phase delta, based on the exact latest
// private predecessor. Zero follows table validation; a positive number follows
// validation of that stable attribute's options. Callback mutation of this same
// relation fails rather than overwriting a change or resurrecting a dropped name.
// Column options transfer immutable payload ownership on success. They must be
// distinct from the mutable option slice passed to an application validator.
func (t *ForeignConstruction) Advance(attribute int32, options []catalog.ForeignOption) error {
	if t == nil || t.scope == nil {
		return fmt.Errorf("stuntgres: inactive foreign construction")
	}
	s := t.scope
	e := s.Engine()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err == nil {
		err = t.currentLocked()
	}
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	next := *t.expected
	if attribute == 0 {
		if next.Foreign != nil {
			e.Mu.Unlock()
			return fmt.Errorf("stuntgres: foreign table phase already complete")
		}
		foreign := *t.foreign
		next.Foreign = &foreign
	} else {
		if next.Foreign == nil || t.nextColumn >= len(t.columns) || attribute != t.columns[t.nextColumn] || len(options) == 0 || len(next.Foreign.Columns[attribute]) != 0 {
			e.Mu.Unlock()
			return fmt.Errorf("stuntgres: invalid foreign column construction phase")
		}
		foreign := *next.Foreign
		foreign.Columns = maps.Clone(foreign.Columns)
		foreign.Columns[attribute] = options
		next.Foreign = &foreign
	}
	next.ObjectRevision, err = e.Catalog.RevisionLocked()
	var stage *CatalogStage
	if err == nil {
		stage = e.BeginCatalogStage(auth.Catalog)
		stage.AuthorizeLocked(auth, s.Op.Session)
		err = stage.Replace(&next)
	}
	e.Mu.Unlock()
	if stage != nil {
		defer e.DiscardCatalogStage(stage)
	}
	if err != nil {
		return err
	}
	prepared, err := e.ValidateCatalogStage(s.Op.Context(), stage)
	if err != nil {
		return err
	}
	if _, err := e.PublishCatalogOperation(s.Op, s, prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		return err
	}
	t.expected = stage.Get(next.Address()).(*catalog.Relation)
	if attribute != 0 {
		t.nextColumn++
	}
	return nil
}

func (t *ForeignConstruction) currentLocked() error {
	s := t.scope
	if s == nil || s.Finished || s.foreignConstructions[t.expected.Address()] != t {
		return fmt.Errorf("stuntgres: inactive foreign construction")
	}
	read := s.Engine().PrivateCatalogLocked(s.Catalog)
	current, ok := read.Relation(t.expected.Address())
	if read.Epoch() != s.TxRef.Epoch() || !ok || current != t.expected {
		return catalog.Conflict{}
	}
	return nil
}

// Complete proves the actual final private descriptor, not a saved candidate,
// before retiring this token alone. Finish performs its ordinary full proof too.
func (t *ForeignConstruction) Complete() error {
	if t == nil || t.scope == nil {
		return fmt.Errorf("stuntgres: inactive foreign construction")
	}
	s := t.scope
	e := s.Engine()
	e.Mu.Lock()
	err := t.currentLocked()
	read := e.PrivateCatalogLocked(s.Catalog)
	current := t.expected
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	if current.Foreign == nil || current.Foreign.Server != t.foreign.Server || !slices.Equal(current.Foreign.Options, t.foreign.Options) {
		return fmt.Errorf("stuntgres: incomplete foreign construction")
	}
	if t.nextColumn != len(t.columns) {
		return fmt.Errorf("stuntgres: incomplete foreign column construction")
	}
	if err := catalog.Validate(e.Catalog.Provider(ids.RelationClassID), read, current); err != nil {
		return err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := t.currentLocked(); err != nil {
		return err
	}
	delete(s.foreignConstructions, current.Address())
	t.invalidate()
	return nil
}

func (t *ForeignConstruction) invalidate() {
	t.scope, t.expected, t.foreign, t.columns = nil, nil, nil, nil
	t.nextColumn = 0
}

// A checkpoint keeps token identity and a value snapshot: a shallow pointer-map
// clone cannot undo Advance or reactivate a token completed inside the suffix.
// Descriptor, option and schedule backing is immutable and safely shared.
type foreignConstructionCheckpoint struct {
	token *ForeignConstruction
	state ForeignConstruction
}

func (s *StatementScope) checkpointForeignConstructions() map[ids.ObjectAddress]foreignConstructionCheckpoint {
	if len(s.foreignConstructions) == 0 {
		return nil
	}
	saved := make(map[ids.ObjectAddress]foreignConstructionCheckpoint, len(s.foreignConstructions))
	for address, token := range s.foreignConstructions {
		saved[address] = foreignConstructionCheckpoint{token: token, state: *token}
	}
	return saved
}

// Exception rollback restores exactly the catalog predecessor and token state
// captured together. Completed suffix tokens are already inactive; no registry
// of all historical tokens is needed. Caller holds statement/owner admission.
func (s *StatementScope) restoreForeignConstructions(saved map[ids.ObjectAddress]foreignConstructionCheckpoint) {
	for address, token := range s.foreignConstructions {
		prior, exists := saved[address]
		if !exists || prior.token != token {
			token.invalidate()
		}
	}
	if len(saved) == 0 {
		s.foreignConstructions = nil
		return
	}
	active := s.foreignConstructions
	if active == nil {
		active = make(map[ids.ObjectAddress]*ForeignConstruction, len(saved))
	} else {
		clear(active)
	}
	for address, prior := range saved {
		*prior.token = prior.state
		active[address] = prior.token
	}
	s.foreignConstructions = active
}

package engine

import (
	"stuntgres/internal/engine/catalog"
)

type publicationConflictProvider struct {
	catalog.Provider
	publishWinner func(catalog.Descriptor)
}

func (p *publicationConflictProvider) Validate(view catalog.Read, d catalog.Descriptor) error {
	if err := p.Provider.Validate(view, d); err != nil {
		return err
	}
	if publish := p.publishWinner; publish != nil {
		p.publishWinner = nil
		publish(d)
	}
	return nil
}

package extension

import (
	"context"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// DictionaryPreparation retains semantic inputs, not authorization decisions.
// Compilation consumes shipped data without holding the engine catalog mutex.
type DictionaryPreparation struct {
	Template  *catalog.TSTemplate
	Original  *catalog.TSDictionary
	Namespace catalog.Descriptor
	Options   []catalog.DictionaryOption
	Runtime   catalog.DictionaryRuntime
}

func dictionaryCreateInputs(h DictionaryHost, auth ddl.Authority, s *parser.CreateDictionary) (*catalog.TSTemplate, []catalog.DictionaryOption, error) {
	var template *catalog.TSTemplate
	options := make([]catalog.DictionaryOption, 0, len(s.Options))
	for _, o := range s.Options {
		if o.Name.Text == "template" {
			if !o.Supplied {
				return nil, nil, dictionaryError("42601", "template requires a parameter")
			}
			source := o.Source
			if source == nil {
				source = &parser.ObjectName{Parts: []parser.Identifier{{Text: o.Value}}}
			}
			raw, err := dictionaryLookup(h, auth, *source, ids.TSTemplateClassID)
			if err != nil {
				return nil, nil, err
			}
			template = raw.(*catalog.TSTemplate)
		} else {
			options = append(options, dictionaryOption(o))
		}
	}
	if template == nil {
		return nil, nil, dictionaryError("42P17", "text search template is required")
	}
	return template, options, nil
}

func PrepareDictionary(h DictionaryHost, auth ddl.Authority, stmt parser.Statement) (DictionaryPreparation, error) {
	var p DictionaryPreparation
	switch s := stmt.(type) {
	case *parser.CreateDictionary:
		ns, current, err := h.CreationNamespace(auth, s.Name)
		if err != nil {
			return p, err
		}
		auth = current
		if err = h.CheckCreateIn(auth, ns); err != nil {
			return p, err
		}
		p.Namespace = h.StageRead().Get(ns.Address)
		template, options, err := dictionaryCreateInputs(h, auth, s)
		if err != nil {
			return p, err
		}
		p.Template = template
		p.Options = options
	case *parser.AlterDictionary:
		raw, err := dictionaryLookup(h, auth, s.Name, ids.TSDictionaryClassID)
		if err != nil {
			return p, err
		}
		p.Original = raw.(*catalog.TSDictionary)
		if err = dictionaryAlterAuthority(h, auth, p.Original, s); err != nil {
			return p, err
		}
		p.Namespace = h.StageRead().Get(p.Original.Namespace())
		if s.Action != "options" {
			return p, nil
		}
		template, ok := h.StageRead().TSTemplate(p.Original.Template)
		if !ok {
			return p, InvalidProviderDescriptor()
		}
		p.Template = template
		p.Options = MergeDictionaryOptions(p.Original.Options, dictionaryOptions(s.Options))
	}
	return p, nil
}
func (p *DictionaryPreparation) Compile(ctx context.Context) error {
	if p.Template == nil {
		return ctx.Err()
	}
	var err error
	p.Runtime, err = CompileDictionary(ctx, p.Template, p.Options)
	return err
}
func (p *DictionaryPreparation) Current(view catalog.Read) bool {
	return (p.Template == nil || view.Get(p.Template.Address()) == p.Template) && (p.Original == nil || view.Get(p.Original.Address()) == p.Original) && (p.Namespace == nil || view.Get(p.Namespace.Address()) == p.Namespace)
}

type preparedDictionaryHost struct {
	DictionaryHost
	preparation *DictionaryPreparation
}

func (p *DictionaryPreparation) Host(h DictionaryHost) DictionaryHost {
	return preparedDictionaryHost{h, p}
}
func initializeDictionary(h DictionaryHost, template *catalog.TSTemplate, options []catalog.DictionaryOption) (catalog.DictionaryRuntime, error) {
	if prepared, ok := h.(preparedDictionaryHost); ok {
		p := prepared.preparation
		if p.Template != template || p.Runtime == nil || !slices.Equal(p.Options, options) {
			return nil, ids.ErrCatalogReplaced
		}
		return p.Runtime, h.Context().Err()
	}
	return CompileDictionary(h.Context(), template, options)
}

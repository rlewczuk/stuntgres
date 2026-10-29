package regobj

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

// ObserveDictionary captures current executable inputs independently of the
// original regdictionary/configuration operand's identity. Alias input itself
// does not initialize a dictionary: unresolved/denied option inputs produce an
// incomplete observation, invalidating a prior fold without inventing an alias
// input error. The executing acquisition owns the exact semantic diagnostic.
func (r TextSearchInput) ObserveDictionary(ctx context.Context, d *catalog.TSDictionary) ([]catalog.ExpressionDependency, error) {
	r.qualified, r.resolved = nil, nil
	view := r.capture.Authority.View
	read := view.Catalog
	dependencies := []catalog.ExpressionDependency{{Ref: view.Ref(d.Address()), Revision: d.Revision()}}
	template, ok := read.TSTemplate(d.Template)
	if !ok {
		return dependencies, nil
	}
	add := func(descriptor catalog.Descriptor) {
		if descriptor != nil {
			dependencies = append(dependencies, catalog.ExpressionDependency{Ref: view.Ref(descriptor.Address()), Revision: descriptor.Revision()})
		}
	}
	add(template)
	add(read.Get(template.Init))
	add(read.Get(template.Lexize))
	if !extension.IsThesaurus(read, d) {
		return dependencies, nil
	}
	for _, option := range d.Options {
		if option.Name != "dictionary" {
			continue
		}
		parts, _, err := r.name(ctx, option.Value)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return dependencies, nil
		}
		path, err := r.namespaces(parts, true)
		if err != nil {
			return dependencies, nil
		}
		for i := range path.Count() {
			address := read.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: path.At(i), Name: parts[len(parts)-1].Text})
			source, ok := read.TSDictionary(address)
			if !ok {
				continue
			}
			add(read.Get(source.Namespace()))
			add(source)
			if sourceTemplate, ok := read.TSTemplate(source.Template); ok {
				add(sourceTemplate)
				add(read.Get(sourceTemplate.Init))
				add(read.Get(sourceTemplate.Lexize))
			}
			return dependencies, nil
		}
		return dependencies, nil
	}
	return dependencies, nil
}

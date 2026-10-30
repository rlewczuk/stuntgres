package extension

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func IspellTemplate(d *catalog.TSTemplate) bool {
	return d != nil && d.Address() == (ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3733}) && d.Implementation == nil && d.Init == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3731}) && d.Lexize == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3732})
}

func ispellOptions(options []catalog.DictionaryOption, load func(int, string) error) ([3]string, error) {
	var names [3]string
	var seen [3]bool
	for _, o := range options {
		index := 0
		label := "DictFile"
		switch o.Name {
		case "dictfile":
		case "afffile":
			index = 1
			label = "AffFile"
		case "stopwords":
			index = 2
			label = "StopWords"
		default:
			return names, dictionaryError("22023", fmt.Sprintf("unrecognized Ispell parameter: \"%s\"", o.Name))
		}
		if seen[index] {
			return names, dictionaryError("22023", "multiple "+label+" parameters")
		}
		seen[index] = true
		if !o.Supplied {
			return names, dictionaryError("42601", o.Name+" requires a parameter")
		}
		names[index] = o.Value
		if load != nil {
			if err := load(index, o.Value); err != nil {
				return names, err
			}
		}
	}
	if !seen[1] {
		return names, dictionaryError("22023", "missing AffFile parameter")
	}
	if !seen[0] {
		return names, dictionaryError("22023", "missing DictFile parameter")
	}
	return names, nil
}
func compileIspell(ctx context.Context, files *functions.DictionaryFiles, options []catalog.DictionaryOption) (catalog.DictionaryRuntime, error) {
	compiler := functions.NewIspellCompiler(files)
	_, err := ispellOptions(options, func(index int, name string) error {
		switch index {
		case 0:
			return compiler.LoadDictionary(ctx, name)
		case 1:
			return compiler.LoadAffixes(ctx, name)
		default:
			return compiler.LoadStopwords(ctx, name)
		}
	})
	if err != nil {
		return nil, err
	}
	return compiler.Finish(ctx)
}

package extension

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// ThesaurusInput is detached compiler input. It retains no caller authority.
type ThesaurusInput struct {
	Resource, LogicalName string
	Normalizer            *catalog.TSDictionary
	Template              *catalog.TSTemplate
	SourceError           error
	OptionError           error
	ReadResource          bool
}

func thesaurusOptions(options []catalog.DictionaryOption) (resource, name string, err error) {
	resource, name, _, err = thesaurusOptionInputs(options)
	return resource, name, err
}

// Preserve the ordered resource acquisition point separately from later option
// validation. Opening shipped data runs detached, never while capturing under Mu.
func thesaurusOptionInputs(options []catalog.DictionaryOption) (resource, name string, readResource bool, err error) {
	seenFile, seenDictionary := false, false
	for _, option := range options {
		switch option.Name {
		case "dictfile":
			if seenFile {
				return resource, name, readResource, dictionaryError("22023", "multiple DictFile parameters")
			}
			seenFile = true
			if !option.Supplied {
				return resource, name, readResource, dictionaryError("42601", "dictfile requires a parameter")
			}
			resource, readResource = option.Value, true
		case "dictionary":
			if seenDictionary {
				return resource, name, readResource, dictionaryError("22023", "multiple Dictionary parameters")
			}
			seenDictionary = true
			if !option.Supplied {
				return resource, name, readResource, dictionaryError("42601", "dictionary requires a parameter")
			}
			name = option.Value
		default:
			return resource, name, readResource, dictionaryError("22023", fmt.Sprintf(`unrecognized Thesaurus parameter: "%s"`, option.Name))
		}
	}
	if !seenFile {
		return resource, name, readResource, dictionaryError("22023", "missing DictFile parameter")
	}
	if !seenDictionary {
		return resource, name, readResource, dictionaryError("22023", "missing Dictionary parameter")
	}
	return resource, name, readResource, nil
}

// Normalizer options are source operands, not trusted replay destinations.
// Reuse the existing semantic utility lookup policy to enforce caller USAGE.
type normalizerLookupHost struct{ DictionaryHost }

func (h normalizerLookupHost) SearchNamespaces(auth ddl.Authority, name parser.ObjectName, class ids.OID) ([]ids.ObjectAddress, error) {
	return h.SearchConfigurationNamespaces(auth, name, class)
}

// CaptureThesaurusInput runs under the catalog mutex, without resource I/O or
// callbacks. Resolution errors are delivered after resource validation.
func CaptureThesaurusInput(h DictionaryHost, auth ddl.Authority, template *catalog.TSTemplate, options []catalog.DictionaryOption) (*ThesaurusInput, error) {
	if dictionaryCapability(h.AccessView(auth).Catalog, template) != 5 {
		return nil, nil
	}
	resource, logical, readResource, optionError := thesaurusOptionInputs(options)
	input := &ThesaurusInput{Resource: resource, LogicalName: logical, ReadResource: readResource, OptionError: optionError}
	if optionError != nil {
		return input, nil
	}
	name, err := parser.ParseRegclassName(logical)
	if err != nil {
		input.SourceError = dictionaryError("42602", "invalid name syntax")
		return input, nil
	}
	for i := range name.Parts {
		name.Parts[i].Pos = parser.Position{}
	}
	raw, err := dictionaryLookup(normalizerLookupHost{h}, auth, name, ids.TSDictionaryClassID)
	if err != nil {
		input.SourceError = err
		return input, nil
	}
	input.Normalizer = raw.(*catalog.TSDictionary)
	view := h.AccessView(auth).Catalog
	input.Template, _ = view.TSTemplate(input.Normalizer.Template)
	if input.Template == nil {
		input.SourceError = InvalidProviderDescriptor()
	} else if dictionaryCapability(view, input.Template) == 5 {
		input.SourceError = dictionaryError("XX000", "forbidden call of thesaurus or nested call")
	} else {
		input.SourceError = validateDictionary(view, input.Normalizer)
	}
	return input, nil
}

func (input *ThesaurusInput) Compile(ctx context.Context) (catalog.DictionaryRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !input.ReadResource {
		return nil, input.OptionError
	}
	rules, err := functions.ReadThesaurusRules(ctx, input.Resource)
	if err != nil {
		return nil, err
	}
	if input.OptionError != nil {
		return nil, input.OptionError
	}
	if input.SourceError != nil {
		return nil, input.SourceError
	}
	return functions.CompileThesaurusDictionary(ctx, rules, input.Normalizer.Runtime)
}

func (input *ThesaurusInput) Current(view catalog.Read) bool {
	if input == nil {
		return true
	}
	if input.OptionError != nil || input.SourceError != nil || input.Normalizer == nil || input.Template == nil {
		return false
	}
	if view.Get(input.Normalizer.Address()) != input.Normalizer || view.Get(input.Template.Address()) != input.Template {
		return false
	}
	return dictionaryCapability(view, input.Template) != 0
}

func IsThesaurus(view catalog.Read, dictionary *catalog.TSDictionary) bool {
	template, ok := view.TSTemplate(dictionary.Template)
	return ok && dictionaryCapability(view, template) == 5
}

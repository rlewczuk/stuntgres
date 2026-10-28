package extension

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func dictionaryError(code, message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

// CompileDictionary compiles immutable execution state before catalog publication.
func CompileDictionary(ctx context.Context, view catalog.Read, template *catalog.TSTemplate, options []catalog.DictionaryOption) (catalog.DictionaryRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if template == nil {
		return nil, InvalidProviderDescriptor()
	}
	if err := validateTemplate(view, template); err != nil {
		return nil, err
	}
	if template.Init == (ids.ObjectAddress{}) && len(options) != 0 {
		return nil, dictionaryError("42601", fmt.Sprintf(`text search template "%s" does not accept options`, template.Name()))
	}
	capability := dictionaryCapability(view, template)
	if capability == 3 {
		return compileSnowballOptions(ctx, options)
	}
	if capability == 4 {
		resource, sensitive, err := synonymOptions(options)
		if err != nil {
			return nil, err
		}
		return functions.CompileSynonymDictionary(ctx, resource, sensitive)
	}
	if capability == 1 {
		accept, stopwords, err := simpleOptions(options)
		if err != nil {
			return nil, err
		}
		return functions.CompileSimpleDictionary(ctx, accept, stopwords)
	}
	if capability == 2 {
		rules := false
		var resource string
		for _, o := range options {
			if o.Name != "rules" {
				return nil, dictionaryError("22023", fmt.Sprintf(`unrecognized Unaccent parameter: "%s"`, o.Name))
			}
			if rules {
				return nil, dictionaryError("22023", "multiple Rules parameters")
			}
			rules = true
			if !o.Supplied {
				return nil, dictionaryError("42601", "rules requires a parameter")
			}
			resource = o.Value
		}
		if !rules {
			return nil, dictionaryError("22023", "missing Rules parameter")
		}
		return functions.CompileUnaccentDictionary(ctx, resource)
	}
	return nil, dictionaryError("0A000", fmt.Sprintf(`native dictionary callback execution is not supported for text search template "%s"`, template.Name()))
}

func simpleOptions(options []catalog.DictionaryOption) (bool, string, error) {
	accept, stopwords := true, ""
	seenAccept, seenStopwords := false, false
	for _, o := range options {
		switch o.Name {
		case "accept":
			if seenAccept {
				return false, "", dictionaryError("22023", "multiple Accept parameters")
			}
			seenAccept = true
			if !o.Supplied {
				continue
			}
			if o.Numeric {
				n, err := strconv.ParseInt(o.Value, 10, 32)
				if err != nil || n != 0 && n != 1 {
					return false, "", dictionaryError("42601", "accept requires a Boolean value")
				}
				accept = n == 1
			} else {
				switch {
				case strings.EqualFold(o.Value, "true"), strings.EqualFold(o.Value, "on"):
					accept = true
				case strings.EqualFold(o.Value, "false"), strings.EqualFold(o.Value, "off"):
					accept = false
				default:
					return false, "", dictionaryError("42601", "accept requires a Boolean value")
				}
			}
		case "stopwords":
			if seenStopwords {
				return false, "", dictionaryError("22023", "multiple StopWords parameters")
			}
			seenStopwords = true
			if !o.Supplied {
				return false, "", dictionaryError("42601", "stopwords requires a parameter")
			}
			stopwords = o.Value
		default:
			return false, "", dictionaryError("22023", fmt.Sprintf(`unrecognized simple dictionary parameter: "%s"`, o.Name))
		}
	}
	return accept, stopwords, nil
}

func synonymOptions(options []catalog.DictionaryOption) (string, bool, error) {
	resource, sensitive, supplied := "", false, false
	for _, o := range options {
		switch o.Name {
		case "synonyms":
			if !o.Supplied {
				return "", false, dictionaryError("42601", "synonyms requires a parameter")
			}
			resource, supplied = o.Value, true
		case "casesensitive":
			// Bare CREATE reaches serialization only after successful init.
			// Bare ALTER options have already been removed by the merger.
			sensitive = true
			if !o.Supplied {
				continue
			}
			if o.Numeric {
				n, err := strconv.ParseInt(o.Value, 10, 32)
				if err != nil || n != 0 && n != 1 {
					return "", false, dictionaryError("42601", "casesensitive requires a Boolean value")
				}
				sensitive = n == 1
			} else {
				switch {
				case strings.EqualFold(o.Value, "true"), strings.EqualFold(o.Value, "on"):
					sensitive = true
				case strings.EqualFold(o.Value, "false"), strings.EqualFold(o.Value, "off"):
					sensitive = false
				default:
					return "", false, dictionaryError("42601", "casesensitive requires a Boolean value")
				}
			}
		default:
			return "", false, dictionaryError("22023", fmt.Sprintf(`unrecognized synonym parameter: "%s"`, o.Name))
		}
	}
	if !supplied {
		return "", false, dictionaryError("22023", "missing Synonyms parameter")
	}
	return resource, sensitive, nil
}

// MergeDictionaryOptions implements PostgreSQL's remove-then-append ALTER order.
func MergeDictionaryOptions(old, changes []catalog.DictionaryOption) []catalog.DictionaryOption {
	result := slices.Clone(old)
	for _, change := range changes {
		result = slices.DeleteFunc(result, func(o catalog.DictionaryOption) bool { return o.Name == change.Name })
		if change.Supplied {
			result = append(result, change)
		}
	}
	return result
}

func validateDictionary(v catalog.Read, d *catalog.TSDictionary) error {
	template, ok := v.TSTemplate(d.Template)
	if !ok || d.Runtime == nil || d.Owner.ID() == 0 {
		return InvalidProviderDescriptor()
	}
	if v.Owner() == nil || v.Owner().Principals == nil || d.Owner != v.Owner().Principals.Principal(d.Owner.ID()) {
		return InvalidProviderDescriptor()
	}
	if err := (CatalogProvider{Class: ids.TSTemplateClassID}).Validate(v, template); err != nil {
		return err
	}
	for _, option := range d.Options {
		if !option.Supplied {
			return InvalidProviderDescriptor()
		}
	}
	if dictionaryCapability(v, template) == 1 {
		runtime, ok := d.Runtime.(*functions.SimpleDictionary)
		accept, stopwords, err := simpleOptions(d.Options)
		if !ok || err != nil || !runtime.Configured(accept, stopwords) || d.Implementation != nil {
			return InvalidProviderDescriptor()
		}
	} else if dictionaryCapability(v, template) == 4 {
		runtime, ok := d.Runtime.(*functions.SynonymDictionary)
		resource, sensitive, err := synonymOptions(d.Options)
		if !ok || err != nil || !runtime.Configured(resource, sensitive) || d.Implementation != nil || d.Extension != (ids.ObjectAddress{}) {
			return InvalidProviderDescriptor()
		}
	} else if dictionaryCapability(v, template) == 3 {
		runtime, ok := d.Runtime.(*functions.SnowballDictionary)
		language, stopwords := "", ""
		seenLanguage, seenStopwords := false, false
		for _, option := range d.Options {
			switch option.Name {
			case "language":
				if seenLanguage {
					return InvalidProviderDescriptor()
				}
				seenLanguage, language = true, option.Value
			case "stopwords":
				if seenStopwords {
					return InvalidProviderDescriptor()
				}
				seenStopwords, stopwords = true, option.Value
			default:
				return InvalidProviderDescriptor()
			}
		}
		if !ok || !seenLanguage || !runtime.Configured(language, stopwords) || d.Implementation != nil || d.Extension != (ids.ObjectAddress{}) {
			return InvalidProviderDescriptor()
		}
	} else if dictionaryCapability(v, template) == 2 {
		if d.Runtime != functions.StockUnaccent {
			return InvalidProviderDescriptor()
		}
		if len(d.Options) != 1 || d.Options[0].Name != "rules" || !d.Options[0].Supplied || d.Options[0].Value != "unaccent" {
			return InvalidProviderDescriptor()
		}
		if d.Extension != (ids.ObjectAddress{}) && (UnaccentDictionaryProviderOf(d) != StockUnaccent || d.Extension != template.Extension) {
			return InvalidProviderDescriptor()
		}
	} else {
		return InvalidProviderDescriptor()
	}
	return nil
}

package functions

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// DictionaryFiles is immutable trusted startup data, not a filesystem or loader.
// The constructor copies the map; its strings already own immutable bytes.
// A Driver shares one value with all its engines, including replacement stages.
type DictionaryFiles struct{ files map[string]string }

func NewDictionaryFiles(files map[string]string) *DictionaryFiles {
	return &DictionaryFiles{files: maps.Clone(files)}
}

func (f *DictionaryFiles) resource(ctx context.Context, name, suffix, kind string) (string, error) {
	if err := CheckDictionaryResourceName(ctx, name); err != nil {
		return "", err
	}
	file := name + "." + suffix
	if f != nil {
		if data, ok := f.files[file]; ok {
			return data, ctx.Err()
		}
	}
	if file == "english.stop" {
		data, err := dictionaryResources.ReadFile(file)
		return string(data), err
	}
	return "", dictionaryError("F0000", fmt.Sprintf("could not open %s file %q: No such file or directory", kind, file))
}

// readDictionaryLines preserves original lines (including their terminating
// newline) for PostgreSQL's resource error Context. There is no scanner size cap.
func readDictionaryLines(ctx context.Context, file, data string, consume func(string) error) error {
	for line, at := 1, 0; at < len(data); line++ {
		end := strings.IndexByte(data[at:], '\n')
		if end < 0 {
			end = len(data)
		} else {
			end += at + 1
		}
		original := data[at:end]
		decorate := func(err error, valid bool) error {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) {
				return err
			}
			owned := *diagnostic
			owned.Context = fmt.Sprintf("line %d of configuration file %q", line, file)
			if valid {
				owned.Context += ": \"" + original + "\""
			}
			return &owned
		}
		for i := 0; i < len(original); {
			if err := ctx.Err(); err != nil {
				return err
			}
			_, width := utf8.DecodeRuneInString(original[i:])
			if original[i] == 0 || width == 1 && original[i] >= utf8.RuneSelf {
				return decorate(sqltype.ValidateTextInput(original[i:]), false)
			}
			i += width
		}
		if err := consume(original); err != nil {
			return decorate(err, true)
		}
		at = end
	}
	return ctx.Err()
}

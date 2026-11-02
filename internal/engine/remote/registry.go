package remote

import (
	"errors"
	"reflect"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/catalog"
)

func ValidateDefinition(def catalog.ExternalSQLProviderDefinition) error {
	if def.Name == "" || len(def.Name) > 63 || !utf8.ValidString(def.Name) || strings.IndexByte(def.Name, 0) >= 0 {
		return errors.New("stuntgres: invalid external SQL provider identifier")
	}
	if strings.TrimSpace(def.Version) == "" || !utf8.ValidString(def.Version) || strings.IndexByte(def.Version, 0) >= 0 {
		return errors.New("stuntgres: invalid external SQL provider version")
	}
	if Nil(def.Provider) {
		return errors.New("stuntgres: nil external SQL provider")
	}
	return nil
}

func Nil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

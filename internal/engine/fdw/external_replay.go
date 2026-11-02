package fdw

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
)

const ExternalReplayPrefix = "stuntgres-external-sql-v1 "

type ExternalRequirement struct {
	Handler           catalog.ForeignFunctionName
	Provider, Version string
}

func ExternalRequirements(view catalog.Read) []ExternalRequirement {
	var required []ExternalRequirement
	for _, descriptor := range view.Descriptors() {
		p, ok := descriptor.(*catalog.Procedure)
		if !ok || p.Foreign == nil || p.Foreign.ExternalProvider == nil {
			continue
		}
		def := p.Foreign.ExternalProvider
		required = append(required, ExternalRequirement{p.Foreign.Name, def.Name, def.Version})
	}
	slices.SortFunc(required, func(a, b ExternalRequirement) int {
		if n := strings.Compare(a.Handler.Schema, b.Handler.Schema); n != 0 {
			return n
		}
		return strings.Compare(a.Handler.Name, b.Handler.Name)
	})
	return required
}

func (r ExternalRequirement) Comment() string {
	data, _ := json.Marshal(r)
	return "-- " + ExternalReplayPrefix + base64.RawStdEncoding.EncodeToString(data) + "\n"
}

func DecodeExternalRequirement(comment string) (ExternalRequirement, bool, error) {
	comment = strings.TrimSpace(comment)
	if !strings.HasPrefix(comment, "stuntgres-external-sql-") {
		return ExternalRequirement{}, false, nil
	}
	invalid := errors.New("stuntgres: malformed external SQL prerequisite")
	if !strings.HasPrefix(comment, ExternalReplayPrefix) {
		return ExternalRequirement{}, true, invalid
	}
	data, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(comment, ExternalReplayPrefix))
	if err != nil {
		return ExternalRequirement{}, true, invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var r ExternalRequirement
	if err := decoder.Decode(&r); err != nil {
		return r, true, invalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || r.Handler.Schema == "" || r.Handler.Name == "" || r.Provider == "" || r.Version == "" {
		return r, true, invalid
	}
	canonical, _ := json.Marshal(r)
	if !bytes.Equal(canonical, data) {
		return r, true, invalid
	}
	return r, true, nil
}

func (r *Registry) ValidateExternalRequirement(required ExternalRequirement) error {
	for token := range r.ProcedureTokens() {
		if token.Name == required.Handler && token.ExternalProvider != nil && token.ExternalProvider.Name == required.Provider && token.ExternalProvider.Version == required.Version {
			return nil
		}
	}
	return errors.New("stuntgres: missing or incompatible registered external SQL provider binding")
}

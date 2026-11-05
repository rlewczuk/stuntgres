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
	Kind              string
	Handler           catalog.ForeignFunctionName
	Provider, Version string
}

func ExternalRequirements(view catalog.Read, dblink, postgres *catalog.ExternalSQLProviderDefinition) []ExternalRequirement {
	var required []ExternalRequirement
	dblinkRequired, postgresRequired := false, false
	for _, descriptor := range view.Descriptors() {
		if installation, ok := catalog.OriginInstallation(descriptor); ok && installation.Provider == "dblink" {
			dblinkRequired = true
		}
		if installation, ok := catalog.OriginInstallation(descriptor); ok && installation.Provider == "postgres_fdw" {
			postgresRequired = true
		}
		p, ok := descriptor.(*catalog.Procedure)
		if !ok || p.Foreign == nil || p.Foreign.ExternalProvider == nil {
			continue
		}
		def := p.Foreign.ExternalProvider
		required = append(required, ExternalRequirement{Kind: "handler", Handler: p.Foreign.Name, Provider: def.Name, Version: def.Version})
	}
	if dblinkRequired {
		requirement := ExternalRequirement{Kind: "dblink"}
		if dblink != nil {
			requirement.Provider, requirement.Version = dblink.Name, dblink.Version
		}
		required = append(required, requirement)
	}
	if postgresRequired {
		requirement := ExternalRequirement{Kind: "postgres_fdw"}
		if postgres != nil {
			requirement.Provider, requirement.Version = postgres.Name, postgres.Version
		}
		required = append(required, requirement)
	}
	slices.SortFunc(required, func(a, b ExternalRequirement) int {
		if n := strings.Compare(a.Kind, b.Kind); n != 0 {
			return n
		}
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
	identity := r.Provider != "" && r.Version != ""
	unbound := r.Provider == "" && r.Version == ""
	validKind := r.Kind == "handler" && r.Handler.Schema != "" && r.Handler.Name != "" && identity ||
		(r.Kind == "dblink" || r.Kind == "postgres_fdw") && r.Handler == (catalog.ForeignFunctionName{}) && (identity || unbound)
	if decoder.Decode(&extra) != io.EOF || !validKind {
		return r, true, invalid
	}
	canonical, _ := json.Marshal(r)
	if !bytes.Equal(canonical, data) {
		return r, true, invalid
	}
	return r, true, nil
}

func (r *Registry) ValidateExternalRequirement(required ExternalRequirement) error {
	if required.Kind == "dblink" || required.Kind == "postgres_fdw" {
		if required.Provider == "" && required.Version == "" {
			return nil
		}
		provider := r.bindings.Dblink
		if required.Kind == "postgres_fdw" {
			provider = r.bindings.PostgresFDW
		}
		if provider != nil && provider.Name == required.Provider && provider.Version == required.Version {
			return nil
		}
		return errors.New("stuntgres: missing or incompatible registered " + required.Kind + " external SQL provider binding")
	}
	for token := range r.ProcedureTokens() {
		if token.Name == required.Handler && token.ExternalProvider != nil && token.ExternalProvider.Name == required.Provider && token.ExternalProvider.Version == required.Version {
			return nil
		}
	}
	return errors.New("stuntgres: missing or incompatible registered external SQL provider binding")
}

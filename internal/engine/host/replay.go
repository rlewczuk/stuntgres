package host

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const ReplayPrefix = "stuntgres-host-runtime-v2 "

type Requirement struct{ Provider, Fingerprint string }

func (r *Runtime) ReplayComment() string {
	data, _ := json.Marshal(Requirement{r.Provider(), r.Fingerprint()})
	return "-- " + ReplayPrefix + base64.RawStdEncoding.EncodeToString(data) + "\n"
}
func DecodeRequirement(comment string) (Requirement, bool, error) {
	comment = strings.TrimSpace(comment)
	if !strings.HasPrefix(comment, "stuntgres-host-runtime-") {
		return Requirement{}, false, nil
	}
	if !strings.HasPrefix(comment, ReplayPrefix) {
		return Requirement{}, true, fmt.Errorf("stuntgres: unsupported host runtime prerequisite version")
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(comment, ReplayPrefix))
	if err != nil {
		return Requirement{}, true, fmt.Errorf("stuntgres: malformed host runtime prerequisite")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var r Requirement
	if err = decoder.Decode(&r); err != nil {
		return r, true, fmt.Errorf("stuntgres: malformed host runtime prerequisite: %w", err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || r.Provider == "" || len(r.Fingerprint) != 64 {
		return r, true, fmt.Errorf("stuntgres: invalid host runtime prerequisite")
	}
	canonical, _ := json.Marshal(r)
	if !bytes.Equal(canonical, raw) {
		return Requirement{}, true, fmt.Errorf("stuntgres: noncanonical host runtime prerequisite")
	}
	return r, true, nil
}
func ValidateRequirement(required Requirement, inventory []*Runtime) error {
	for _, r := range inventory {
		if r.Provider() == required.Provider {
			if r.Fingerprint() == required.Fingerprint {
				return nil
			}
			return fmt.Errorf("stuntgres: incompatible registered host runtime %q", required.Provider)
		}
	}
	return fmt.Errorf("stuntgres: required host runtime %q is not registered", required.Provider)
}

package host

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const AbsencePrefix = "stuntgres-host-pair-absence-v1 "

// PairAbsence carries logical identity, never source numeric catalog addresses.
type PairAbsence struct {
	Provider, ABI, Language string
	Type                    Type
	SQLName                 Name
}
type PairAbsences struct{ Pairs []PairAbsence }

func (r PairAbsences) Comment() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return "-- " + AbsencePrefix + base64.RawStdEncoding.EncodeToString(data) + "\n", nil
}
func (r PairAbsences) Validate() error {
	seen := make(map[PairAbsence]bool, len(r.Pairs))
	for _, p := range r.Pairs {
		if p.Provider == "" || p.ABI == "" || p.Language == "" || !validType(p.Type) || !validName(p.SQLName) || seen[p] {
			return fmt.Errorf("stuntgres: invalid or duplicate host pair absence")
		}
		seen[p] = true
	}
	return nil
}
func DecodePairAbsences(comment string) (PairAbsences, bool, error) {
	text := strings.TrimSpace(comment)
	if !strings.HasPrefix(text, "stuntgres-host-pair-absence-") {
		return PairAbsences{}, false, nil
	}
	bad := func() (PairAbsences, bool, error) {
		return PairAbsences{}, true, fmt.Errorf("stuntgres: malformed host pair absence record")
	}
	if !strings.HasPrefix(text, AbsencePrefix) {
		return bad()
	}
	data, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(text, AbsencePrefix))
	if err != nil {
		return bad()
	}
	var r PairAbsences
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&r) != nil {
		return bad()
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return bad()
	}
	if err = r.Validate(); err != nil {
		return PairAbsences{}, true, err
	}
	// Duplicate JSON fields must not silently overwrite capability identities.
	canonical, _ := json.Marshal(r)
	if !bytes.Equal(canonical, data) {
		return bad()
	}
	return r, true, nil
}

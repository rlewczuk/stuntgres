package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

const AuxiliaryForkSnapshotMarker = "stuntgres-auxiliary-forks-v1"
type AuxiliaryForkSnapshot struct {
	Schema, Relation string
	Forks AuxiliaryForks
}
func (r AuxiliaryForkSnapshot) Validate() error {
	if r.Schema == "" || r.Relation == "" || !r.Forks.Valid() || r.Forks == (AuxiliaryForks{}) { return fmt.Errorf("stuntgres: invalid auxiliary fork snapshot") }
	return nil
}
func CaptureAuxiliaryForkSnapshots(ctx context.Context, read catalog.Read, relations map[ids.ObjectAddress]*Relation) ([]AuxiliaryForkSnapshot, error) {
	var records []AuxiliaryForkSnapshot
	for address, store := range relations {
		if err := ctx.Err(); err != nil { return nil, err }
		if store == nil || store.Auxiliary == (AuxiliaryForks{}) { continue }
		relation, ok := read.Relation(address)
		if !ok { continue }
		ns, ok := read.Namespace(relation.Namespace())
		if !ok { return nil, ids.ErrCatalogReplaced }
		if ns.Temporary != (ids.SessionToken{}) { continue }
		record := AuxiliaryForkSnapshot{Schema: ns.Name(), Relation: relation.Name(), Forks: store.Auxiliary}
		if err := record.Validate(); err != nil { return nil, err }
		records = append(records, record)
	}
	slices.SortFunc(records, func(a, b AuxiliaryForkSnapshot) int { if n := strings.Compare(a.Schema, b.Schema); n != 0 { return n }; return strings.Compare(a.Relation, b.Relation) })
	return records, ctx.Err()
}
func RestoreAuxiliaryForkSnapshots(ctx context.Context, read catalog.Read, records []AuxiliaryForkSnapshot) (map[ids.ObjectAddress]AuxiliaryForks, error) {
	var result map[ids.ObjectAddress]AuxiliaryForks
	for _, record := range records {
		if err := ctx.Err(); err != nil { return nil, err }
		if err := record.Validate(); err != nil { return nil, err }
		ns := read.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: record.Schema})
		relation, ok := read.Relation(read.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: record.Relation}))
		if !ok || relation.RelationKind != catalog.TableRelation && relation.RelationKind != catalog.MaterializedViewRelation { return nil, fmt.Errorf("stuntgres: invalid auxiliary fork relation") }
		if read.MustNamespace(ns).Temporary != (ids.SessionToken{}) { return nil, fmt.Errorf("stuntgres: temporary auxiliary fork snapshot") }
		if _, duplicate := result[relation.Address()]; duplicate { return nil, fmt.Errorf("stuntgres: duplicate auxiliary fork snapshot") }
		if result == nil { result = make(map[ids.ObjectAddress]AuxiliaryForks, len(records)) }
		result[relation.Address()] = record.Forks
	}
	return result, ctx.Err()
}
func EncodeAuxiliaryForkSnapshot(r AuxiliaryForkSnapshot) (string, error) {
	if err := r.Validate(); err != nil { return "", err }
	data, err := json.Marshal(r)
	if err != nil { return "", err }
	return "-- "+AuxiliaryForkSnapshotMarker+" "+base64.StdEncoding.EncodeToString(data)+"\n", nil
}
func DecodeAuxiliaryForkSnapshot(comment string) (AuxiliaryForkSnapshot, bool, error) {
	var record AuxiliaryForkSnapshot
	text := strings.TrimSpace(comment)
	if !strings.HasPrefix(text, "stuntgres-auxiliary-forks-") { return record, false, nil }
	invalid := func() (AuxiliaryForkSnapshot, bool, error) { return record, true, fmt.Errorf("stuntgres: invalid auxiliary fork snapshot") }
	marker, payload, ok := strings.Cut(text, " ")
	if !ok || marker != AuxiliaryForkSnapshotMarker { return invalid() }
	data, err := base64.StdEncoding.Strict().DecodeString(payload)
	if err != nil { return invalid() }
	if err := validateSnapshotJSON(json.NewDecoder(bytes.NewReader(data))); err != nil { return invalid() }
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil { return invalid() }
	if err := decoder.Decode(new(any)); err != io.EOF { return invalid() }
	if err := record.Validate(); err != nil { return invalid() }
	return record, true, nil
}

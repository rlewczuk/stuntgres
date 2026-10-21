package engine

import (
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

// Restore hook entries owned by the type, extension, view, partition and
// routine features. Each calls the feature's existing method.

// decodeRestoreDomainReplay collects a domain replay record and the script
// offset where its comment ends, for pairing with the following INSERT.
func decodeRestoreDomainReplay(r *restoreRun, comment string, position parser.Position) (bool, error) {
	record, recognized, err := typedef.DecodeDomainReplay(comment)
	if err != nil || !recognized {
		return false, err
	}
	r.domainReplays = append(r.domainReplays, typedef.PositionedDomainReplay{Record: record, End: position.Offset + len(comment)})
	return true, nil
}

// decodeRestoreExtensionOwners collects an extension owner record.
func decodeRestoreExtensionOwners(r *restoreRun, comment string, _ parser.Position) (bool, error) {
	record, recognized, err := extension.DecodeOwners(comment)
	if err != nil || !recognized {
		return false, err
	}
	r.extensionOwners = append(r.extensionOwners, record)
	return true, nil
}

// decodeRestoreMaterializedSnapshot collects a materialized view snapshot.
func decodeRestoreMaterializedSnapshot(r *restoreRun, comment string, _ parser.Position) (bool, error) {
	record, recognized, err := decodeMaterializedSnapshot(comment)
	if err != nil || !recognized {
		return false, err
	}
	r.snapshots = append(r.snapshots, record)
	return true, nil
}

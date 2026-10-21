package control

import (
	"context"
	"encoding/hex"
	"fmt"
	"sort"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

type largeObjectDump struct {
	object *catalog.LargeObject
	owner  string
	acl    string
}

// Render every owner/ACL before writer I/O, using the existing role-aware
// ACL codec. The payload remains the captured immutable sparse value.
func prepareLargeObjectDump(ctx context.Context, c *Capture) ([]largeObjectDump, error) {
	if len(c.LargeObjects) == 0 {
		return nil, nil
	}
	array, ok := c.OriginalCatalog.LookupType(sqltype.ACLItemArray)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing ACL array type")
	}
	out := make([]largeObjectDump, 0, len(c.LargeObjects))
	for _, d := range c.LargeObjects {
		owner, err := c.OriginalRoles.Descriptor(d.Owner)
		if err != nil {
			return nil, err
		}
		value, err := pgcatalog.ClassACL(c.OriginalCatalog.Owner(), c.OriginalRoles, array, d.ACL)
		if err != nil {
			return nil, err
		}
		literal := "NULL"
		if !value.IsNull() {
			text, err := array.OutputWithEnvironment(ctx, value, sqltype.OutputEnvironment{ACLItem: c.ACLOutput})
			if err != nil {
				return nil, err
			}
			literal = dump.CommentLiteral(text)
		}
		out = append(out, largeObjectDump{d, owner.Name, literal + "::pg_catalog.aclitem[]"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].object.Address().ObjectID < out[j].object.Address().ObjectID })
	return out, nil
}

func emitDumpLargeObjects(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	for _, record := range s.largeObjectRecords {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.forceOwner(record.owner); err != nil {
			return err
		}
		oid := record.object.Address().ObjectID
		if err := w.emit(fmt.Sprintf("SELECT pg_catalog.lo_create(%d::pg_catalog.oid);\n", oid)); err != nil {
			return err
		}
		type chunk struct {
			offset int64
			data   []byte
		}
		var chunks []chunk
		record.object.Content.Chunks(func(offset int64, data []byte) bool {
			chunks = append(chunks, chunk{offset, data})
			return true
		})
		sort.Slice(chunks, func(i, j int) bool { return chunks[i].offset < chunks[j].offset })
		for _, part := range chunks {
			q := fmt.Sprintf("SELECT pg_catalog.lo_put(%d::pg_catalog.oid,%d::pg_catalog.int8,pg_catalog.decode('%s','hex'));\n", oid, part.offset, hex.EncodeToString(part.data))
			if err := w.emit(q); err != nil {
				return err
			}
		}
		// Public PostgreSQL catalog replay runs as administrative authority;
		// native Restore recognizes only this bounded generated repair.
		if err := w.forceOwner("postgres"); err != nil {
			return err
		}
		if err := w.emit(fmt.Sprintf("UPDATE pg_catalog.pg_largeobject_metadata SET lomacl=%s WHERE oid=%d::pg_catalog.oid;\n", record.acl, oid)); err != nil {
			return err
		}
	}
	return nil
}

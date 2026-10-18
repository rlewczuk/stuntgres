package catalog

import (
	"stuntgres/internal/sqltype"
)

type IndexMethod uint8

const (
	BtreeMethod IndexMethod = iota
	HashMethod
	GistMethod
)

func (m IndexMethod) String() string {
	if m == GistMethod {
		return "gist"
	}
	if m == HashMethod {
		return "hash"
	}
	return "btree"
}

func (m IndexMethod) DefaultClass(t *sqltype.Descriptor) (sqltype.IndexClass, bool) {
	if m == GistMethod {
		return sqltype.DefaultGiSTIndexClass(t)
	}
	if m == HashMethod {
		return sqltype.DefaultHashIndexClass(t)
	}
	return sqltype.DefaultIndexClass(t)
}

func (m IndexMethod) DefaultClassID(id sqltype.ID) (sqltype.IndexClass, bool) {
	if m == GistMethod {
		return sqltype.DefaultGiSTIndexClassID(id)
	}
	if m == HashMethod {
		return sqltype.DefaultHashIndexClassID(id)
	}
	return sqltype.DefaultIndexClassID(id)
}

func (m IndexMethod) LookupClass(name string) (sqltype.IndexClass, bool) {
	if m == GistMethod {
		return sqltype.LookupGiSTIndexClass(name)
	}
	if m == HashMethod {
		return sqltype.LookupHashIndexClass(name)
	}
	return sqltype.LookupIndexClass(name)
}

package catalog

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

package catalog

type IndexMethod uint8

const (
	BtreeMethod IndexMethod = iota
	HashMethod
	GistMethod
	BrinMethod
	SpgistMethod
)

func (m IndexMethod) String() string {
	if m == SpgistMethod {
		return "spgist"
	}
	if m == BrinMethod {
		return "brin"
	}
	if m == GistMethod {
		return "gist"
	}
	if m == HashMethod {
		return "hash"
	}
	return "btree"
}

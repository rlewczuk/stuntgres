package pgcatalog

// Default and pretty forms are identical. Fragments retain genuine routine
// identity and identifier spelling under search-path and quoting changes.
var shmemAllocationViewParts = []builtinViewPart{
	viewText(" SELECT "), viewIdentifier("name"), viewText(",\n"),
	viewText("    "), viewIdentifier("off"), viewText(",\n"),
	viewText("    "), viewIdentifier("size"), viewText(",\n"),
	viewText("    "), viewIdentifier("allocated_size"), viewText("\n"),
	viewText("   FROM "), viewRoutine("pg_get_shmem_allocations", ""), viewText("() "), viewIdentifier("pg_get_shmem_allocations"), viewText("("), viewIdentifier("name"), viewText(", "), viewIdentifier("off"), viewText(", "), viewIdentifier("size"), viewText(", "), viewIdentifier("allocated_size"), viewText(");"),
}

var shmemAllocationNUMAViewParts = []builtinViewPart{
	viewText(" SELECT "), viewIdentifier("name"), viewText(",\n"),
	viewText("    "), viewIdentifier("numa_node"), viewText(",\n"),
	viewText("    "), viewIdentifier("size"), viewText("\n"),
	viewText("   FROM "), viewRoutine("pg_get_shmem_allocations_numa", ""), viewText("() "), viewIdentifier("pg_get_shmem_allocations_numa"), viewText("("), viewIdentifier("name"), viewText(", "), viewIdentifier("numa_node"), viewText(", "), viewIdentifier("size"), viewText(");"),
}

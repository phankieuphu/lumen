package schema_test

import (
	"sort"
	"strings"
)

func join(cols []string) string { return strings.Join(cols, ",") }

func sortEdges[E any](es []E, key func(E) string) []E {
	sort.Slice(es, func(i, j int) bool { return key(es[i]) < key(es[j]) })
	return es
}

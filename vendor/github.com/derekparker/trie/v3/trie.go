// Implementation of an R-Way Trie data structure.
//
// A Trie has a root Node which is the base of the tree.
// Each subsequent Node has a letter and children, which are
// nodes that have letter values associated with them.
package trie

import (
	"slices"
	"sort"
	"sync"
)

type node[T any] struct {
	mask     uint64
	parent   *node[T]
	children map[rune]*node[T]
	meta     T
	path     *string // pointer to avoid storing empty strings

	val       rune
	depth     int32
	termCount int32
}

// Trie is a data structure that stores a set of strings.
type Trie[T any] struct {
	mu   sync.RWMutex
	root *node[T]
	size int
}

type ByKeys []string

func (a ByKeys) Len() int           { return len(a) }
func (a ByKeys) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByKeys) Less(i, j int) bool { return len(a[i]) < len(a[j]) }

const nul = 0x0

// Pool for reusing node slices in collection operations
var nodeSlicePool = sync.Pool{
	New: func() interface{} {
		return make([]*node[any], 0, 64)
	},
}

// Pool for reusing string slices in collection operations
var stringSlicePool = sync.Pool{
	New: func() interface{} {
		return make([]string, 0, 64)
	},
}

// Pool for FuzzySearch potentialSubtree slices
var potentialSubtreePool = sync.Pool{
	New: func() interface{} {
		return make([]potentialSubtree[any], 0, 128)
	},
}

// New creates a new Trie with an initialized root Node.
func New[T any]() *Trie[T] {
	return &Trie[T]{
		root: &node[T]{depth: 0}, // Lazy init children map
		size: 0,
	}
}

// Add adds the key to the Trie, including meta data.
func (t *Trie[T]) Add(key string, meta T) *node[T] {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.size++
	runes := []rune(key)
	bitmask := maskruneslice(runes)
	nd := t.root
	nd.mask |= bitmask
	nd.termCount++
	for i := range runes {
		r := runes[i]
		bitmask = maskruneslice(runes[i:])
		if nd.children != nil && len(nd.children) > 0 {
			if n, ok := nd.children[r]; ok {
				nd = n
				nd.mask |= bitmask
			} else {
				nd = nd.newEmptyChild(r, bitmask)
			}
		} else {
			nd = nd.newEmptyChild(r, bitmask)
		}
		nd.termCount++
	}
	nd = nd.newChild(nul, 0, meta, key)

	return nd
}

// Find finds and returns meta data associated
// with `key`.
func (t *Trie[T]) Find(key string) (*node[T], bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	nd := findNode(t.root, []rune(key))
	if nd == nil {
		return nil, false
	}

	if nd.children == nil {
		return nil, false
	}
	nd, ok := nd.children[nul]
	if !ok || nd.path == nil {
		return nil, false
	}

	return nd, true
}

func (t *Trie[T]) HasKeysWithPrefix(key string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	nd := findNode(t.root, []rune(key))
	return nd != nil
}

// Remove removes a key from the trie, ensuring that
// all bitmasks up to root are appropriately recalculated.
func (t *Trie[T]) Remove(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	var (
		rs = []rune(key)
		nd = findNode(t.root, []rune(key))
	)

	if nd == nil {
		return
	}

	t.size--
	for n := nd.parent; n != nil; n = n.parent {
		if n == t.root {
			t.root = &node[T]{} // Lazy init children map
			break
		}

		if n.children != nil && len(n.children) > 1 {
			n.removeChild(rs[n.depth])
			break
		}
	}
}

// Keys returns all the keys currently stored in the trie.
func (t *Trie[T]) Keys() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.size == 0 {
		return []string{}
	}

	return t.PrefixSearch("")
}

// FuzzySearch performs a fuzzy search against the keys in the trie.
func (t *Trie[T]) FuzzySearch(pre string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	keys := fuzzycollect(t.root, []rune(pre))
	sort.Sort(ByKeys(keys))
	return keys
}

// PrefixSearch performs a prefix search against the keys in the trie.
func (t *Trie[T]) PrefixSearch(pre string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	nd := findNode(t.root, []rune(pre))
	if nd == nil {
		return nil
	}

	return collect(nd)
}

// newChild creates and returns a pointer to a new child for the node.
func (n *node[T]) newChild(val rune, bitmask uint64, meta T, pathStr ...string) *node[T] {
	node := &node[T]{
		val:    val,
		mask:   bitmask,
		meta:   meta,
		parent: n,
		depth:  n.depth + 1,
	}
	// Store path for terminal nodes
	if len(pathStr) > 0 {
		node.path = &pathStr[0]
	}
	n.ensureChildren()
	n.children[node.val] = node
	n.mask |= bitmask
	return node
}

// newEmptyChild creates and returns a pointer to a new child for the node.
func (n *node[T]) newEmptyChild(val rune, bitmask uint64) *node[T] {
	node := &node[T]{
		val:    val,
		mask:   bitmask,
		parent: n,
		depth:  n.depth + 1,
	}
	n.ensureChildren()
	n.children[node.val] = node
	n.mask |= bitmask
	return node
}

func (n *node[T]) removeChild(r rune) {
	delete(n.children, r)
	for nd := n.parent; nd != nil; nd = nd.parent {
		nd.mask ^= nd.mask
		nd.mask |= uint64(1) << uint64(nd.val-'a')
		if nd.children != nil {
			for _, c := range nd.children {
				nd.mask |= c.mask
			}
		}
	}
}

// Val returns the value of the node.
func (n *node[T]) Val() T {
	return n.meta
}

// ensureChildren lazily initializes the children map if needed
func (n *node[T]) ensureChildren() {
	if n.children == nil {
		n.children = make(map[rune]*node[T])
	}
}

// reconstructPath builds the full path from root to this node
func (n *node[T]) reconstructPath() string {
	if n.parent == nil {
		return ""
	}
	if n.val == nul {
		// Terminal node - return parent's path
		return n.parent.reconstructPath()
	}
	return n.parent.reconstructPath() + string(n.val)
}

func findNode[T any](nd *node[T], runes []rune) *node[T] {
	if nd == nil {
		return nil
	}

	if len(runes) == 0 {
		return nd
	}

	if nd.children == nil {
		return nil
	}
	n, ok := nd.children[runes[0]]
	if !ok {
		return nil
	}

	var nrunes []rune
	if len(runes) > 1 {
		nrunes = runes[1:]
	} else {
		nrunes = runes[0:0]
	}

	return findNode(n, nrunes)
}

// maskruneslice creates a bitmask for the given runes.
// Optimized to eliminate bounds checking and enable vectorization.
//
//go:inline
func maskruneslice(rs []rune) uint64 {
	// Use 4 accumulators for better instruction-level parallelism
	var m0, m1, m2, m3 uint64

	// Process 4 elements at a time using slice patterns for BCE
	for len(rs) >= 4 {
		// Compiler knows rs[:4] is safe when len(rs) >= 4
		// This pattern eliminates all bounds checks
		r := rs[:4:4] // Full slice expression prevents capacity growth

		// No bounds checks on these accesses
		m0 |= uint64(1) << uint64(r[0]-'a')
		m1 |= uint64(1) << uint64(r[1]-'a')
		m2 |= uint64(1) << uint64(r[2]-'a')
		m3 |= uint64(1) << uint64(r[3]-'a')

		rs = rs[4:]
	}

	// Handle remaining elements (0-3)
	// Process remaining with explicit length checks for BCE
	switch len(rs) {
	case 3:
		m0 |= uint64(1) << uint64(rs[0]-'a')
		m1 |= uint64(1) << uint64(rs[1]-'a')
		m2 |= uint64(1) << uint64(rs[2]-'a')
	case 2:
		m0 |= uint64(1) << uint64(rs[0]-'a')
		m1 |= uint64(1) << uint64(rs[1]-'a')
	case 1:
		m0 |= uint64(1) << uint64(rs[0]-'a')
	}

	// Combine all accumulators
	return m0 | m1 | m2 | m3
}

func collect[T any](nd *node[T]) []string {
	keys := make([]string, 0, nd.termCount)
	childrenCount := 0
	if nd.children != nil {
		childrenCount = len(nd.children)
	}
	nodes := make([]*node[T], 1, childrenCount+1)
	nodes[0] = nd
	for len(nodes) > 0 {
		i := len(nodes) - 1
		n := nodes[i]
		nodes = nodes[:i]
		if n.children != nil {
			for _, c := range n.children {
				nodes = append(nodes, c)
			}
		}
		if n.path != nil {
			keys = append(keys, *n.path)
		}
	}
	return keys
}

type potentialSubtree[T any] struct {
	idx  int
	node *node[T]
}

func fuzzycollect[T any](nd *node[T], partial []rune) []string {
	if len(partial) == 0 {
		return collect(nd)
	}

	// Get pooled slices to minimize allocations
	keys := stringSlicePool.Get().([]string)
	keys = keys[:0] // Reset length but keep capacity
	defer stringSlicePool.Put(keys)

	// Use a cast to work around generic pool limitations
	potentialRaw := potentialSubtreePool.Get().([]potentialSubtree[any])
	potential := make([]potentialSubtree[T], 1, cap(potentialRaw))
	potential[0] = potentialSubtree[T]{node: nd, idx: 0}
	defer func() {
		// Clear and return the raw slice to pool
		potentialRaw = potentialRaw[:0]
		potentialSubtreePool.Put(potentialRaw)
	}()

	for len(potential) > 0 {
		i := len(potential) - 1
		p := potential[i]
		potential = potential[:i]

		m := maskruneslice(partial[p.idx:])
		if (p.node.mask & m) != m {
			continue
		}

		if p.node.val == partial[p.idx] {
			p.idx++
			if p.idx == len(partial) {
				// Instead of calling collect(), do direct terminal collection
				collectTerminalsDirectly(p.node, &keys)
				continue
			}
		}

		if p.node.children != nil {
			for _, c := range p.node.children {
				potential = append(potential, potentialSubtree[T]{node: c, idx: p.idx})
			}
		}
	}

	// Copy result to return since keys slice is from pool
	return slices.Clone(keys)
}

// collectTerminalsDirectly collects terminal paths without allocating intermediate slices
func collectTerminalsDirectly[T any](nd *node[T], keys *[]string) {
	// Use stack-based traversal with pre-allocated node slice
	nodes := make([]*node[T], 1, 32)
	nodes[0] = nd

	for len(nodes) > 0 {
		i := len(nodes) - 1
		n := nodes[i]
		nodes = nodes[:i]

		if n.children != nil {
			for _, c := range n.children {
				nodes = append(nodes, c)
			}
		}

		if n.path != nil {
			*keys = append(*keys, *n.path)
		}
	}
}

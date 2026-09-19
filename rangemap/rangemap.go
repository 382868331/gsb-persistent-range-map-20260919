// Package rangemap implements an immutable mapping from int64 half-open
// intervals [lo, hi) to strings, backed by a persistent AVL tree with
// structural sharing. Every mutating operation returns a new version; old
// versions stay readable.
//
// Coverage is explicit: a coordinate is either uncovered or covered with a
// string value. The empty string is a legal value and is distinct from
// "uncovered".
//
// Endpoints may be math.MinInt64 and math.MaxInt64. The point math.MaxInt64
// itself can never be covered (there is no hi+1 conversion anywhere).
package rangemap

import "errors"

// MaxSegments bounds the number of normalized segments in one version.
const MaxSegments = 10000

// Errors returned by mutating operations. They never modify the receiver.
var (
	// ErrInvalidRange is returned when lo >= hi.
	ErrInvalidRange = errors.New("rangemap: invalid range, require lo < hi")
	// ErrTooManySegments is returned when an operation would produce more
	// than MaxSegments segments.
	ErrTooManySegments = errors.New("rangemap: too many segments (limit 10000)")
)

// Segment is a covered half-open interval [Lo, Hi) carrying Value.
type Segment struct {
	Lo    int64
	Hi    int64
	Value string
}

// Change describes one maximal coordinate run in a Diff result inside which
// the two versions differ in coverage or value. Rebuilding the new version
// from the old one is possible from the change list alone (Set when
// NewCovered, otherwise Erase).
type Change struct {
	Segment
	OldCovered bool
	OldValue   string
	NewCovered bool
	NewValue   string
}

// Stats records node traffic of one operation: nodes freshly allocated
// (Created) and distinct pre-existing nodes visited (Visited).
type Stats struct {
	Created int64
	Visited int64
}

// StatsHolder is implemented by every published version.
type StatsHolder interface {
	RangeMapStats() Stats
}

// nodeID identifies an immutable node across versions. 0 means nil. Nodes
// shared between versions keep the same id, so id equality means identical
// subtree content.
type nodeID = uint64

var nextID nodeID = 1

func allocID() nodeID {
	id := nextID
	nextID++
	return id
}

// node is one maximal normalized covered segment. Only covered intervals
// are stored; an uncovered hole is simply the absence of any node. Stored
// intervals are pairwise disjoint, and in-order adjacent nodes never carry
// the same value (they would have been merged).
type node struct {
	lo, hi int64
	val    string

	left, right *node
	height      int
	cnt         int64
	// hiMax is the largest hi in this subtree (augmentation used by
	// interval searches to prune whole subtrees).
	hiMax int64

	id nodeID
}

func heightOf(n *node) int {
	if n == nil {
		return 0
	}
	return n.height
}

func countOf(n *node) int64 {
	if n == nil {
		return 0
	}
	return n.cnt
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minI64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// opCtx carries per-operation counters and the de-duplication set used to
// count distinct visited nodes.
type opCtx struct {
	created int64
	visited int64
	seen    map[nodeID]struct{}
}

func newCtx() *opCtx {
	return &opCtx{seen: make(map[nodeID]struct{})}
}

func (c *opCtx) mark(n *node) {
	if n == nil {
		return
	}
	if _, ok := c.seen[n.id]; ok {
		return
	}
	c.seen[n.id] = struct{}{}
	c.visited++
}

func (c *opCtx) stats() Stats {
	return Stats{Created: c.created, Visited: c.visited}
}

// mkNode allocates a fresh immutable node joining two persistent subtrees.
func (c *opCtx) mkNode(lo, hi int64, val string, l, r *node) *node {
	c.created++
	hiMax := hi
	if l != nil && l.hiMax > hiMax {
		hiMax = l.hiMax
	}
	if r != nil && r.hiMax > hiMax {
		hiMax = r.hiMax
	}
	return &node{
		lo:     lo,
		hi:     hi,
		val:    val,
		left:   l,
		right:  r,
		height: 1 + maxInt(heightOf(l), heightOf(r)),
		cnt:    1 + countOf(l) + countOf(r),
		hiMax:  hiMax,
		id:     allocID(),
	}
}

// balance rebalances one AVL node; rotations allocate 1-3 new nodes while
// sharing all inputs with older versions.
func (c *opCtx) balance(lo, hi int64, val string, l, r *node) *node {
	dl := heightOf(l) - heightOf(r)
	switch {
	case dl > 1: // l != nil
		if heightOf(l.left) >= heightOf(l.right) {
			nr := c.mkNode(lo, hi, val, l.right, r)
			return c.mkNode(l.lo, l.hi, l.val, l.left, nr)
		}
		lr := l.right
		nl := c.mkNode(l.lo, l.hi, l.val, l.left, lr.left)
		nr := c.mkNode(lo, hi, val, lr.right, r)
		return c.mkNode(lr.lo, lr.hi, lr.val, nl, nr)
	case dl < -1: // r != nil
		if heightOf(r.right) >= heightOf(r.left) {
			nl := c.mkNode(lo, hi, val, l, r.left)
			return c.mkNode(r.lo, r.hi, r.val, nl, r.right)
		}
		rl := r.left
		nl := c.mkNode(lo, hi, val, l, rl.left)
		nr := c.mkNode(r.lo, r.hi, r.val, rl.right, r.right)
		return c.mkNode(rl.lo, rl.hi, rl.val, nl, nr)
	default:
		return c.mkNode(lo, hi, val, l, r)
	}
}

// join3 returns the union of tree a, one pivot segment and tree b, where
// every key in a is below pivot.lo and every key in b above it. It is the
// standard height-based AVL join and works (descending along the taller
// spine) even when one side is nil.
func (c *opCtx) join3(a *node, lo, hi int64, val string, b *node) *node {
	if heightOf(a) > heightOf(b)+1 {
		c.mark(a)
		return c.balance(a.lo, a.hi, a.val, a.left, c.join3(a.right, lo, hi, val, b))
	}
	if heightOf(b) > heightOf(a)+1 {
		c.mark(b)
		return c.balance(b.lo, b.hi, b.val, c.join3(a, lo, hi, val, b.left), b.right)
	}
	return c.mkNode(lo, hi, val, a, b)
}

// join2 concatenates two trees with ordered, disjoint key sets.
func (c *opCtx) join2(a, b *node) *node {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if heightOf(a) > heightOf(b)+1 {
		c.mark(a)
		return c.balance(a.lo, a.hi, a.val, a.left, c.join2(a.right, b))
	}
	if heightOf(b) > heightOf(a)+1 {
		c.mark(b)
		return c.balance(b.lo, b.hi, b.val, c.join2(a, b.left), b.right)
	}
	p := leftmost(b)
	c.mark(p)
	return c.balance(p.lo, p.hi, p.val, a, c.eraseMin(b))
}

// split partitions n into (keys < key, keys >= key); results share
// untouched structure with the input.
func (c *opCtx) split(n *node, key int64) (*node, *node) {
	if n == nil {
		return nil, nil
	}
	c.mark(n)
	if key <= n.lo {
		l, r := c.split(n.left, key)
		return l, c.join3(r, n.lo, n.hi, n.val, n.right)
	}
	l, r := c.split(n.right, key)
	return c.join3(n.left, n.lo, n.hi, n.val, l), r
}

func (c *opCtx) eraseMin(n *node) *node {
	if n == nil {
		return nil
	}
	c.mark(n)
	if n.left == nil {
		return n.right
	}
	return c.balance(n.lo, n.hi, n.val, c.eraseMin(n.left), n.right)
}

func (c *opCtx) eraseKey(n *node, key int64) *node {
	if n == nil {
		return nil
	}
	c.mark(n)
	if key < n.lo {
		return c.balance(n.lo, n.hi, n.val, c.eraseKey(n.left, key), n.right)
	}
	if key > n.lo {
		return c.balance(n.lo, n.hi, n.val, n.left, c.eraseKey(n.right, key))
	}
	return c.join2(n.left, n.right)
}

func leftmost(n *node) *node {
	for n != nil && n.left != nil {
		n = n.left
	}
	return n
}

func rightmost(n *node) *node {
	for n != nil && n.right != nil {
		n = n.right
	}
	return n
}

// pointNode returns the node whose interval contains p, or nil.
func pointNode(n *node, p int64) *node {
	for n != nil {
		if p < n.lo {
			n = n.left
		} else if p >= n.hi {
			n = n.right
		} else {
			return n
		}
	}
	return nil
}

// visitOverlap invokes onNode on exactly the nodes intersecting [lo, hi)
// plus boundary-spine nodes, pruning with the hiMax augmentation.
func (c *opCtx) visitOverlap(n *node, lo, hi int64, onNode func(*node)) {
	if n == nil {
		return
	}
	c.mark(n)
	if n.left != nil && n.left.hiMax > lo {
		c.visitOverlap(n.left, lo, hi, onNode)
	}
	if n.lo < hi && n.hi > lo {
		onNode(n)
	}
	if n.right != nil && n.lo < hi && n.right.hiMax > lo {
		c.visitOverlap(n.right, lo, hi, onNode)
	}
}

// Map is one immutable version. A nil *Map acts as the fully uncovered map.
type Map struct {
	root  *node
	stats Stats // traffic of the operation that produced this version
}

// Compile-time check that published versions expose stats.
var _ StatsHolder = (*Map)(nil)

// New returns a fully uncovered version.
func New() *Map {
	return &Map{}
}

func rootOf(m *Map) *node {
	if m == nil {
		return nil
	}
	return m.root
}

// RangeMapStats reports node traffic of the operation creating this version.
func (m *Map) RangeMapStats() Stats {
	if m == nil {
		return Stats{}
	}
	return m.stats
}

// Len returns the number of normalized covered segments.
func (m *Map) Len() int {
	return int(countOf(rootOf(m)))
}

func validateRange(lo, hi int64) error {
	if lo >= hi {
		return ErrInvalidRange
	}
	return nil
}

// piece is a transient normalized fragment while rebuilding a range.
type piece struct {
	lo, hi int64
	val    string
}

// applyRange replaces [lo, hi) by coverage val when set, or by a hole when
// unset, returning a new version. The old version is never modified.
func (m *Map) applyRange(lo, hi int64, set bool, val string) (*Map, Stats, error) {
	if err := validateRange(lo, hi); err != nil {
		return m, Stats{}, err
	}
	root := rootOf(m)
	c := newCtx()

	// Phase 1: count intersecting segments k (also warms visited stats).
	k := 0
	c.visitOverlap(root, lo, hi, func(*node) { k++ })
	if k == 0 && !set {
		// Erasing coordinates that are already uncovered is a no-op.
		return m, c.stats(), nil
	}

	// Phase 2: isolate keys in [lo, hi) with two persistent splits.
	a, m0 := c.split(root, lo)
	mid, b := c.split(m0, hi)

	var left, right []piece
	a2 := a

	// L is the unique node on the left that can touch or cross lo.
	L := rightmost(a)
	spans := false
	switch {
	case L != nil && L.hi > lo:
		// L overlaps the edit range; cut it off a and keep its left
		// survivor. If L also crosses hi it spans the whole range, and
		// its right survivor is kept too.
		a2 = c.eraseKey(a, L.lo)
		left = append(left, piece{L.lo, lo, L.val})
		if L.hi > hi {
			right = append(right, piece{hi, L.hi, L.val})
			spans = true
		}
	case set && L != nil && L.hi == lo && L.val == val:
		// L merely touches lo with the same value: include it so the
		// merge pass joins it with the fresh piece.
		a2 = c.eraseKey(a, L.lo)
		left = append(left, piece{L.lo, lo, L.val})
	}

	if set {
		left = append(left, piece{lo, hi, val})
	}

	// A node inside mid may cross hi and survive on the right (unless L
	// already spans the whole range).
	if !spans {
		if X := pointNode(mid, hi); X != nil {
			right = append(right, piece{hi, X.hi, X.val})
		}
	}

	// Assemble in coordinate order, then merge adjacent equal values.
	pieces := append(left, right...)
	merged := pieces[:0]
	for _, p := range pieces {
		if n := len(merged); n > 0 && merged[n-1].hi == p.lo && merged[n-1].val == p.val {
			merged[n-1].hi = p.hi
		} else {
			merged = append(merged, p)
		}
	}
	pieces = merged

	// R is the first node on the right: merge with the final piece when
	// the two touch and carry the same value.
	if R := leftmost(b); R != nil && len(pieces) > 0 {
		last := &pieces[len(pieces)-1]
		if R.lo == last.hi && last.val == R.val {
			last.hi = R.hi
			b = c.eraseMin(b)
		}
	}

	// Phase 3: stitch untouched parts and fresh pieces back together.
	t := a2
	for i := range pieces {
		t = c.join3(t, pieces[i].lo, pieces[i].hi, pieces[i].val, nil)
	}
	t = c.join2(t, b)

	if t != nil && t.cnt > MaxSegments {
		// The half-built tree is discarded; the receiver stays intact.
		return m, c.stats(), ErrTooManySegments
	}
	return &Map{root: t, stats: c.stats()}, c.stats(), nil
}

// Set covers [lo, hi) with value and returns the new version. An empty
// value is legal: it means covered with "", distinct from uncovered.
func (m *Map) Set(lo, hi int64, value string) (*Map, error) {
	nm, _, err := m.applyRange(lo, hi, true, value)
	return nm, err
}

// Erase removes coverage over [lo, hi) and returns the new version.
func (m *Map) Erase(lo, hi int64) (*Map, error) {
	nm, _, err := m.applyRange(lo, hi, false, "")
	return nm, err
}

// Get reports coverage at a single point. value is meaningful only when
// covered. The point math.MaxInt64 is always uncovered.
func (m *Map) Get(p int64) (value string, covered bool) {
	if n := pointNode(rootOf(m), p); n != nil {
		return n.val, true
	}
	return "", false
}

// appendQuery collects covered nodes intersecting [lo, hi), clipped,
// merging adjacent equal-value runs in in-order.
func appendQuery(n *node, lo, hi int64, out []Segment) []Segment {
	if n == nil {
		return out
	}
	if n.left != nil && n.left.hiMax > lo {
		out = appendQuery(n.left, lo, hi, out)
	}
	if n.lo < hi && n.hi > lo {
		slo := maxI64(n.lo, lo)
		shi := minI64(n.hi, hi)
		if k := len(out); k > 0 && out[k-1].Hi == slo && out[k-1].Value == n.val {
			out[k-1].Hi = shi
		} else {
			out = append(out, Segment{slo, shi, n.val})
		}
	}
	if n.right != nil && n.lo < hi && n.right.hiMax > lo {
		out = appendQuery(n.right, lo, hi, out)
	}
	return out
}

// Query returns covered fragments intersecting [lo, hi), clipped to the
// query range; adjacent equal-value fragments are merged. Holes are omitted.
func (m *Map) Query(lo, hi int64) ([]Segment, error) {
	if err := validateRange(lo, hi); err != nil {
		return nil, err
	}
	return appendQuery(rootOf(m), lo, hi, nil), nil
}

// Segments returns all normalized covered segments of the version.
func (m *Map) Segments() []Segment {
	var out []Segment
	var walk func(*node)
	walk = func(n *node) {
		if n == nil {
			return
		}
		walk(n.left)
		if k := len(out); k > 0 && out[k-1].Hi == n.lo && out[k-1].Value == n.val {
			out[k-1].Hi = n.hi
		} else {
			out = append(out, Segment{n.lo, n.hi, n.val})
		}
		walk(n.right)
	}
	walk(rootOf(m))
	return out
}

// ---- Diff ---------------------------------------------------------------

type cellKind struct {
	present bool
	val     string
}

var uncoveredKind = cellKind{}

// rangeIter is an in-order iterator over nodes that can intersect a query
// range. Its stack is the standard in-order spine; whole subtrees whose
// hiMax ends at or before qlo are pruned at push time.
type rangeIter struct {
	stack []*node
}

func pushGTE(st *[]*node, n *node, qlo int64) {
	for n != nil {
		if n.hiMax <= qlo {
			n = n.right
		} else {
			*st = append(*st, n)
			n = n.left
		}
	}
}

func newRangeIter(root *node, qlo int64) rangeIter {
	var it rangeIter
	pushGTE(&it.stack, root, qlo)
	return it
}

func (it *rangeIter) peek() *node {
	if len(it.stack) == 0 {
		return nil
	}
	return it.stack[len(it.stack)-1]
}

// advance moves past the top node. When skipRight is true the top node's
// entire right subtree is skipped as well (used when both versions share
// the same immutable subtree).
func (it *rangeIter) advance(skipRight bool, qlo int64) {
	n := it.stack[len(it.stack)-1]
	it.stack = it.stack[:len(it.stack)-1]
	if !skipRight {
		pushGTE(&it.stack, n.right, qlo)
	}
}

// Diff returns maximal coordinate runs inside [lo, hi) where old and new
// differ, ordered by coordinate. Adjacent differing runs are merged only
// when their old (coverage,value) and new (coverage,value) are respectively
// identical. Shared immutable subtrees are detected by node id and skipped
// as whole blocks.
//
// Applying the result to old via Apply reconstructs new within [lo, hi).
func Diff(old, new *Map, lo, hi int64) ([]Change, Stats, error) {
	if err := validateRange(lo, hi); err != nil {
		return nil, Stats{}, err
	}
	c := newCtx()
	ia := newRangeIter(rootOf(old), lo)
	ib := newRangeIter(rootOf(new), lo)

	var changes []Change
	var (
		haveRun        bool
		runLo, runHi   int64
		runOld, runNew cellKind
	)
	flush := func() {
		if !haveRun {
			return
		}
		changes = append(changes, Change{
			Segment:    Segment{runLo, runHi, runNew.val},
			OldCovered: runOld.present,
			OldValue:   runOld.val,
			NewCovered: runNew.present,
			NewValue:   runNew.val,
		})
		haveRun = false
	}

	cur := lo
	for cur < hi {
		// Drain in-order nodes already fully left of cur (they can
		// survive on the spine because pushGTE prunes on hiMax > qlo,
		// not on the moving cursor).
		for {
			na := ia.peek()
			if na == nil || na.hi > cur {
				break
			}
			c.mark(na)
			ia.advance(false, lo)
		}
		for {
			nb := ib.peek()
			if nb == nil || nb.hi > cur {
				break
			}
			c.mark(nb)
			ib.advance(false, lo)
		}
		a := ia.peek()
		b := ib.peek()
		if a != nil && a.lo >= hi {
			a = nil
		}
		if b != nil && b.lo >= hi {
			b = nil
		}

		// Both versions have the exact same immutable node at the in-order
		// cursor. Climb the two spines while their parent frames are also
		// the same node to find the largest shared subtree, then jump over
		// it in both iterators at once.
		if a != nil && b != nil && a.id == b.id {
			flush()
			n := a
			c.mark(n)
			for len(ia.stack) >= 2 && len(ib.stack) >= 2 &&
				ia.stack[len(ia.stack)-2].id == ib.stack[len(ib.stack)-2].id {
				ia.stack = ia.stack[:len(ia.stack)-1]
				ib.stack = ib.stack[:len(ib.stack)-1]
				n = ia.stack[len(ia.stack)-1]
				c.mark(n)
			}
			end := minI64(n.hiMax, hi)
			ia.advance(true, lo)
			ib.advance(true, lo)
			if end <= cur {
				end = hi // defensive progress
			}
			cur = end
			continue
		}

		if a != nil {
			c.mark(a)
		}
		if b != nil {
			c.mark(b)
		}
		if a == nil && b == nil {
			break
		}

		end := hi
		if a != nil {
			if a.lo > cur {
				end = minI64(end, a.lo)
			} else {
				end = minI64(end, a.hi)
			}
		}
		if b != nil {
			if b.lo > cur {
				end = minI64(end, b.lo)
			} else {
				end = minI64(end, b.hi)
			}
		}

		ka, kb := uncoveredKind, uncoveredKind
		if a != nil && a.lo <= cur {
			ka = cellKind{true, a.val}
		}
		if b != nil && b.lo <= cur {
			kb = cellKind{true, b.val}
		}

		if ka == kb {
			flush()
		} else if haveRun && runHi == cur && runOld == ka && runNew == kb {
			runHi = end
		} else {
			flush()
			runLo, runHi, runOld, runNew = cur, end, ka, kb
			haveRun = true
		}

		if a != nil && a.lo <= cur && a.hi <= end {
			ia.advance(false, lo)
		}
		if b != nil && b.lo <= cur && b.hi <= end {
			ib.advance(false, lo)
		}
		cur = end
	}
	flush()
	return changes, c.stats(), nil
}

// Apply replays Diff changes onto dst (Set for newly covered runs, Erase
// for newly uncovered runs) in coordinate order.
func Apply(dst *Map, changes []Change) (*Map, error) {
	m := dst
	var err error
	for _, ch := range changes {
		if ch.NewCovered {
			m, err = m.Set(ch.Lo, ch.Hi, ch.NewValue)
		} else {
			m, err = m.Erase(ch.Lo, ch.Hi)
		}
		if err != nil {
			return dst, err
		}
	}
	return m, nil
}

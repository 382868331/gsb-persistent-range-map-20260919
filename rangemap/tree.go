package rangemap

// node is one covered half-open segment [lo, hi) with a string value.
//
// The tree is ordered by lo (keys are unique within a version). Persistence
// is achieved by path copying: every mutating recursive step allocates a new
// node instead of modifying an existing one, so old roots stay valid.
//
// Aggregated fields support O(log n) searches and tight range pruning:
//   - height/size: AVL rebalancing and the per-version fragment cap
//   - minLo/maxHi: coordinate bounding box of the whole subtree
//
// No endpoint arithmetic such as hi+1 is ever performed; MaxInt64 is a legal
// hi and the point MaxInt64 itself is simply never inside any segment.
type node struct {
	lo, hi int64
	val    string

	left  *node
	right *node

	height int
	size   int
	minLo  int64
	maxHi  int64
}

// counter records nodes actually allocated (created) and dereferenced
// (visited) while performing an operation.
type counter struct {
	created int64
	visited int64
	skipped int64
}

func (c *counter) touch(n *node) { c.visited++ }
func (c *counter) born()         { c.created++ }

func (c *counter) stats() Stats {
	return Stats{Created: int(c.created), Visited: int(c.visited), Skipped: int(c.skipped)}
}

func zheight(n *node) int {
	if n == nil {
		return 0
	}
	return n.height
}

func zsize(n *node) int {
	if n == nil {
		return 0
	}
	return n.size
}

// newNode allocates a node and computes its aggregated fields.
func newNode(c *counter, lo, hi int64, val string, l, r *node) *node {
	c.born()
	n := &node{lo: lo, hi: hi, val: val, left: l, right: r}
	n.recompute()
	return n
}

// cloneNode allocates a replacement for n with new payload/children.
func cloneNode(c *counter, n *node, lo, hi int64, val string, l, r *node) *node {
	return newNode(c, lo, hi, val, l, r)
}

// recompute derives height, size and the bounding box from the node itself
// and its current children.
func (n *node) recompute() {
	n.height = 1 + max(zheight(n.left), zheight(n.right))
	n.size = 1 + zsize(n.left) + zsize(n.right)

	n.minLo = n.lo
	n.maxHi = n.hi
	if n.left != nil {
		n.minLo = min(n.minLo, n.left.minLo)
		n.maxHi = max(n.maxHi, n.left.maxHi)
	}
	if n.right != nil {
		n.minLo = min(n.minLo, n.right.minLo)
		n.maxHi = max(n.maxHi, n.right.maxHi)
	}
}

// ---- persistent AVL rotations -------------------------------------------

func rotateRight(c *counter, n *node) *node {
	y := n.left
	t2 := y.right
	c.touch(y)
	c.touch(n)
	moved := newNode(c, n.lo, n.hi, n.val, t2, n.right)
	return newNode(c, y.lo, y.hi, y.val, y.left, moved)
}

func rotateLeft(c *counter, n *node) *node {
	y := n.right
	t2 := y.left
	c.touch(y)
	c.touch(n)
	moved := newNode(c, n.lo, n.hi, n.val, n.left, t2)
	return newNode(c, y.lo, y.hi, y.val, moved, y.right)
}

func balance(c *counter, n *node) *node {
	bf := zheight(n.left) - zheight(n.right)
	if bf > 1 {
		l := n.left
		c.touch(l)
		if zheight(l.left) >= zheight(l.right) {
			return rotateRight(c, n)
		}
		reb := newNode(c, n.lo, n.hi, n.val, rotateLeft(c, l), n.right)
		return rotateRight(c, reb)
	}
	if bf < -1 {
		r := n.right
		c.touch(r)
		if zheight(r.right) >= zheight(r.left) {
			return rotateLeft(c, n)
		}
		reb := newNode(c, n.lo, n.hi, n.val, n.left, rotateRight(c, r))
		return rotateLeft(c, reb)
	}
	return n
}

// ---- persistent BST/AVL primitives, keyed by lo --------------------------

func insert(c *counter, n *node, lo, hi int64, val string) *node {
	if n == nil {
		return newNode(c, lo, hi, val, nil, nil)
	}
	c.touch(n)
	switch {
	case lo < n.lo:
		n = cloneNode(c, n, n.lo, n.hi, n.val, insert(c, n.left, lo, hi, val), n.right)
	case lo > n.lo:
		n = cloneNode(c, n, n.lo, n.hi, n.val, n.left, insert(c, n.right, lo, hi, val))
	default:
		panic("rangemap: internal error: duplicate segment key on insert")
	}
	return balance(c, n)
}

func minNode(c *counter, n *node) *node {
	for n.left != nil {
		c.touch(n)
		n = n.left
	}
	c.touch(n)
	return n
}

func removeMin(c *counter, n *node) *node {
	if n.left == nil {
		return n.right
	}
	c.touch(n)
	r := newNode(c, n.lo, n.hi, n.val, removeMin(c, n.left), n.right)
	return balance(c, r)
}

func eraseKey(c *counter, n *node, key int64) *node {
	if n == nil {
		return nil
	}
	c.touch(n)
	switch {
	case key < n.lo:
		n = cloneNode(c, n, n.lo, n.hi, n.val, eraseKey(c, n.left, key), n.right)
	case key > n.lo:
		n = cloneNode(c, n, n.lo, n.hi, n.val, n.left, eraseKey(c, n.right, key))
	default:
		if n.left == nil {
			return n.right
		}
		if n.right == nil {
			return n.left
		}
		s := minNode(c, n.right)
		r := newNode(c, s.lo, s.hi, s.val, n.left, removeMin(c, n.right))
		return balance(c, r)
	}
	return balance(c, n)
}

// ---- searches -------------------------------------------------------------

// coverAt returns the segment covering point x, or nil. A point is covered
// iff lo <= x < hi; x == MaxInt64 can therefore never be covered.
func coverAt(c *counter, n *node, x int64) *node {
	for n != nil {
		c.touch(n)
		switch {
		case x < n.lo:
			n = n.left
		case x >= n.hi:
			n = n.right
		default:
			return n
		}
	}
	return nil
}

// findNode looks up the segment whose lo equals key.
func findNode(c *counter, n *node, key int64) *node {
	for n != nil {
		c.touch(n)
		switch {
		case key < n.lo:
			n = n.left
		case key > n.lo:
			n = n.right
		default:
			return n
		}
	}
	return nil
}

// predecessorBefore returns the segment with the greatest lo < key.
func predecessorBefore(c *counter, n *node, key int64) *node {
	var best *node
	for n != nil {
		c.touch(n)
		if n.lo >= key {
			n = n.left
		} else {
			best = n
			n = n.right
		}
	}
	return best
}

// lowerBound returns the segment with the smallest lo >= key.
func lowerBound(c *counter, n *node, key int64) *node {
	var best *node
	for n != nil {
		c.touch(n)
		if n.lo < key {
			n = n.right
		} else {
			best = n
			n = n.left
		}
	}
	return best
}

// endingAt returns the unique segment ending exactly at x ([p.lo, x)), if any.
func endingAt(c *counter, n *node, x int64) *node {
	p := predecessorBefore(c, n, x)
	if p != nil && p.hi == x {
		return p
	}
	return nil
}

// startingAt returns the unique segment starting exactly at x, if any.
func startingAt(c *counter, n *node, x int64) *node {
	s := lowerBound(c, n, x)
	if s != nil && s.lo == x {
		return s
	}
	return nil
}

// ---- in-order range collection with bounding-box pruning ------------------

func subtreeMayIntersect(n *node, qlo, qhi int64) bool {
	return n != nil && n.maxHi > qlo && n.minLo < qhi
}

// collectSegments appends covered fragments intersecting [qlo, qhi), clipped
// to that query window, in ascending coordinate order.
func collectSegments(c *counter, n *node, qlo, qhi int64, out []Fragment) []Fragment {
	if n == nil || !subtreeMayIntersect(n, qlo, qhi) {
		return out
	}
	c.touch(n)
	out = collectSegments(c, n.left, qlo, qhi, out)
	if n.lo < qhi && n.hi > qlo {
		out = append(out, Fragment{
			Lo:    max(n.lo, qlo),
			Hi:    min(n.hi, qhi),
			Value: n.val,
		})
	}
	out = collectSegments(c, n.right, qlo, qhi, out)
	return out
}

// collectIntersecting appends the raw stored segments intersecting [lo, hi).
func collectIntersecting(c *counter, n *node, lo, hi int64, out []*node) []*node {
	if n == nil || !subtreeMayIntersect(n, lo, hi) {
		return out
	}
	c.touch(n)
	out = collectIntersecting(c, n.left, lo, hi, out)
	if n.lo < hi && n.hi > lo {
		out = append(out, n)
	}
	out = collectIntersecting(c, n.right, lo, hi, out)
	return out
}

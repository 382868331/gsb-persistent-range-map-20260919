// Package rangemap implements an immutable map from int64 half-open
// intervals [lo, hi) to string values, backed by a persistent AVL tree.
//
// Every Set or Erase returns a new *Map; previous versions remain fully
// readable because mutated paths are copied and untouched subtrees are
// shared. The empty string is a legal value and is distinct from "not
// covered". Endpoints may be MinInt64 and MaxInt64; the point MaxInt64
// itself is never covered, and no hi+1 style arithmetic is used.
package rangemap

import (
	"errors"
)

// MaxFragments is the per-version fragment limit required by the spec.
const MaxFragments = 10000

var (
	// ErrInvalidRange is returned when lo >= hi.
	ErrInvalidRange = errors.New("rangemap: invalid range: require lo < hi")
	// ErrTooManyFragments is returned when an edit would make a version
	// hold more than MaxFragments segments. The receiver version is left
	// unchanged.
	ErrTooManyFragments = errors.New("rangemap: fragment limit exceeded")
)

// Stats reports work done by one operation.
type Stats struct {
	// Created is the number of tree nodes newly allocated.
	Created int
	// Visited is the number of existing nodes dereferenced.
	Visited int
	// Skipped counts shared subtrees proven identical without descent
	// (used by Diff; always 0 for other operations).
	Skipped int
}

// Map is one immutable version. The zero value is an empty map; prefer New.
type Map struct {
	root *node
}

// New returns an empty map.
func New() *Map { return &Map{} }

// Len returns the number of covered segments in this version.
func (m *Map) Len() int {
	if m == nil || m.root == nil {
		return 0
	}
	return m.root.size
}

func validRange(lo, hi int64) bool { return lo < hi }

// Set covers [lo, hi) with val, returning a new version. The empty string
// is a legal value. The edit coalesces with adjacent segments that carry
// the same value, and splits segments at the window boundary.
func (m *Map) Set(lo, hi int64, val string) (*Map, Stats, error) {
	if !validRange(lo, hi) {
		return m, Stats{}, ErrInvalidRange
	}
	c := &counter{}
	root := m.root

	hit := collectIntersecting(c, root, lo, hi, nil)
	for _, s := range hit {
		root = eraseKey(c, root, s.lo)
	}
	// Parts of cut segments outside the window keep their old coverage and
	// value; the window interior is what the Set replaces. At most one
	// stored segment can cross each window edge, so these keys never
	// collide with each other or with a surviving segment.
	for _, s := range hit {
		if s.lo < lo {
			root = insert(c, root, s.lo, lo, s.val)
		}
		if s.hi > hi {
			root = insert(c, root, hi, s.hi, s.val)
		}
	}

	effLo, effHi := lo, hi
	if left := endingAt(c, root, lo); left != nil && left.val == val {
		effLo = left.lo
		root = eraseKey(c, root, left.lo)
	}
	if right := startingAt(c, root, hi); right != nil && right.val == val {
		effHi = right.hi
		root = eraseKey(c, root, right.lo)
	}
	root = insert(c, root, effLo, effHi, val)

	if zsize(root) > MaxFragments {
		// The receiver is untouched: all edits happened on copied paths.
		return m, c.stats(), ErrTooManyFragments
	}
	return &Map{root: root}, c.stats(), nil
}

// Erase removes coverage over [lo, hi), splitting any segment it cuts and
// returning a new version. Empty regions and the empty string need no
// special handling.
func (m *Map) Erase(lo, hi int64) (*Map, Stats, error) {
	if !validRange(lo, hi) {
		return m, Stats{}, ErrInvalidRange
	}
	c := &counter{}
	root := m.root

	hit := collectIntersecting(c, root, lo, hi, nil)
	for _, s := range hit {
		root = eraseKey(c, root, s.lo)
	}
	// Remnants of cut segments. At most one segment can cross each edge of
	// the window, so at most two inserts and their keys never collide.
	for _, s := range hit {
		if s.lo < lo {
			root = insert(c, root, s.lo, lo, s.val)
		}
		if s.hi > hi {
			root = insert(c, root, hi, s.hi, s.val)
		}
	}

	if zsize(root) > MaxFragments {
		return m, c.stats(), ErrTooManyFragments
	}
	return &Map{root: root}, c.stats(), nil
}

// At reports whether x is covered and, if so, its value. The point
// MaxInt64 is never covered. It runs in O(log(n+1)).
func (m *Map) At(x int64) (value string, covered bool) {
	if m == nil {
		return "", false
	}
	c := &counter{}
	s := coverAt(c, m.root, x)
	if s == nil {
		return "", false
	}
	return s.val, true
}

// Fragment is one covered piece returned by a range query.
type Fragment struct {
	Lo, Hi int64
	Value  string
}

// Range returns covered fragments intersecting [lo, hi), clipped to the
// query window, in ascending order, with adjacent equal-valued pieces
// merged. Only covered fragments are returned; holes and uncovered space
// are simply absent (an empty string value is still reported).
func (m *Map) Range(lo, hi int64) ([]Fragment, Stats, error) {
	if !validRange(lo, hi) {
		return nil, Stats{}, ErrInvalidRange
	}
	c := &counter{}
	raw := collectSegments(c, m.root, lo, hi, nil)
	return mergeFragments(raw), c.stats(), nil
}

// Fragments returns every covered segment of this version, ascending.
func (m *Map) Fragments() []Fragment {
	if m == nil {
		return nil
	}
	c := &counter{}
	raw := collectSegments(c, m.root, minInt64, maxInt64, nil)
	return raw
}

// mergeFragments joins consecutive pieces that carry the same value. The
// stored invariant already guarantees this for whole segments, but clipping
// can place two equal pieces next to each other in an output window.
func mergeFragments(in []Fragment) []Fragment {
	if len(in) == 0 {
		return in
	}
	out := in[:0]
	for _, f := range in {
		if n := len(out); n > 0 && out[n-1].Hi == f.Lo && out[n-1].Value == f.Value {
			out[n-1].Hi = f.Hi
			continue
		}
		out = append(out, f)
	}
	return out
}

// Equal reports whether two versions cover exactly the same intervals with
// exactly the same values.
func (m *Map) Equal(o *Map) bool {
	var a, b []Fragment
	if m != nil {
		a = m.Fragments()
	}
	if o != nil {
		b = o.Fragments()
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

const (
	minInt64 = -1 << 63
	maxInt64 = 1<<63 - 1
)

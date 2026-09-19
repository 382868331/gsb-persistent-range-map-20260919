package rangemap

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
)

// ---- helpers ------------------------------------------------------------

func mustSet(t *testing.T, m *Map, lo, hi int64, v string) *Map {
	t.Helper()
	nm, err := m.Set(lo, hi, v)
	if err != nil {
		t.Fatalf("Set(%d,%d,%q): %v", lo, hi, v, err)
	}
	return nm
}

func mustErase(t *testing.T, m *Map, lo, hi int64) *Map {
	t.Helper()
	nm, err := m.Erase(lo, hi)
	if err != nil {
		t.Fatalf("Erase(%d,%d): %v", lo, hi, err)
	}
	return nm
}

func mustQuery(t *testing.T, m *Map, lo, hi int64) []Segment {
	t.Helper()
	got, err := m.Query(lo, hi)
	if err != nil {
		t.Fatalf("Query(%d,%d): %v", lo, hi, err)
	}
	return got
}

func hiMaxOf(n *node) int64 {
	if n == nil {
		return math.MinInt64
	}
	return n.hiMax
}

// validateTree checks AVL balance, augmentations, key order, disjointness
// and the "adjacent nodes carry distinct values" invariant.
func validateTree(t *testing.T, m *Map, label string) {
	t.Helper()
	var prev *node
	var check func(n *node, hasLo bool, dLo int64, hasHi bool, dHi int64) int
	check = func(n *node, hasLo bool, dLo int64, hasHi bool, dHi int64) int {
		if n == nil {
			return 0
		}
		if !(n.lo < n.hi) {
			t.Fatalf("%s: empty/negative node [%d,%d)", label, n.lo, n.hi)
		}
		if (hasLo && n.lo <= dLo) || (hasHi && n.lo >= dHi) {
			t.Fatalf("%s: BST key %d outside (%v:%d, %v:%d)",
				label, n.lo, hasLo, dLo, hasHi, dHi)
		}
		hl := check(n.left, hasLo, dLo, true, n.lo)
		if prev != nil {
			if !(prev.hi <= n.lo) {
				t.Fatalf("%s: overlapping nodes [%d,%d) and [%d,%d)",
					label, prev.lo, prev.hi, n.lo, n.hi)
			}
			if prev.hi == n.lo && prev.val == n.val {
				t.Fatalf("%s: adjacent equal-value nodes at %d", label, n.lo)
			}
		}
		prev = n
		hr := check(n.right, true, n.lo, hasHi, dHi)
		h := 1 + maxInt(hl, hr)
		if n.height != h {
			t.Fatalf("%s: height mismatch at %d: got %d want %d",
				label, n.lo, n.height, h)
		}
		if d := hl - hr; d < -1 || d > 1 {
			t.Fatalf("%s: unbalanced at %d (bf=%d)", label, n.lo, d)
		}
		wantCnt := int64(1) + countOf(n.left) + countOf(n.right)
		if n.cnt != wantCnt {
			t.Fatalf("%s: cnt mismatch at %d: %d want %d",
				label, n.lo, n.cnt, wantCnt)
		}
		wantHi := maxI64(n.hi, maxI64(hiMaxOf(n.left), hiMaxOf(n.right)))
		if n.hiMax != wantHi {
			t.Fatalf("%s: hiMax mismatch at %d: %d want %d",
				label, n.lo, n.hiMax, wantHi)
		}
		if n.id == 0 {
			t.Fatalf("%s: node without id at %d", label, n.lo)
		}
		return h
	}
	check(m.root, false, 0, false, 0)
	if got := int(countOf(m.root)); got != m.Len() {
		t.Fatalf("%s: Len %d != tree count %d", label, m.Len(), got)
	}
}

func findNode(n *node, lo int64) *node {
	for n != nil {
		switch {
		case lo < n.lo:
			n = n.left
		case lo > n.lo:
			n = n.right
		default:
			return n
		}
	}
	return nil
}

// ---- required cases -----------------------------------------------------

func TestCoverageSplitting(t *testing.T) {
	m := New()
	m = mustSet(t, m, 0, 100, "a")
	m = mustSet(t, m, 40, 60, "b")
	m = mustSet(t, m, 45, 55, "a") // carve a different island inside b
	validateTree(t, m, "split")

	want := []Segment{{0, 40, "a"}, {40, 45, "b"}, {45, 55, "a"},
		{55, 60, "b"}, {60, 100, "a"}}
	if got := m.Segments(); !reflect.DeepEqual(got, want) {
		t.Fatalf("segments:\n got %v\nwant %v", got, want)
	}

	// Point checks on and around every boundary.
	covered := func(p, want2 int64, v string) {
		t.Helper()
		gv, ok := m.Get(p)
		if !ok || gv != v {
			t.Fatalf("Get(%d) = (%q,%v), want (%q,true)", p, gv, ok, v)
		}
		_ = want2
	}
	hole := func(p int64) {
		t.Helper()
		if _, ok := m.Get(p); ok {
			t.Fatalf("Get(%d) unexpectedly covered", p)
		}
	}
	covered(0, 0, "a")
	covered(39, 0, "a")
	covered(40, 0, "b")
	covered(44, 0, "b")
	covered(45, 0, "a")
	covered(54, 0, "a")
	covered(55, 0, "b")
	covered(59, 0, "b")
	covered(60, 0, "a")
	covered(99, 0, "a")
	hole(-1)
	hole(100)

	// Erasing the middle island leaves two b remnants separated by a
	// hole (erase means uncovered, not "restore the previous value").
	m2 := mustErase(t, m, 45, 55)
	want2 := []Segment{{0, 40, "a"}, {40, 45, "b"},
		{55, 60, "b"}, {60, 100, "a"}}
	if got := m2.Segments(); !reflect.DeepEqual(got, want2) {
		t.Fatalf("after erase:\n got %v\nwant %v", got, want2)
	}
	// Erasing all of b leaves the a remnants separated by a hole.
	m3 := mustErase(t, m2, 40, 60)
	if got := m3.Segments(); !reflect.DeepEqual(got,
		[]Segment{{0, 40, "a"}, {60, 100, "a"}}) {
		t.Fatalf("after full erase: %v", m3.Segments())
	}
}

func TestEmptyValueVsHole(t *testing.T) {
	m := New()
	m = mustSet(t, m, 0, 5, "") // covered with empty string
	if v, ok := m.Get(2); !ok || v != "" {
		t.Fatalf("empty value: got (%q,%v)", v, ok)
	}
	got := mustQuery(t, m, 0, 5)
	if !reflect.DeepEqual(got, []Segment{{0, 5, ""}}) {
		t.Fatalf("empty value query: %v", got)
	}

	m = mustErase(t, m, 2, 3) // hole inside
	if _, ok := m.Get(2); ok {
		t.Fatal("hole point still covered")
	}
	if v, ok := m.Get(1); !ok || v != "" {
		t.Fatalf("empty neighbour: (%q,%v)", v, ok)
	}
	// Equal values separated by a hole must NOT merge.
	got = mustQuery(t, m, 0, 5)
	if want := []Segment{{0, 2, ""}, {3, 5, ""}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("hole query:\n got %v\nwant %v", got, want)
	}
	// Query entirely inside the hole returns nothing.
	if got = mustQuery(t, m, 2, 3); len(got) != 0 {
		t.Fatalf("hole-only query: %v", got)
	}
	// Filling the hole with empty value restores a single segment.
	m = mustSet(t, m, 2, 3, "")
	if got = m.Segments(); !reflect.DeepEqual(got, []Segment{{0, 5, ""}}) {
		t.Fatalf("refilled: %v", got)
	}
}

func TestAdjacentMerge(t *testing.T) {
	m := New()
	m = mustSet(t, m, 0, 5, "a")
	m = mustSet(t, m, 5, 10, "a") // touching, same value -> merge
	if got := m.Segments(); !reflect.DeepEqual(got, []Segment{{0, 10, "a"}}) {
		t.Fatalf("right merge: %v", got)
	}
	m = mustSet(t, m, -3, 0, "a") // merge on the left boundary
	m = mustSet(t, m, 10, 13, "a")
	if got := m.Segments(); !reflect.DeepEqual(got, []Segment{{-3, 13, "a"}}) {
		t.Fatalf("boundary merges: %v", got)
	}
	// Touching but different values stay separate.
	m = mustSet(t, m, 13, 20, "b")
	if got := m.Segments(); !reflect.DeepEqual(got,
		[]Segment{{-3, 13, "a"}, {13, 20, "b"}}) {
		t.Fatalf("different touch: %v", got)
	}
	// Covering across both with a merges everything.
	m = mustSet(t, m, -10, 30, "a")
	if got := m.Segments(); !reflect.DeepEqual(got, []Segment{{-10, 30, "a"}}) {
		t.Fatalf("global set: %v", got)
	}
}

func TestAdjacentDiffPairs(t *testing.T) {
	// Same old value, two different new values: runs must not merge.
	old := mustSet(t, New(), 0, 10, "a")
	nw := mustSet(t, old, 0, 5, "b")
	nw = mustSet(t, nw, 5, 10, "c")
	ch, _, err := Diff(old, nw, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{
		{Segment: Segment{0, 5, "b"}, OldCovered: true, OldValue: "a",
			NewCovered: true, NewValue: "b"},
		{Segment: Segment{5, 10, "c"}, OldCovered: true, OldValue: "a",
			NewCovered: true, NewValue: "c"},
	}
	if !reflect.DeepEqual(ch, want) {
		t.Fatalf("diff got %v want %v", ch, want)
	}

	// Two different old values, same new value: must not merge either.
	old2 := mustSet(t, New(), 0, 5, "a")
	old2 = mustSet(t, old2, 5, 10, "d")
	nw2 := mustSet(t, New(), 0, 10, "b")
	ch2, _, err := Diff(old2, nw2, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch2) != 2 {
		t.Fatalf("diff2 len = %d, want 2: %v", len(ch2), ch2)
	}

	// Set<->erase alternating pairs stay separate.
	old3 := mustSet(t, New(), 0, 10, "a")
	nw3 := mustErase(t, mustSet(t, New(), 5, 10, "a"), 0, 5)
	// new: hole [0,5), covered a [5,10)
	ch3, _, _ := Diff(old3, nw3, 0, 10)
	if len(ch3) != 1 || ch3[0].Lo != 0 || ch3[0].Hi != 5 ||
		ch3[0].OldCovered != true || ch3[0].NewCovered != false {
		t.Fatalf("diff3 = %v", ch3)
	}
}

func TestInt64Endpoints(t *testing.T) {
	const min = math.MinInt64
	const max = math.MaxInt64
	m := New()
	m = mustSet(t, m, min, max, "x") // whole domain except the max point
	if v, ok := m.Get(min); !ok || v != "x" {
		t.Fatalf("Get(min) = (%q,%v)", v, ok)
	}
	if v, ok := m.Get(-1); !ok || v != "x" {
		t.Fatalf("Get(-1) = (%q,%v)", v, ok)
	}
	if v, ok := m.Get(max - 1); !ok || v != "x" {
		t.Fatalf("Get(max-1) = (%q,%v)", v, ok)
	}
	if _, ok := m.Get(max); ok {
		t.Fatal("max point must never be covered")
	}
	got := mustQuery(t, m, min, max)
	if !reflect.DeepEqual(got, []Segment{{min, max, "x"}}) {
		t.Fatalf("full domain query: %v", got)
	}

	m = mustSet(t, m, max-10, max, "y")
	if v, ok := m.Get(max - 10); !ok || v != "y" {
		t.Fatalf("edge set: (%q,%v)", v, ok)
	}
	if _, ok := m.Get(max); ok {
		t.Fatal("max point covered after edge set")
	}
	m = mustSet(t, m, max-1, max, "")
	if v, ok := m.Get(max - 1); !ok || v != "" {
		t.Fatalf("empty at max-1: (%q,%v)", v, ok)
	}

	m = mustErase(t, m, min, 0)
	if _, ok := m.Get(min); ok {
		t.Fatal("min still covered after erase")
	}
	if _, ok := m.Get(-1); ok {
		t.Fatal("-1 still covered after erase")
	}
	if v, ok := m.Get(0); !ok || v != "x" {
		t.Fatalf("Get(0) = (%q,%v), want x", v, ok)
	}
	if v, ok := m.Get(max - 10); !ok || v != "y" {
		t.Fatalf("Get(max-10) = (%q,%v), want y", v, ok)
	}

	// lo >= hi errors, including lo == hi at MaxInt64.
	for _, c := range [][2]int64{{5, 5}, {5, 4}, {max, max}, {max, min}} {
		if _, err := New().Set(c[0], c[1], "z"); err != ErrInvalidRange {
			t.Fatalf("Set(%d,%d) err = %v", c[0], c[1], err)
		}
		if _, err := New().Erase(c[0], c[1]); err != ErrInvalidRange {
			t.Fatalf("Erase(%d,%d) err = %v", c[0], c[1], err)
		}
	}
}

func TestOldVersionImmutable(t *testing.T) {
	v0 := New()
	v1 := mustSet(t, v0, 0, 10, "a")
	v2 := mustSet(t, v1, 3, 7, "b")
	v3 := mustErase(t, v2, -5, 5)

	if got := v0.Segments(); len(got) != 0 {
		t.Fatalf("v0 mutated: %v", got)
	}
	if got := v1.Segments(); !reflect.DeepEqual(got, []Segment{{0, 10, "a"}}) {
		t.Fatalf("v1 mutated: %v", got)
	}
	want2 := []Segment{{0, 3, "a"}, {3, 7, "b"}, {7, 10, "a"}}
	if got := v2.Segments(); !reflect.DeepEqual(got, want2) {
		t.Fatalf("v2 mutated: %v", got)
	}
	want3 := []Segment{{5, 7, "b"}, {7, 10, "a"}}
	if got := v3.Segments(); !reflect.DeepEqual(got, want3) {
		t.Fatalf("v3: %v", got)
	}

	// Invalid input returns the same pointer and changes nothing.
	if nm, err := v2.Set(8, 8, "z"); err != ErrInvalidRange || nm != v2 {
		t.Fatalf("invalid Set: err=%v same=%v", err, nm == v2)
	}
	if nm, err := v2.Erase(8, 2); err != ErrInvalidRange || nm != v2 {
		t.Fatalf("invalid Erase: err=%v same=%v", err, nm == v2)
	}
	if got := v2.Segments(); !reflect.DeepEqual(got, want2) {
		t.Fatalf("v2 mutated by bad input: %v", got)
	}

	// Nil receiver behaves as the empty map.
	var nilMap *Map
	if _, ok := nilMap.Get(0); ok {
		t.Fatal("nil map covered a point")
	}
	if got, err := nilMap.Query(0, 1); err != nil || len(got) != 0 {
		t.Fatalf("nil map query: %v %v", got, err)
	}
	v4 := mustSet(t, nilMap, 1, 2, "q")
	if v, ok := v4.Get(1); !ok || v != "q" {
		t.Fatalf("set on nil map: (%q,%v)", v, ok)
	}
}

func TestQueryClipping(t *testing.T) {
	m := New()
	m = mustSet(t, m, 0, 10, "a")
	m = mustSet(t, m, 10, 20, "b")
	cases := []struct {
		lo, hi int64
		want   []Segment
	}{
		{-5, 0, nil},
		{-5, 5, []Segment{{0, 5, "a"}}},
		{5, 15, []Segment{{5, 10, "a"}, {10, 15, "b"}}},
		{10, 20, []Segment{{10, 20, "b"}}},
		{15, 25, []Segment{{15, 20, "b"}}},
		{20, 25, nil},
		{3, 4, []Segment{{3, 4, "a"}}},
	}
	for _, c := range cases {
		got, err := m.Query(c.lo, c.hi)
		if err != nil {
			t.Fatalf("Query(%d,%d): %v", c.lo, c.hi, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("Query(%d,%d) = %v, want %v", c.lo, c.hi, got, c.want)
		}
	}
	if _, err := m.Query(5, 5); err != ErrInvalidRange {
		t.Fatalf("bad query err = %v", err)
	}
}

func TestMaxSegments(t *testing.T) {
	m := New()
	var err error
	// Disjoint unit segments separated by gaps -> exactly the limit.
	for i := 0; i < MaxSegments; i++ {
		m, err = m.Set(int64(i*3), int64(i*3)+1, "x")
		if err != nil {
			t.Fatalf("set %d: %v", i, err)
		}
	}
	if m.Len() != MaxSegments {
		t.Fatalf("len = %d", m.Len())
	}
	// One more disjoint segment fails and leaves the version untouched.
	before := m.Segments()
	nm, err := m.Set(int64(MaxSegments*3), int64(MaxSegments*3)+1, "x")
	if err != ErrTooManySegments {
		t.Fatalf("want ErrTooManySegments, got %v", err)
	}
	if nm != m {
		t.Fatal("failed version should return same map")
	}
	if got := m.Segments(); !reflect.DeepEqual(got, before) {
		t.Fatal("map changed after rejected Set")
	}
}

// ---- reference-model randomized verification ---------------------------

const domLo, domHi = -16, 16

type refCell struct {
	present bool
	val     string
}

type refModel struct{ m map[int64]refCell }

func newRef() *refModel { return &refModel{m: map[int64]refCell{}} }

func (r *refModel) set(lo, hi int64, v string) {
	for p := lo; p < hi; p++ {
		r.m[p] = refCell{true, v}
	}
}

func (r *refModel) erase(lo, hi int64) {
	for p := lo; p < hi; p++ {
		r.m[p] = refCell{}
	}
}

func (r *refModel) get(p int64) refCell { return r.m[p] }

func (r *refModel) segments(lo, hi int64) []Segment {
	var out []Segment
	for p := lo; p < hi; p++ {
		c := r.get(p)
		if !c.present {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Hi == p && out[n-1].Value == c.val {
			out[n-1].Hi = p + 1
		} else {
			out = append(out, Segment{p, p + 1, c.val})
		}
	}
	return out
}

func assertMatchesRef(t *testing.T, m *Map, r *refModel, label string, lo, hi int64) {
	t.Helper()
	for p := lo; p < hi; p++ {
		gv, gok := m.Get(p)
		want := r.get(p)
		if gok != want.present || (gok && gv != want.val) {
			t.Fatalf("%s: Get(%d)=(%q,%v), want (%q,%v)",
				label, p, gv, gok, want.val, want.present)
		}
	}
	got, err := m.Query(lo, hi)
	if err != nil {
		t.Fatalf("%s Query: %v", label, err)
	}
	want := r.segments(lo, hi)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s Query:\n got %v\nwant %v", label, got, want)
	}
}

func TestRandomReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	values := []string{"a", "b", "", "c", "a"}

	type hist struct {
		m *Map
		r *refModel
	}
	history := []hist{{New(), newRef()}}

	for step := 0; step < 200; step++ {
		last := history[len(history)-1]
		lo := int64(rng.Intn(domHi-domLo) + domLo)
		hi := lo + 1 + int64(rng.Intn(6))
		if hi > domHi {
			hi = domHi
		}
		if lo >= hi {
			continue
		}
		var nm *Map
		nr := newRef()
		*nr = *last.r
		nr.m = cloneCells(last.r.m)
		if rng.Intn(10) < 7 {
			v := values[rng.Intn(len(values))]
			nm = mustSet(t, last.m, lo, hi, v)
			nr.set(lo, hi, v)
		} else {
			nm = mustErase(t, last.m, lo, hi)
			nr.erase(lo, hi)
		}
		validateTree(t, nm, "random")
		assertMatchesRef(t, nm, nr, "random", domLo, domHi)

		// All historical versions stay readable.
		for h, hh := range history {
			assertMatchesRef(t, hh.m, hh.r, "history", domLo, domHi)
			_ = h
		}

		// Diff against the previous version over the whole domain and
		// over random sub-ranges; rebuild via Apply.
		qRanges := [][2]int64{{domLo, domHi}}
		for j := 0; j < 3; j++ {
			ql := int64(rng.Intn(domHi-domLo) + domLo)
			qh := ql + 1 + int64(rng.Intn(domHi-domLo))
			if qh > domHi {
				qh = domHi
			}
			if ql < qh {
				qRanges = append(qRanges, [2]int64{ql, qh})
			}
		}
		for _, q := range qRanges {
			changes, dst, err := Diff(last.m, nm, q[0], q[1])
			if err != nil {
				t.Fatalf("Diff step %d: %v", step, err)
			}
			assertDiffShape(t, changes, q[0], q[1])
			assertDiffExact(t, last.r, nr, changes, q[0], q[1], step)
			rebuilt, err := Apply(last.m, changes)
			if err != nil {
				t.Fatalf("Apply step %d: %v", step, err)
			}
			assertMatchesRef(t, rebuilt, nr, "rebuilt", q[0], q[1])
			_ = dst
		}

		history = append(history, hist{nm, nr})
	}
}

func cloneCells(in map[int64]refCell) map[int64]refCell {
	out := make(map[int64]refCell, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func assertDiffShape(t *testing.T, changes []Change, lo, hi int64) {
	t.Helper()
	for i, ch := range changes {
		if !(lo <= ch.Lo && ch.Lo < ch.Hi && ch.Hi <= hi) {
			t.Fatalf("change %d out of range: %v", i, ch)
		}
		if ch.OldCovered == ch.NewCovered &&
			(!ch.OldCovered || ch.OldValue == ch.NewValue) {
			t.Fatalf("change %d is a non-change: %v", i, ch)
		}
		if i > 0 {
			prev := changes[i-1]
			if ch.Lo < prev.Hi {
				t.Fatalf("overlapping changes: %v %v", prev, ch)
			}
			if ch.Lo == prev.Hi {
				// Adjacent runs may coexist only when they could NOT
				// have been merged: at least one of the old pair or the
				// new pair differs between the runs.
				if prev.OldCovered == ch.OldCovered &&
					prev.OldValue == ch.OldValue &&
					prev.NewCovered == ch.NewCovered &&
					prev.NewValue == ch.NewValue {
					t.Fatalf("adjacent changes should have been merged: %v %v", prev, ch)
				}
			}
		}
	}
}

func assertDiffExact(t *testing.T, oldR, newR *refModel, changes []Change,
	lo, hi int64, step int) {
	t.Helper()
	for p := lo; p < hi; p++ {
		o := oldR.get(p)
		n := newR.get(p)
		differs := o != n
		var hit *Change
		for i := range changes {
			if changes[i].Lo <= p && p < changes[i].Hi {
				hit = &changes[i]
				break
			}
		}
		if differs && hit == nil {
			t.Fatalf("step %d: point %d differs but no change covers it", step, p)
		}
		if !differs && hit != nil {
			t.Fatalf("step %d: point %d equal but inside change %v", step, p, hit)
		}
		if hit != nil {
			if (hit.OldCovered != o.present) ||
				(hit.OldCovered && hit.OldValue != o.val) ||
				(hit.NewCovered != n.present) ||
				(hit.NewCovered && hit.NewValue != n.val) {
				t.Fatalf("step %d: change %v mislabels point %d: old=%v new=%v",
					step, hit, p, o, n)
			}
		}
	}
}

// ---- persistence: sharing and node accounting --------------------------

func TestStructuralSharingAndCounts(t *testing.T) {
	const n = 2000
	m := New()
	for i := 0; i < n; i++ {
		base := int64(i * 8)
		var err error
		m, err = m.Set(base, base+3, string(rune('a'+i%4)))
		if err != nil {
			t.Fatalf("build %d: %v", i, err)
		}
	}
	validateTree(t, m, "big")
	if m.Len() != n {
		t.Fatalf("big len = %d", m.Len())
	}

	// One sparse local edit intersecting a handful of segments.
	lo := int64(1000 * 8)
	nm := mustSet(t, m, lo, lo+30, "z") // hits ~4 segments
	stats := nm.RangeMapStats()
	t.Logf("local edit on n=%d: created=%d visited=%d", n, stats.Created, stats.Visited)
	if stats.Created <= 0 || stats.Visited <= 0 {
		t.Fatal("stats not recorded")
	}
	if stats.Created >= int64(n) {
		t.Fatalf("local edit copied %d nodes, expected sharing (n=%d)",
			stats.Created, n)
	}
	if stats.Visited >= int64(n) {
		t.Fatalf("local edit visited %d nodes, expected ~O((k+1)log n)",
			stats.Visited)
	}

	// Untouched segments are literally the same node pointers.
	for _, key := range []int64{0, 8, 8*500 + 0, 8 * (n - 1), 8 * (n - 2)} {
		if key == lo || findNode(m.root, key) == nil {
			continue
		}
		a := findNode(m.root, key)
		b := findNode(nm.root, key)
		if a == nil || b == nil || a != b {
			t.Fatalf("segment at %d not shared: %p vs %p", key, a, b)
		}
	}

	// Diff on the local change visits only the local region plus shared
	// skips; it must be far smaller than a full scan.
	_, dStats, err := Diff(m, nm, lo-40, lo+80)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("local diff: visited=%d created(n/a for diff)=%d",
		dStats.Visited, dStats.Created)
	if dStats.Visited >= int64(n) {
		t.Fatalf("local diff visited %d of %d nodes", dStats.Visited, n)
	}

	// A no-op erase reports work but allocates nothing new.
	pre := nextID
	_, err = nm.Erase(domLo2(), domLo2()+1) // region with no coverage
	if err != nil {
		t.Fatal(err)
	}
	if nextID != pre {
		t.Fatalf("no-op erase allocated %d nodes", nextID-pre)
	}
}

func domLo2() int64 { return -1_000_000 }

// Diff between two maps that share a large untouched prefix/suffix: the
// shared subtree shortcut should jump over it.
func TestDiffSharedSubtrees(t *testing.T) {
	m := New()
	for i := 0; i < 500; i++ {
		base := int64(i * 4)
		m = mustSet(t, m, base, base+2, "a")
	}
	// Change one middle segment.
	nm := mustSet(t, m, 4*250, 4*250+2, "b")
	ch, stats, err := Diff(m, nm, math.MinInt64, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch) != 1 || ch[0].Lo != 4*250 || ch[0].Hi != 4*250+2 {
		t.Fatalf("changes = %v", ch)
	}
	t.Logf("whole-domain diff on shared trees visited=%d nodes (tree has %d)",
		stats.Visited, m.Len())
	// A full linear scan would touch every node; whole shared subtrees
	// are jumped over, so traffic must be far below n.
	if stats.Visited >= int64(m.Len())/3 {
		t.Fatalf("shared-subtree skip ineffective: visited %d of %d",
			stats.Visited, m.Len())
	}
}

func TestDiffRangeOutsideChange(t *testing.T) {
	m := mustSet(t, New(), 0, 10, "a")
	nm := mustSet(t, m, 20, 30, "b")
	ch, _, err := Diff(m, nm, 40, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch) != 0 {
		t.Fatalf("expected no changes, got %v", ch)
	}
	ch, _, err = Diff(m, nm, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch) != 0 {
		t.Fatalf("query range avoids the new segment, got %v", ch)
	}
}

func TestEraseNoOpSameVersion(t *testing.T) {
	m := mustSet(t, New(), 0, 5, "a")
	nm, err := m.Erase(100, 200)
	if err != nil {
		t.Fatal(err)
	}
	if nm != m {
		t.Fatal("erase of uncovered range should return same version")
	}
}

func TestStatsMonotonicIDs(t *testing.T) {
	// Every structural operation records its actual created count.
	m := New()
	m = mustSet(t, m, 0, 1000, "a")
	if s := m.RangeMapStats(); s.Created != 1 {
		t.Fatalf("first set created = %d, want 1", s.Created)
	}
	before := nextID
	m = mustSet(t, m, 400, 600, "b")
	got := m.RangeMapStats().Created
	if got != int64(nextID-before) {
		t.Fatalf("created stats %d != allocated %d", got, nextID-before)
	}
}

package rangemap

import (
	"math/rand"
	"testing"
)

// ---- brute-force reference over the integer universe [-16,16) ------------

const refLo, refHi = -16, 16

type refMap struct{ m map[int64]string }

func newRef() *refMap { return &refMap{m: map[int64]string{}} }

func (r *refMap) clone() *refMap {
	c := newRef()
	for k, v := range r.m {
		c.m[k] = v
	}
	return c
}

func (r *refMap) set(lo, hi int64, v string) {
	for x := lo; x < hi; x++ {
		r.m[x] = v
	}
}

func (r *refMap) erase(lo, hi int64) {
	for x := lo; x < hi; x++ {
		delete(r.m, x)
	}
}

func (r *refMap) at(x int64) (string, bool) {
	v, ok := r.m[x]
	return v, ok
}

// frags returns covered runs clipped to [lo, hi), adjacent equal values merged.
func (r *refMap) frags(lo, hi int64) []Fragment {
	var out []Fragment
	for x := lo; x < hi; x++ {
		v, ok := r.m[x]
		if !ok {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Hi == x && out[n-1].Value == v {
			out[n-1].Hi = x + 1
		} else {
			out = append(out, Fragment{Lo: x, Hi: x + 1, Value: v})
		}
	}
	return out
}

func fragsEqual(a, b []Fragment) bool {
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

// assertAgainstRef compares At, Range and Fragments against the reference.
func assertAgainstRef(t *testing.T, m *Map, r *refMap) {
	t.Helper()
	for x := int64(refLo); x < refHi; x++ {
		gotV, gotOK := m.At(x)
		wantV, wantOK := r.at(x)
		if gotOK != wantOK || (gotOK && gotV != wantV) {
			t.Fatalf("At(%d) = (%q,%v), want (%q,%v)", x, gotV, gotOK, wantV, wantOK)
		}
	}
	windows := [][2]int64{{refLo, refHi}, {-16, 0}, {0, 16}, {-7, 9}, {-3, 3}, {5, 6}, {-16, -15}, {15, 16}}
	for _, w := range windows {
		got, _, err := m.Range(w[0], w[1])
		if err != nil {
			t.Fatalf("Range(%d,%d): %v", w[0], w[1], err)
		}
		if want := r.frags(w[0], w[1]); !fragsEqual(got, want) {
			t.Fatalf("Range(%d,%d) = %v, want %v", w[0], w[1], got, want)
		}
	}
	if got, want := m.Fragments(), r.frags(refLo, refHi); !fragsEqual(got, want) {
		t.Fatalf("Fragments = %v, want %v", got, want)
	}
}

// ---- fixed-seed random history walk vs reference -------------------------

func TestRandomReferenceFixedSeed(t *testing.T) {
	const seed = 20260919
	rng := rand.New(rand.NewSource(seed))
	values := []string{"a", "b", "c", "", "ab", ""}

	versions := []*Map{New()}
	refs := []*refMap{newRef()}

	const ops = 220
	for op := 0; op < ops; op++ {
		base := rng.Intn(len(versions))
		m, r := versions[base], refs[base].clone()

		lo := int64(refLo + rng.Intn(refHi-refLo))
		hi := int64(refLo + rng.Intn(refHi-refLo))
		if lo > hi {
			lo, hi = hi, lo
		}
		if lo == hi {
			hi++
		}
		v := values[rng.Intn(len(values))]

		var nm *Map
		var err error
		if rng.Intn(3) == 0 {
			nm, _, err = m.Erase(lo, hi)
			if err != nil {
				t.Fatalf("Erase(%d,%d): %v", lo, hi, err)
			}
			r.erase(lo, hi)
		} else {
			nm, _, err = m.Set(lo, hi, v)
			if err != nil {
				t.Fatalf("Set(%d,%d): %v", lo, hi, err)
			}
			r.set(lo, hi, v)
		}
		versions = append(versions, nm)
		refs = append(refs, r)
		assertAgainstRef(t, nm, r)
		verifyTree(t, nm.root)
	}

	// Diff: fixed-seed cross-version checks, pointwise and reconstruction.
	for i := 0; i < 60; i++ {
		oi := rng.Intn(len(versions))
		ni := rng.Intn(len(versions))
		lo := int64(refLo + rng.Intn(refHi-refLo))
		hi := lo + int64(1+rng.Intn(int(refHi-lo)))
		diffs, _, err := Diff(versions[oi], versions[ni], lo, hi)
		if err != nil {
			t.Fatalf("Diff: %v", err)
		}
		validateDiffs(t, versions[oi], versions[ni], diffs, lo, hi)
		rebuilt, _, err := ApplyDiff(versions[oi], diffs)
		if err != nil {
			t.Fatalf("ApplyDiff: %v", err)
		}
		for x := int64(refLo); x < refHi; x++ {
			wantM := versions[ni]
			if x < lo || x >= hi {
				wantM = versions[oi]
			}
			want, wantOK := wantM.At(x)
			gv, gok := rebuilt.At(x)
			if gok != wantOK || (gok && gv != want) {
				t.Fatalf("rebuild op=%d pair=(%d,%d) window=[%d,%d) At(%d)=(%q,%v),want (%q,%v)",
					i, oi, ni, lo, hi, x, gv, gok, want, wantOK)
			}
		}
	}
}

// validateDiffs checks exactness and the merge rule pointwise.
func validateDiffs(t *testing.T, oldM, newM *Map, diffs []DiffRange, lo, hi int64) {
	t.Helper()
	var cursor int64 = lo
	for i, d := range diffs {
		if d.Lo < cursor || d.Lo < lo || d.Hi <= d.Lo || d.Hi > hi {
			t.Fatalf("malformed diff #%d %+v (cursor %d window [%d,%d))", i, d, cursor, lo, hi)
		}
		// The pair must be constant across the range and actually differ.
		if d.OldCovered == d.NewCovered && d.Old == d.New {
			t.Fatalf("diff %+v covers an unchanged range", d)
		}
		for x := d.Lo; x < d.Hi; x++ {
			ov, ook := oldM.At(x)
			nv, nok := newM.At(x)
			if ook != d.OldCovered || (ook && ov != d.Old) ||
				nok != d.NewCovered || (nok && nv != d.New) {
				t.Fatalf("diff %+v wrong at %d: old=(%q,%v) new=(%q,%v)", d, x, ov, ook, nv, nok)
			}
		}
		if i > 0 {
			p := diffs[i-1]
			if p.OldCovered == d.OldCovered && p.Old == d.Old &&
				p.NewCovered == d.NewCovered && p.New == d.New {
				t.Fatalf("adjacent diffs %+v,%+v should have merged", p, d)
			}
		}
		cursor = d.Hi
	}
	// Every differing integer point must be covered by exactly one diff.
	for x := lo; x < hi; x++ {
		ov, ook := oldM.At(x)
		nv, nok := newM.At(x)
		differs := ook != nok || (ook && ov != nv)
		found := false
		for _, d := range diffs {
			if x >= d.Lo && x < d.Hi {
				found = true
				break
			}
		}
		if differs != found {
			t.Fatalf("point %d differs=%v but diff coverage=%v (%v)", x, differs, found, diffs)
		}
	}
}

// ---- specific spec scenarios ---------------------------------------------

func TestCoverageSplitting(t *testing.T) {
	m := New()
	m, _, err := m.Set(0, 10, "a")
	if err != nil {
		t.Fatal(err)
	}
	m, _, err = m.Set(4, 6, "b")
	if err != nil {
		t.Fatal(err)
	}
	want := []Fragment{{0, 4, "a"}, {4, 6, "b"}, {6, 10, "a"}}
	if got := m.Fragments(); !fragsEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	got, _, _ := m.Range(2, 8)
	want = []Fragment{{2, 4, "a"}, {4, 6, "b"}, {6, 8, "a"}}
	if !fragsEqual(got, want) {
		t.Fatalf("clipped range got %v want %v", got, want)
	}
	got, _, _ = m.Range(-100, 4)
	if !fragsEqual(got, []Fragment{{0, 4, "a"}}) {
		t.Fatalf("left clip %v", got)
	}
	got, _, _ = m.Range(6, 100)
	if !fragsEqual(got, []Fragment{{6, 10, "a"}}) {
		t.Fatalf("right clip %v", got)
	}
	if v, ok := m.At(3); !ok || v != "a" {
		t.Fatalf("At(3)=(%q,%v)", v, ok)
	}
}

func TestEmptyValueAndHole(t *testing.T) {
	m, _, _ := New().Set(0, 5, "")
	if v, ok := m.At(2); !ok || v != "" {
		t.Fatalf("empty string must be a real value, got (%q,%v)", v, ok)
	}
	m, _, _ = m.Erase(2, 3)
	if _, ok := m.At(2); ok {
		t.Fatal("erased hole must report uncovered")
	}
	if v, ok := m.At(1); !ok || v != "" {
		t.Fatalf("empty value outside hole changed: (%q,%v)", v, ok)
	}
	got, _, _ := m.Range(0, 5)
	want := []Fragment{{0, 2, ""}, {3, 5, ""}}
	if !fragsEqual(got, want) {
		t.Fatalf("hole query got %v want %v", got, want)
	}
	if got := m.Fragments(); !fragsEqual(got, want) {
		t.Fatalf("fragments around hole got %v want %v", got, want)
	}
	// Filling the hole with the same value restores one segment.
	m2, _, _ := m.Set(2, 3, "")
	if got := m2.Fragments(); !fragsEqual(got, []Fragment{{0, 5, ""}}) {
		t.Fatalf("refill merge got %v", got)
	}
	// Erasing a no-op region changes nothing semantically.
	m3, _, err := m.Erase(100, 200)
	if err != nil || !m3.Equal(m) {
		t.Fatal("erase in empty region should be a semantic no-op")
	}
}

func TestAdjacentMerge(t *testing.T) {
	m, _, _ := New().Set(0, 2, "x")
	m, _, _ = m.Set(2, 4, "x")
	if got := m.Fragments(); !fragsEqual(got, []Fragment{{0, 4, "x"}}) {
		t.Fatalf("adjacent equal did not merge: %v", got)
	}
	// Extend left and right with the same value.
	m, _, _ = m.Set(-2, 0, "x")
	m, _, _ = m.Set(4, 6, "x")
	if got := m.Fragments(); !fragsEqual(got, []Fragment{{-2, 6, "x"}}) {
		t.Fatalf("side merge failed: %v", got)
	}
	// Different value in the middle splits it.
	m, _, _ = m.Set(1, 3, "y")
	if got := m.Fragments(); !fragsEqual(got, []Fragment{{-2, 1, "x"}, {1, 3, "y"}, {3, 6, "x"}}) {
		t.Fatalf("split failed: %v", got)
	}
	// Overwrite restores a single run.
	m, _, _ = m.Set(-2, 6, "x")
	if got := m.Fragments(); !fragsEqual(got, []Fragment{{-2, 6, "x"}}) {
		t.Fatalf("overwrite merge failed: %v", got)
	}
}

func TestInt64Endpoints(t *testing.T) {
	m, st, err := New().Set(minInt64, maxInt64, "all")
	if err != nil {
		t.Fatal(err)
	}
	if m.Len() != 1 {
		t.Fatalf("Len=%d want 1", m.Len())
	}
	for _, x := range []int64{minInt64, -1, 0, 1, maxInt64 - 1} {
		if v, ok := m.At(x); !ok || v != "all" {
			t.Fatalf("At(%d)=(%q,%v)", x, v, ok)
		}
	}
	if _, ok := m.At(maxInt64); ok {
		t.Fatal("MaxInt64 point must never be covered")
	}
	got, _, _ := m.Range(minInt64, maxInt64)
	if !fragsEqual(got, []Fragment{{minInt64, maxInt64, "all"}}) {
		t.Fatalf("full-domain range: %v", got)
	}
	// Erasing a middle cell splits into two segments touching the extremes.
	m2, _, _ := m.Erase(0, 1)
	if got := m2.Fragments(); !fragsEqual(got, []Fragment{
		{minInt64, 0, "all"},
		{1, maxInt64, "all"},
	}) {
		t.Fatalf("endpoint split: %v", got)
	}
	m3, _, _ := m2.Set(0, 1, "all")
	if !m3.Equal(m) {
		t.Fatal("refilling the endpoint split should restore the original")
	}
	// Sets anchored at each extreme; no overflow anywhere.
	e, _, err := New().Set(minInt64, 0, "L")
	if err != nil {
		t.Fatal(err)
	}
	e, _, err = e.Set(0, maxInt64, "R")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := e.At(minInt64); !ok || v != "L" {
		t.Fatalf("left extreme: (%q,%v)", v, ok)
	}
	if v, ok := e.At(maxInt64 - 1); !ok || v != "R" {
		t.Fatalf("right extreme: (%q,%v)", v, ok)
	}
	if st.Created <= 0 {
		t.Fatalf("stats should record created nodes, got %+v", st)
	}
}

func TestOldVersionImmutable(t *testing.T) {
	v0 := New()
	v1, _, _ := v0.Set(0, 10, "a")
	v2, _, _ := v1.Set(3, 7, "b")
	v3, _, _ := v2.Erase(-5, 5)

	if v0.Len() != 0 {
		t.Fatal("v0 mutated")
	}
	if got := v1.Fragments(); !fragsEqual(got, []Fragment{{0, 10, "a"}}) {
		t.Fatalf("v1 mutated: %v", got)
	}
	if got := v2.Fragments(); !fragsEqual(got, []Fragment{{0, 3, "a"}, {3, 7, "b"}, {7, 10, "a"}}) {
		t.Fatalf("v2 mutated: %v", got)
	}
	if got := v3.Fragments(); !fragsEqual(got, []Fragment{{5, 7, "b"}, {7, 10, "a"}}) {
		t.Fatalf("v3 wrong: %v", got)
	}
	// Branching from an old version leaves both branches intact.
	branch, _, _ := v1.Set(0, 2, "c")
	if got := branch.Fragments(); !fragsEqual(got, []Fragment{{0, 2, "c"}, {2, 10, "a"}}) {
		t.Fatalf("branch: %v", got)
	}
	if got := v2.Fragments(); !fragsEqual(got, []Fragment{{0, 3, "a"}, {3, 7, "b"}, {7, 10, "a"}}) {
		t.Fatalf("sibling v2 mutated by branch: %v", got)
	}
}

func TestInvalidInputLeavesOldVersion(t *testing.T) {
	m, _, _ := New().Set(0, 5, "a")
	snapshot := m.Fragments()
	for _, tc := range [][2]int64{{5, 5}, {9, 4}, {0, 0}} {
		nm, st, err := m.Set(tc[0], tc[1], "z")
		if err != ErrInvalidRange || nm != m {
			t.Fatalf("Set(%d,%d) err=%v same=%v", tc[0], tc[1], err, nm == m)
		}
		if st != (Stats{}) {
			t.Fatalf("Set(%d,%d) stats must be zero, got %+v", tc[0], tc[1], st)
		}
		ne, est, eerr := m.Erase(tc[0], tc[1])
		if eerr != ErrInvalidRange || ne != m || est != (Stats{}) {
			t.Fatalf("Erase(%d,%d) err=%v same=%v stats=%+v", tc[0], tc[1], eerr, ne == m, est)
		}
		if _, _, rerr := m.Range(tc[0], tc[1]); rerr != ErrInvalidRange {
			t.Fatalf("Range(%d,%d) err=%v", tc[0], tc[1], rerr)
		}
		if _, _, derr := Diff(m, m, tc[0], tc[1]); derr != ErrInvalidRange {
			t.Fatalf("Diff(%d,%d) err=%v", tc[0], tc[1], derr)
		}
	}
	if got := m.Fragments(); !fragsEqual(got, snapshot) {
		t.Fatalf("old version changed after bad input: %v", got)
	}
}

func TestDiffAdjacentValuePairs(t *testing.T) {
	// Same old value, two different new values: must stay two diffs.
	old, _, _ := New().Set(0, 2, "a")
	nw, _, _ := New().Set(0, 1, "b")
	nw, _, _ = nw.Set(1, 2, "c")
	diffs, _, err := Diff(old, nw, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []DiffRange{
		{0, 1, true, "a", true, "b"},
		{1, 2, true, "a", true, "c"},
	}
	if len(diffs) != 2 || diffs[0] != want[0] || diffs[1] != want[1] {
		t.Fatalf("got %+v want %+v", diffs, want)
	}

	// Two different old values, same new value: also two diffs.
	old2, _, _ := New().Set(0, 1, "a")
	old2, _, _ = old2.Set(1, 2, "b")
	new2, _, _ := New().Set(0, 2, "c")
	diffs, _, _ = Diff(old2, new2, 0, 2)
	want = []DiffRange{
		{0, 1, true, "a", true, "c"},
		{1, 2, true, "b", true, "c"},
	}
	if len(diffs) != 2 || diffs[0] != want[0] || diffs[1] != want[1] {
		t.Fatalf("got %+v want %+v", diffs, want)
	}

	// Same old/new pair over adjacent cells merges into one diff run.
	old3, _, _ := New().Set(0, 2, "a")
	new3 := New()
	diffs, _, _ = Diff(old3, new3, 0, 2)
	if len(diffs) != 1 || diffs[0] != (DiffRange{0, 2, true, "a", false, ""}) {
		t.Fatalf("erase merge got %+v", diffs)
	}
	diffs, _, _ = Diff(new3, old3, 0, 2)
	if len(diffs) != 1 || diffs[0] != (DiffRange{0, 2, false, "", true, "a"}) {
		t.Fatalf("add merge got %+v", diffs)
	}

	// Window clipping: differences outside the window are not reported.
	diffs, _, _ = Diff(old, nw, 0, 1)
	if len(diffs) != 1 || diffs[0] != (DiffRange{0, 1, true, "a", true, "b"}) {
		t.Fatalf("clipped diff got %+v", diffs)
	}
}

func TestDiffSharedSubtreeSkipped(t *testing.T) {
	m := New()
	for i := 0; i < 20; i++ {
		m, _, _ = m.Set(int64(i*8), int64(i*8+2), "v")
	}
	// Comparing a version with itself: the whole root is one shared subtree.
	// Each of Diff's two paired traversals records the skip.
	diffs, st, err := Diff(m, m, minInt64, maxInt64)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 0 {
		t.Fatalf("self diff must be empty, got %+v", diffs)
	}
	if st.Skipped < m.Len() {
		t.Fatalf("Skipped=%d want at least %d", st.Skipped, m.Len())
	}
}

func TestFragmentLimit(t *testing.T) {
	m := New()
	var err error
	for i := 0; i < MaxFragments; i++ {
		m, _, err = m.Set(int64(i*8), int64(i*8+2), "x")
		if err != nil {
			t.Fatalf("segment %d: %v", i, err)
		}
	}
	if m.Len() != MaxFragments {
		t.Fatalf("Len=%d want %d", m.Len(), MaxFragments)
	}
	old := m
	m2, _, err := m.Set(int64(MaxFragments*8), int64(MaxFragments*8+2), "x")
	if err != ErrTooManyFragments {
		t.Fatalf("want ErrTooManyFragments, got %v", err)
	}
	if m2 != old || m2.Len() != MaxFragments {
		t.Fatal("receiver must be returned unchanged after limit error")
	}
}

func TestSparseLocalChangeNodeCount(t *testing.T) {
	m := New()
	for i := 0; i < 2000; i++ {
		var err error
		m, _, err = m.Set(int64(i*8), int64(i*8+2), "v")
		if err != nil {
			t.Fatal(err)
		}
	}
	// One tiny edit far inside the structure: path copying should allocate
	// O(log n) nodes, nowhere near a full O(n) rebuild.
	_, st, err := m.Set(1000, 1001, "w")
	if err != nil {
		t.Fatal(err)
	}
	if st.Created >= m.Len()/4 {
		t.Fatalf("local edit created %d nodes for %d segments: not path copying", st.Created, m.Len())
	}
	if st.Visited <= 0 {
		t.Fatalf("local edit visited no nodes: %+v", st)
	}
}

// ---- structural AVL verification ------------------------------------------

func verifyTree(t *testing.T, n *node) {
	t.Helper()
	var rec func(n *node) (int, int, int64, int64)
	rec = func(n *node) (height, size int, mn, mx int64) {
		if n == nil {
			return 0, 0, maxInt64, minInt64
		}
		lh, ls, lmn, lmx := rec(n.left)
		rh, rs, rmn, rmx := rec(n.right)
		if n.left != nil && n.left.maxKey() >= n.lo {
			t.Fatalf("BST order violated at lo=%d", n.lo)
		}
		if n.right != nil && n.right.minKey() <= n.lo {
			t.Fatalf("BST order violated at lo=%d", n.lo)
		}
		bf := lh - rh
		if bf > 1 || bf < -1 {
			t.Fatalf("AVL imbalance bf=%d at lo=%d", bf, n.lo)
		}
		if h := 1 + max(lh, rh); h != n.height {
			t.Fatalf("height wrong at lo=%d: %d want %d", n.lo, n.height, h)
		}
		if s := 1 + ls + rs; s != n.size {
			t.Fatalf("size wrong at lo=%d: %d want %d", n.lo, n.size, s)
		}
		mn, mx = n.lo, n.hi
		if n.left != nil {
			mn = min(mn, lmn)
			mx = max(mx, lmx)
		}
		if n.right != nil {
			mn = min(mn, rmn)
			mx = max(mx, rmx)
		}
		if mn != n.minLo || mx != n.maxHi {
			t.Fatalf("bbox wrong at lo=%d: got [%d,%d) want [%d,%d)", n.lo, n.minLo, n.maxHi, mn, mx)
		}
		return n.height, n.size, mn, mx
	}
	rec(n)
}

func (n *node) minKey() int64 {
	for n.left != nil {
		n = n.left
	}
	return n.lo
}

func (n *node) maxKey() int64 {
	for n.right != nil {
		n = n.right
	}
	return n.lo
}

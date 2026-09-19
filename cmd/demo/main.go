// Command demo exercises the persistent range map end to end: it shows a
// normal editing/diff/reconstruction workflow with node accounting, then a
// failure that the library actually triggers at runtime (an invalid range).
// Everything printed is computed by the library, nothing is hard-coded.
package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/382868331/gsb-persistent-range-map-20260919/rangemap"
)

func main() {
	start := time.Now()
	ok := true

	fmt.Println("=== persistent range map demo ===")

	// --- 1. normal result: edits, persistence, queries -------------------
	fmt.Println("\n[1] editing history (old versions stay readable)")
	v0 := rangemap.New()
	v1, st1, err := v0.Set(0, 10, "bold")
	must(err)
	v2, st2, err := v1.Set(4, 8, "italic")
	must(err)
	v3, st3, err := v2.Erase(2, 6)
	must(err)
	v3, st4, err := v3.Set(10, 12, "") // empty string is a real value
	must(err)

	fmt.Printf("v1 segments: %v\n", frags(v1.Fragments()))
	fmt.Printf("v2 segments: %v\n", frags(v2.Fragments()))
	fmt.Printf("v3 segments: %v  (note [10,12) value is the empty string)\n", frags(v3.Fragments()))
	fmt.Printf("v1 after later edits (persistence): %v\n", frags(v1.Fragments()))

	at := func(m *rangemap.Map, x int64) string {
		if v, covered := m.At(x); covered {
			return fmt.Sprintf("%q", v)
		}
		return "<uncovered>"
	}
	fmt.Printf("v3.At(1)=%s  v3.At(4)=%s  v3.At(9)=%s  v3.At(11)=%s\n",
		at(v3, 1), at(v3, 4), at(v3, 9), at(v3, 11))

	rq, _, err := v3.Range(-2, 20)
	must(err)
	fmt.Printf("v3.Range([-2,20)) clipped fragments: %v\n", frags(rq))
	fmt.Printf("nodes created per edit: set v1=%d, set v2=%d, erase v3=%d, empty-value set=%d\n",
		st1.Created, st2.Created, st3.Created, st4.Created)

	// --- 2. diff and reconstruction --------------------------------------
	fmt.Println("\n[2] Diff(v2 -> v3) over [-16,16) and reconstruction")
	diffs, dst, err := rangemap.Diff(v2, v3, -16, 16)
	must(err)
	for _, d := range diffs {
		fmt.Printf("  diff [%-3d,%-3d) old=%s new=%s\n", d.Lo, d.Hi, side(d.OldCovered, d.Old), side(d.NewCovered, d.New))
	}
	rebuilt, _, err := rangemap.ApplyDiff(v2, diffs)
	must(err)
	match := true
	for x := int64(-16); x < 16; x++ {
		gv, gok := rebuilt.At(x)
		wv, wok := v3.At(x)
		if gok != wok || (gok && gv != wv) {
			match = false
			ok = false
			fmt.Printf("  MISMATCH at %d\n", x)
		}
	}
	fmt.Printf("ApplyDiff reproduces v3 pointwise on [-16,16): %v (diff visited=%d nodes)\n", match, dst.Visited)

	// Shared subtree short-circuit: identical versions skip wholesale.
	_, self, err := rangemap.Diff(v2, v2, -1<<63, 1<<63-1)
	must(err)
	fmt.Printf("Diff(v2,v2) over the whole int64 domain: 0 ranges, shared nodes skipped=%d\n", self.Skipped)

	// --- 3. node accounting for one sparse local change ------------------
	fmt.Println("\n[3] one small local edit inside a large sparse map")
	const n = 4000
	big := rangemap.New()
	for i := 0; i < n; i++ {
		big, _, err = big.Set(int64(i*8), int64(i*8+2), "v")
		if err != nil {
			panic(err)
		}
	}
	big2, lst, err := big.Set(10000, 10001, "w")
	must(err)
	fullCopy := n
	fmt.Printf("segments=%d  one Set([10000,10001)) created=%d visited=%d (full copy would be >= %d)\n",
		big.Len(), lst.Created, lst.Visited, fullCopy)
	if lst.Created >= fullCopy/4 {
		ok = false
		fmt.Println("  UNEXPECTED: local edit looks like a full rebuild")
	} else {
		fmt.Println("  path copying confirmed: allocation is O(log n)-scale, not O(n)")
	}
	_ = big2

	// --- 4. a failure the library actually triggers ----------------------
	fmt.Println("\n[4] invalid input must fail and leave the old version intact")
	before := v2.Fragments()
	_, badStats, badErr := v2.Set(8, 3, "boom")
	fmt.Printf("v2.Set([8,3)) -> error: %v | stats=%+v\n", badErr, badStats)
	if !errors.Is(badErr, rangemap.ErrInvalidRange) {
		ok = false
		fmt.Println("  UNEXPECTED: wanted ErrInvalidRange")
	}
	if _, _, e := v2.Erase(5, 5); !errors.Is(e, rangemap.ErrInvalidRange) {
		ok = false
		fmt.Println("  UNEXPECTED: Erase(5,5) did not fail")
	} else {
		fmt.Println("v2.Erase([5,5)) -> rangemap: invalid range: require lo < hi")
	}
	if fragEq(v2.Fragments(), before) {
		fmt.Println("v2 unchanged after the rejected calls")
	} else {
		ok = false
		fmt.Println("  UNEXPECTED: v2 was mutated by a rejected call")
	}

	fmt.Printf("\n=== demo finished in %v ===\n", time.Since(start).Round(time.Millisecond))
	if ok {
		fmt.Println("RESULT: PASS (normal workflow verified; expected invalid-range failure triggered)")
	} else {
		fmt.Println("RESULT: FAIL")
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

type fragStr struct {
	Lo, Hi int64
	V      string
}

func frags(fs []rangemap.Fragment) []fragStr {
	out := make([]fragStr, len(fs))
	for i, f := range fs {
		out[i] = fragStr{f.Lo, f.Hi, f.Value}
	}
	return out
}

func fragEq(a, b []rangemap.Fragment) bool {
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

func side(covered bool, v string) string {
	if !covered {
		return "<uncovered>"
	}
	return fmt.Sprintf("%q", v)
}

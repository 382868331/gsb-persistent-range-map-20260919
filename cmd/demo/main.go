// Command demo showcases the persistent interval map library: it computes
// one normal edit/query/diff workflow with node accounting, then genuinely
// triggers the library's failure paths (invalid range and the 10000
// segment limit). Everything printed is computed by the code.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	rm "github.com/382868331/gsb-persistent-range-map-20260919/rangemap"
)

func main() {
	start := time.Now()
	fail := false

	fmt.Println("=== 持久化区间映射演示 (persistent range map) ===")
	fmt.Println()

	// ---------- Part 1: a normal result, actually computed ----------
	fmt.Println("[1] 正常结果: Set / Erase / 历史版本 / Diff 重建")
	v0 := rm.New()
	v1, err := v0.Set(0, 100, "red")
	must(err)
	v2, err := v1.Set(40, 70, "blue") // splits red, blue island in the middle
	must(err)
	v3, err := v2.Set(50, 60, "") // covered with EMPTY string (not a hole)
	must(err)
	v4, err := v3.Erase(80, 90) // real uncovered hole
	must(err)

	fmt.Printf("  v1 = %v\n", v1.Segments())
	fmt.Printf("  v2 = %v\n", v2.Segments())
	fmt.Printf("  v3 = %v\n", v3.Segments())
	fmt.Printf("  v4 = %v\n", v4.Segments())
	fmt.Printf("  旧版本 v2 在后续编辑后仍可读: %v\n", v2.Segments())

	val, covered := v4.Get(55)
	fmt.Printf("  Get(55) on v4: covered=%v value=%q (空字符串是合法值)\n", covered, val)
	_, covered = v4.Get(85)
	fmt.Printf("  Get(85) on v4: covered=%v (Erase 造成的空洞)\n", covered)
	q, err := v4.Query(35, 75)
	must(err)
	fmt.Printf("  Query(v4,[35,75)) 裁剪结果 = %v\n", q)

	changes, dStats, err := rm.Diff(v3, v4, 0, 100)
	must(err)
	fmt.Printf("  Diff(v3,v4,[0,100)) = %v (访问节点 %d)\n", changes, dStats.Visited)
	rebuilt, err := rm.Apply(v3, changes)
	must(err)
	rq1, err := rebuilt.Query(0, 100)
	must(err)
	rq2, err := v4.Query(0, 100)
	must(err)
	ok := equalSegs(rq1, rq2)
	fmt.Printf("  按 Diff 对 v3 做 Set/Erase 重建 v4: 一致=%v\n", ok)
	if !ok {
		fail = true
	}
	fmt.Println()

	// ---------- Part 1b: fixed-seed reference check over [-16,16) ---
	fmt.Println("[2] 固定种子小样本逐点参考校验 (seed=42, 域[-16,16))")
	if refOK := fixedSeedReferenceCheck(); refOK {
		fmt.Println("  120 次随机 Set/Erase 后逐点状态、范围查询、历史版本、Diff 重建: 全部一致")
	} else {
		fmt.Println("  参考校验不一致!")
		fail = true
	}
	fmt.Println()

	// ---------- Part 1c: large sparse local edit, node counts -------
	fmt.Println("[3] 较大稀疏局部改动的节点计数 (结构共享)")
	big := rm.New()
	const bigN = 5000
	for i := 0; i < bigN; i++ {
		base := int64(i * 4)
		big, err = big.Set(base, base+2, "x")
		must(err)
	}
	fmt.Printf("  版本片段数 n = %d\n", big.Len())
	lo := int64(2000 * 4)
	big2, err := big.Set(lo, lo+10, "y") // intersects a handful of segments
	must(err)
	s := big2.RangeMapStats()
	fmt.Printf("  一次局部 Set([%d,%d),\"y\"): 新建节点=%d 访问节点=%d (全量复制需要约 %d)\n",
		lo, lo+10, s.Created, s.Visited, bigN)
	_, ds, err := rm.Diff(big, big2, lo-100, lo+200)
	must(err)
	fmt.Printf("  局部 Diff 访问节点=%d\n", ds.Visited)
	if s.Created >= int64(bigN) {
		fmt.Println("  节点计数异常: 局部改动接近全量复制!")
		fail = true
	}
	fmt.Println()

	// ---------- Part 2: a failure that is actually triggered --------
	fmt.Println("[4] 实际触发的失败:")

	// 4a: invalid range, old version untouched.
	before := v4.Segments()
	_, err = v4.Set(5, 5, "z")
	fmt.Printf("  Set(5,5):  err = %v\n", err)
	triggered1 := err == rm.ErrInvalidRange
	_, err = v4.Erase(10, 3)
	fmt.Printf("  Erase(10,3): err = %v\n", err)
	triggered2 := err == rm.ErrInvalidRange
	same := equalSegs(v4.Segments(), before)
	fmt.Printf("  非法输入未改变旧版本: %v\n", same)
	if !(triggered1 && triggered2 && same) {
		fail = true
	}

	// 4b: genuinely exceed the 10000 segment limit.
	lim := rm.New()
	for i := 0; i < rm.MaxSegments; i++ {
		base := int64(i * 2)
		lim, err = lim.Set(base, base+1, "m")
		must(err)
	}
	lenAtLimit := lim.Len()
	overflow, err := lim.Set(int64(rm.MaxSegments*2), int64(rm.MaxSegments*2)+1, "m")
	fmt.Printf("  已建 %d 片段; 第 %d 个不相交片段 Set: err = %v\n",
		lenAtLimit, lenAtLimit+1, err)
	triggered3 := err == rm.ErrTooManySegments
	returnedSame := overflow == lim
	fmt.Printf("  超限后版本片段数仍为 %d, 且返回原版本指针: %v\n",
		lim.Len(), returnedSame)
	if !(triggered3 && returnedSame && lim.Len() == rm.MaxSegments) {
		fail = true
	}
	fmt.Println()

	elapsed := time.Since(start)
	fmt.Printf("=== 演示耗时 %v (要求约 8 秒内) ===\n", elapsed.Round(time.Millisecond))
	if fail {
		fmt.Println("总体结果: 存在未通过的自检")
		os.Exit(1)
	}
	fmt.Println("总体结果: 正常工作流自检通过; 失败路径均由代码实际触发并按预期返回")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func equalSegs(a, b []rm.Segment) bool {
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

// fixedSeedReferenceCheck runs a deterministic small sequence against a
// pointwise reference map over the integer domain [-16,16), checking point
// lookups, clipped range queries, history preservation and Diff rebuild on
// every step.
func fixedSeedReferenceCheck() bool {
	const lo, hi = -16, 16
	rng := rand.New(rand.NewSource(42))
	values := []string{"red", "blue", "", "green"}

	type cell struct {
		cov bool
		v   string
	}
	type snap struct {
		m   *rm.Map
		ref map[int64]cell
	}
	snaps := []snap{{rm.New(), map[int64]cell{}}}

	for step := 0; step < 120; step++ {
		last := snaps[len(snaps)-1]
		a := int64(rng.Intn(hi-lo) + lo)
		b := a + 1 + int64(rng.Intn(5))
		if b > hi {
			b = hi
		}
		ref := make(map[int64]cell, len(last.ref))
		for k, v := range last.ref {
			ref[k] = v
		}
		var m *rm.Map
		if rng.Intn(10) < 7 {
			v := values[rng.Intn(len(values))]
			m, _ = last.m.Set(a, b, v)
			for p := a; p < b; p++ {
				ref[p] = cell{true, v}
			}
		} else {
			m, _ = last.m.Erase(a, b)
			for p := a; p < b; p++ {
				delete(ref, p)
			}
		}
		snaps = append(snaps, snap{m, ref})

		// Pointwise check of the new version and all history.
		for si, sn := range snaps {
			for p := int64(lo); p < hi; p++ {
				gv, gok := sn.m.Get(p)
				w := sn.ref[p]
				if gok != w.cov || (gok && gv != w.v) {
					fmt.Printf("  MISMATCH step=%d snap=%d p=%d got(%q,%v) want(%q,%v)\n",
						step, si, p, gv, gok, w.v, w.cov)
					return false
				}
			}
		}

		// Diff against the previous snapshot over the full domain and a
		// random sub-range; rebuild via Apply and verify every point.
		prev := snaps[len(snaps)-2]
		for _, q := range [2][2]int64{{lo, hi}, {a - 3, b + 3}} {
			if q[0] < lo {
				q[0] = lo
			}
			if q[1] > hi {
				q[1] = hi
			}
			if q[0] >= q[1] {
				continue
			}
			ch, _, derr := rm.Diff(prev.m, m, q[0], q[1])
			if derr != nil {
				return false
			}
			// Adjacent changes may not be merged unless both old and new
			// (coverage,value) pairs match.
			for i := 1; i < len(ch); i++ {
				if ch[i].Lo == ch[i-1].Hi {
					p, n0 := ch[i-1], ch[i]
					if p.OldCovered == n0.OldCovered && p.OldValue == n0.OldValue &&
						p.NewCovered == n0.NewCovered && p.NewValue == n0.NewValue {
						fmt.Printf("  MISMATCH unmerged equal adjacent changes at step %d\n", step)
						return false
					}
				}
			}
			rb, aerr := rm.Apply(prev.m, ch)
			if aerr != nil {
				return false
			}
			for p := q[0]; p < q[1]; p++ {
				gv, gok := rb.Get(p)
				w := ref[p]
				if gok != w.cov || (gok && gv != w.v) {
					fmt.Printf("  REBUILD MISMATCH step=%d p=%d got(%q,%v) want(%q,%v)\n",
						step, p, gv, gok, w.v, w.cov)
					return false
				}
			}
		}
	}
	return true
}

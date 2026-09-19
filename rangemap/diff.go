package rangemap

// DiffRange is one maximal sub-interval of a Diff window on which the old
// and new versions disagree. OldCovered/NewCovered distinguish "uncovered"
// from a stored empty string; Old/New carry the value when covered.
type DiffRange struct {
	Lo, Hi     int64
	OldCovered bool
	Old        string
	NewCovered bool
	New        string
}

// Diff returns the maximal ranges inside [lo, hi) where old and new differ
// in coverage state or value. Adjacent differing pieces are kept separate
// only when their old state/value or new state/value changes; a maximal run
// is emitted as soon as either side changes.
//
// Persistence makes pointer identity a sound equality witness: when the
// traversal reaches the same *node in both versions (a shared subtree) and
// that subtree lies wholly inside the window, its covered block is known to
// be identical on both sides and is skipped wholesale (Stats.Skipped).
func Diff(oldM, newM *Map, lo, hi int64) ([]DiffRange, Stats, error) {
	if !validRange(lo, hi) {
		return nil, Stats{}, ErrInvalidRange
	}
	c := &counter{}

	var aSegs, bSegs []Fragment
	if (oldM == nil || oldM.root == nil) && (newM == nil || newM.root == nil) {
		return nil, c.stats(), nil
	}
	var ra, rb *node
	if oldM != nil {
		ra = oldM.root
	}
	if newM != nil {
		rb = newM.root
	}
	aSegs = collectPaired(c, ra, rb, lo, hi, aSegs)
	bSegs = collectPaired(c, rb, ra, lo, hi, bSegs)
	aSegs = mergeFragments(aSegs)
	bSegs = mergeFragments(bSegs)

	var out []DiffRange
	ia, ib := 0, 0
	cursor := lo

	advance := func() {
		for ia < len(aSegs) && aSegs[ia].Hi <= cursor {
			ia++
		}
		for ib < len(bSegs) && bSegs[ib].Hi <= cursor {
			ib++
		}
	}

	for cursor < hi {
		advance()
		var av, bv string
		aOK := ia < len(aSegs) && aSegs[ia].Lo <= cursor
		bOK := ib < len(bSegs) && bSegs[ib].Lo <= cursor
		if aOK {
			av = aSegs[ia].Value
		}
		if bOK {
			bv = bSegs[ib].Value
		}

		end := hi
		if aOK {
			end = min(end, aSegs[ia].Hi)
		} else if ia < len(aSegs) {
			end = min(end, aSegs[ia].Lo)
		}
		if bOK {
			end = min(end, bSegs[ib].Hi)
		} else if ib < len(bSegs) {
			end = min(end, bSegs[ib].Lo)
		}

		if aOK != bOK || av != bv {
			out = append(out, DiffRange{
				Lo:         cursor,
				Hi:         end,
				OldCovered: aOK,
				Old:        av,
				NewCovered: bOK,
				New:        bv,
			})
		}
		cursor = end
	}
	return out, c.stats(), nil
}

// collectPaired performs an in-order gather of own's segments intersecting
// [qlo, qhi), descending in parallel with the other version's tree. When
// the two nodes are the same pointer (a shared, hence identical, subtree)
// and its coordinate block lies wholly inside the window, the block is
// skipped and its nodes are counted in counter.skipped.
//
// The traversal descends own in in-order order; the parallel argument may
// have a different shape but each side's own order is preserved regardless.
func collectPaired(c *counter, own, other *node, qlo, qhi int64, out []Fragment) []Fragment {
	if own == nil {
		return out
	}
	if !subtreeMayIntersect(own, qlo, qhi) {
		return out
	}
	if own == other && own.minLo >= qlo && own.maxHi <= qhi {
		// Identical covered block on both sides, fully inside the window:
		// it can contribute no difference.
		c.skipped += int64(own.size)
		return out
	}
	c.touch(own)

	var ol, or *node
	if other != nil {
		ol, or = other.left, other.right
	}
	out = collectPaired(c, own.left, ol, qlo, qhi, out)
	if own.lo < qhi && own.hi > qlo {
		out = append(out, Fragment{
			Lo:    max(own.lo, qlo),
			Hi:    min(own.hi, qhi),
			Value: own.val,
		})
	}
	out = collectPaired(c, own.right, or, qlo, qhi, out)
	return out
}

// ApplyDiff rebuilds the part of old covered by a Diff result: Set for
// ranges newly covered (or changed to another value), Erase for ranges that
// become holes. Applying the Diff(old, new, lo, hi) result to old yields a
// map equal to new inside [lo, hi) (and unchanged outside it).
func ApplyDiff(oldM *Map, diffs []DiffRange) (*Map, Stats, error) {
	cur := oldM
	if cur == nil {
		cur = New()
	}
	var total Stats
	var err error
	for _, d := range diffs {
		var st Stats
		if d.NewCovered {
			cur, st, err = cur.Set(d.Lo, d.Hi, d.New)
		} else {
			cur, st, err = cur.Erase(d.Lo, d.Hi)
		}
		if err != nil {
			return oldM, total, err
		}
		total.Created += st.Created
		total.Visited += st.Visited
	}
	return cur, total, nil
}

// Package viewerlogic mirrors web/scripts/viewer_model.js for Go regressions.
package viewerlogic

const EstLinePx = 22
const Section = 100

type Interval struct {
	Start, End int
	Height     int
}

func Sorted(intervals []Interval) []Interval {
	out := append([]Interval(nil), intervals...)
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Start < out[i].Start {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func NextMissingFrom(intervals []Interval, from, lineCount int) int {
	if lineCount < 1 {
		return 0
	}
	line := from
	if line < 1 {
		line = 1
	}
	sorted := Sorted(intervals)
	for line <= lineCount {
		covered := false
		for _, iv := range sorted {
			if line >= iv.Start && line <= iv.End {
				line = iv.End + 1
				covered = true
				break
			}
		}
		if !covered {
			return line
		}
	}
	return 0
}

func NextAfterPartial(requestedStart, actualEnd, lineCount int) int {
	if actualEnd < requestedStart {
		return 0
	}
	next := actualEnd + 1
	if next > lineCount {
		return 0
	}
	return next
}

type PrefetchGeom struct {
	ViewportTop, ViewportBottom int
	ContentTop, ContentBottom   int
	PrefetchPx                  int
	Intervals                   []Interval
	LineCount                   int
}

func PrefetchTargets(g PrefetchGeom) []int {
	var out []int
	sorted := Sorted(g.Intervals)
	if len(sorted) == 0 || g.LineCount < 1 {
		return out
	}
	extentLast := sorted[len(sorted)-1].End
	nearBottom := g.ViewportBottom >= g.ContentBottom-g.PrefetchPx &&
		g.ViewportBottom <= g.ContentBottom+g.PrefetchPx*2
	if nearBottom {
		if next := NextMissingFrom(g.Intervals, extentLast+1, g.LineCount); next > 0 {
			out = append(out, next)
		}
	}
	return out
}

func LineAtScrollOffset(scrollTop int, intervals []Interval, lineCount, viewportCenterOffset int) int {
	y := scrollTop + viewportCenterOffset
	if y < 0 {
		y = 0
	}
	sorted := Sorted(intervals)
	pos := 0
	firstLoaded := lineCount + 1
	if len(sorted) > 0 {
		firstLoaded = sorted[0].Start
	}
	if firstLoaded > 1 {
		topH := (firstLoaded - 1) * EstLinePx
		if y < pos+topH {
			idx := (y - pos) / EstLinePx
			line := 1 + idx
			if line < 1 {
				line = 1
			}
			if line > lineCount {
				line = lineCount
			}
			return line
		}
		pos += topH
	}
	line := firstLoaded
	for _, iv := range sorted {
		if iv.Start > line {
			gapH := (iv.Start - line) * EstLinePx
			if y < pos+gapH {
				idx := (y - pos) / EstLinePx
				out := line + idx
				if out > lineCount {
					out = lineCount
				}
				return out
			}
			pos += gapH
		}
		h := iv.Height
		if h <= 0 {
			h = (iv.End - iv.Start + 1) * EstLinePx
		}
		if y < pos+h {
			span := iv.End - iv.Start
			frac := float64(y-pos) / float64(h)
			out := iv.Start + int(frac*float64(span+1))
			if out < iv.Start {
				out = iv.Start
			}
			if out > iv.End {
				out = iv.End
			}
			return out
		}
		pos += h
		line = iv.End + 1
	}
	if line <= lineCount {
		bottomH := (lineCount - line + 1) * EstLinePx
		if y < pos+bottomH {
			idx := (y - pos) / EstLinePx
			out := line + idx
			if out > lineCount {
				out = lineCount
			}
			return out
		}
		return lineCount
	}
	if len(sorted) > 0 {
		return sorted[len(sorted)-1].End
	}
	return 1
}

func RequestWindow(start, lineCount int) (int, int) {
	if start < 1 {
		start = 1
	}
	if lineCount > 0 && start > lineCount {
		start = lineCount
	}
	end := start + Section - 1
	if lineCount > 0 && end > lineCount {
		end = lineCount
	}
	return start, end
}

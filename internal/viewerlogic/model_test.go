package viewerlogic

import "testing"

func TestPrefetchAtLoadedBoundaryRequests201(t *testing.T) {
	intervals := []Interval{{Start: 1, End: 100, Height: 2200}, {Start: 101, End: 200, Height: 2200}}
	got := PrefetchTargets(PrefetchGeom{
		ViewportTop: 4000, ViewportBottom: 4300,
		ContentTop: 0, ContentBottom: 4400,
		PrefetchPx: 360, Intervals: intervals, LineCount: 16988,
	})
	found := false
	for _, n := range got {
		if n == 201 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 201 at loaded boundary, got %v", got)
	}
}

func TestThumbNearEOFMapsPast16000(t *testing.T) {
	intervals := []Interval{{Start: 1, End: 100, Height: 2200}, {Start: 101, End: 200, Height: 2200}}
	lineCount := 16988
	scrollTop := (lineCount - 40) * EstLinePx
	target := LineAtScrollOffset(scrollTop, intervals, lineCount, 200)
	if target <= 16000 {
		t.Fatalf("thumb near EOF mapped to %d", target)
	}
	start, _ := RequestWindow(target, lineCount)
	if start <= 16000 {
		t.Fatalf("request window start %d not near EOF", start)
	}
}

func TestByteLimitedContinuesAt4(t *testing.T) {
	if NextAfterPartial(1, 3, 16988) != 4 {
		t.Fatal("nextAfterPartial")
	}
	if NextMissingFrom([]Interval{{Start: 1, End: 3}}, 1, 16988) != 4 {
		t.Fatal("nextMissingFrom")
	}
}

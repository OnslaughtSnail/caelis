package tuiapp

import "testing"

func TestRenderedRevealRowsStable_RejectsPrefixOnlyWrappedRows(t *testing.T) {
	pending := splitGraphemeClusters("- 💻 文件操作说明还在继续")
	base := chooseRevealClusterCount(pending, 5, 5)
	if base != 5 {
		t.Fatalf("expected baseline reveal count 5, got %d", base)
	}

	before := renderStreamNarrativePlainRows("", "answer", "", 8)
	after := renderStreamNarrativePlainRows(joinGraphemeClusters(pending[:base]), "answer", "", 8)
	if renderedRevealRowsStable(before, after, base, len(pending), "answer", "", false) {
		t.Fatalf("expected prefix-only wrapped rows to remain classified as unstable, before=%q after=%q", before, after)
	}
}

func TestRenderedRevealRowsStable_RequiresCompletedUpstreamForTinyTail(t *testing.T) {
	before := renderStreamNarrativePlainRows("", "answer", "", 8)
	after := renderStreamNarrativePlainRows("hello", "answer", "", 8)

	if renderedRevealRowsStable(before, after, 5, 5, "answer", "", false) {
		t.Fatalf("expected tiny tail to stay unstable while upstream is still active, before=%q after=%q", before, after)
	}
	if !renderedRevealRowsStable(before, after, 5, 5, "answer", "", true) {
		t.Fatalf("expected tiny tail to be allowed after upstream completes, before=%q after=%q", before, after)
	}
}

func TestExtendRevealToStableRenderedRows_AvoidsTinySoftWrappedTail(t *testing.T) {
	pending := splitGraphemeClusters("🙂中文说明还在继续")
	base := chooseRevealClusterCount(pending, 4, 4)
	if base != 4 {
		t.Fatalf("expected baseline reveal count 4, got %d", base)
	}

	got := extendRevealToStableRenderedRows("abcd", pending, base, 7, 6, "answer", "", false)
	if got <= base {
		t.Fatalf("expected reveal to extend after opening a new wrapped row, baseline=%d got=%d", base, got)
	}

	before := renderStreamNarrativePlainRows("abcd", "answer", "", 6)
	after := renderStreamNarrativePlainRows("abcd"+joinGraphemeClusters(pending[:got]), "answer", "", 6)
	if !renderedRevealRowsStable(before, after, got, len(pending), "answer", "", false) {
		t.Fatalf("expected extended reveal to stabilize wrapped tail, before=%q after=%q", before, after)
	}
}

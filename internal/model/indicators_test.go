package model

import "testing"

func TestNormalizeIndicators(t *testing.T) {
	// Nothing stored means an install that predates indicators, so it gets
	// the defaults rather than a bare header.
	if got := NormalizeIndicators(nil); len(got) != len(DefaultIndicators()) {
		t.Fatalf("an empty list should seed the defaults, got %+v", got)
	}
	got := NormalizeIndicators([]IndicatorRule{
		{Name: "  Busy  ", Colour: IndicatorRed, Condition: IndicatorCondition{Kind: IndicatorNodesInStatus, Status: "down"}},
		{ID: "dup", Name: "", Colour: IndicatorYellow, Condition: IndicatorCondition{Kind: IndicatorServiceHealth, Status: "down", MinCount: 9}},
		{ID: "dup", Name: "Clash", Colour: IndicatorOrange, Condition: IndicatorCondition{Kind: IndicatorAttention, MinCount: 3}},
	})
	if got[0].ID == "" || got[0].Name != "Busy" || got[0].Condition.MinCount != 1 {
		t.Errorf("a missing id, stray spaces and a zero count should all be fixed: %+v", got[0])
	}
	if got[1].Name != "Indicator" || got[1].Condition.Status != "" || got[1].Condition.MinCount != 1 {
		t.Errorf("serviceHealth takes no status and no count: %+v", got[1])
	}
	if got[2].ID == got[1].ID {
		t.Errorf("a duplicate id should be replaced: %+v", got)
	}
	if got[2].Condition.MinCount != 3 {
		t.Errorf("a stated count should survive: %+v", got[2])
	}
}

func TestValidateIndicators(t *testing.T) {
	if err := ValidateIndicators(DefaultIndicators()); err != nil {
		t.Fatalf("the defaults must validate: %v", err)
	}
	for _, bad := range []IndicatorRule{
		{Name: "Green", Colour: "green", Condition: IndicatorCondition{Kind: IndicatorServiceHealth}},
		{Name: "Nonsense", Colour: IndicatorRed, Condition: IndicatorCondition{Kind: "weather"}},
		{Name: "All well", Colour: IndicatorRed, Condition: IndicatorCondition{Kind: IndicatorNodesInStatus, Status: "up"}},
		{Name: "Negative", Colour: IndicatorRed, Condition: IndicatorCondition{Kind: IndicatorAttention, MinCount: -1}},
	} {
		if err := ValidateIndicators([]IndicatorRule{bad}); err == nil {
			t.Errorf("%+v should not be accepted", bad)
		}
	}
}

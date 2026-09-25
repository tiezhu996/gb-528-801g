package interlock

import (
	"reflect"
	"testing"

	"stage-rigging-cue-interlock/backend/internal/constants"
)

func TestEvaluateDeterministicBlockerEvidence(t *testing.T) {
	cues := []CueInput{{GraphCue: GraphCue{ID: 1, CueCode: "Q-1", SequenceNo: 1, DurationMS: 2000}, Version: 4, Actions: []ActionInput{{DeviceID: 1, DurationMS: 2000, FromPositionM: 10, ToPositionM: 8, LoadKG: 650}}}}
	devices := []DeviceInput{{ID: 1, DeviceCode: "D-1", Name: "One", MaxLoadKG: 600, MaxSpeedMS: 2, TravelMinM: 5, TravelMaxM: 15, SafetyZone: "zone-a", DeviceStatus: "available"}}
	rules := []RuleInput{{ID: 1, RuleCode: "LOAD-1", RuleType: "load_limit", DeviceIDs: []uint{1}, Threshold: RuleThreshold{UseDeviceLimits: true}, Severity: "blocker", Enabled: true, RuleVersion: 1}}
	first, err := Evaluate(cues, devices, rules, 100)
	if err != nil {
		t.Fatalf("first Evaluate returned error: %v", err)
	}
	second, err := Evaluate(cues, devices, rules, 100)
	if err != nil {
		t.Fatalf("second Evaluate returned error: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("fixed input produced different evaluations:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if first.HighestSeverity != constants.ResultBlocker || len(first.RuleResults) != 1 {
		t.Fatalf("unexpected evaluation: %#v", first)
	}
	result := first.RuleResults[0]
	if result.RuleCode != "LOAD-1" || result.ActualValue != 650 || result.ThresholdValue != 600 || len(result.CueCodes) != 1 || len(result.DeviceCodes) != 1 {
		t.Fatalf("incomplete blocker evidence: %#v", result)
	}
}

func TestEvaluateDependencyGap(t *testing.T) {
	zero := float64(0)
	cues := []CueInput{
		{GraphCue: GraphCue{ID: 1, CueCode: "Q-1", SequenceNo: 1, DurationMS: 1000}, Version: 4, Actions: []ActionInput{{DeviceID: 1, DurationMS: 1000}}},
		{GraphCue: GraphCue{ID: 2, CueCode: "Q-2", SequenceNo: 2, StartOffsetMS: 900, DurationMS: 1000, DependencyIDs: []uint{1}}, Version: 4, Actions: []ActionInput{{DeviceID: 2, DurationMS: 1000}}},
	}
	devices := []DeviceInput{{ID: 1, DeviceCode: "D-1", DeviceStatus: "available", SafetyZone: "a"}, {ID: 2, DeviceCode: "D-2", DeviceStatus: "available", SafetyZone: "b"}}
	rules := []RuleInput{{ID: 1, RuleCode: "DEP-1", RuleType: "dependency_guard", Threshold: RuleThreshold{MinimumGapMS: &zero}, Severity: "blocker", Enabled: true, RuleVersion: 1}}
	evaluation, err := Evaluate(cues, devices, rules, 100)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if evaluation.HighestSeverity != constants.ResultBlocker || evaluation.RuleResults[0].ActualValue != -100 {
		t.Fatalf("expected -100ms dependency blocker, got %#v", evaluation.RuleResults)
	}
}

func sameDeviceCues(firstTo, secondFrom float64, secondStart int64) []CueInput {
	return []CueInput{
		{GraphCue: GraphCue{ID: 1, CueCode: "Q-1", SequenceNo: 1, DurationMS: 1000}, Version: 1, Actions: []ActionInput{{DeviceID: 1, DurationMS: 1000, FromPositionM: 10, ToPositionM: firstTo}}},
		{GraphCue: GraphCue{ID: 2, CueCode: "Q-2", SequenceNo: 2, StartOffsetMS: secondStart, DurationMS: 1000}, Version: 1, Actions: []ActionInput{{DeviceID: 1, StartOffsetMS: 0, DurationMS: 1000, FromPositionM: secondFrom, ToPositionM: 4}}},
	}
}

func TestSameDeviceOverlappingCuesIsInvalidEvenWithoutRules(t *testing.T) {
	devices := []DeviceInput{{ID: 1, DeviceCode: "D-1", DeviceStatus: "available", SafetyZone: "zone-a"}}
	first, err := Evaluate(sameDeviceCues(8, 8, 500), devices, nil, 100)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if first.HighestSeverity != constants.ResultInvalid {
		t.Fatalf("expected invalid highest severity, got %s (%#v)", first.HighestSeverity, first.RuleResults)
	}
	result := first.RuleResults[0]
	if result.RuleCode != deviceContentionCode || result.Result != constants.ResultInvalid {
		t.Fatalf("expected device contention invalid evidence, got %#v", result)
	}
	if result.WindowStartMS != 500 || result.WindowEndMS != 1000 || result.ActualValue != 500 || result.Unit != "ms overlap" {
		t.Fatalf("unexpected overlap window evidence: %#v", result)
	}
	if len(result.CueCodes) != 2 || result.CueCodes[0] != "Q-1" || result.CueCodes[1] != "Q-2" {
		t.Fatalf("evidence must name both contending cues: %#v", result.CueCodes)
	}
	if len(result.DeviceCodes) != 1 || result.DeviceCodes[0] != "D-1" {
		t.Fatalf("evidence must name the shared device: %#v", result.DeviceCodes)
	}
	second, err := Evaluate(sameDeviceCues(8, 8, 500), devices, nil, 100)
	if err != nil {
		t.Fatalf("second Evaluate returned error: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("same snapshot must produce identical device contention evidence")
	}
}

func TestSameDeviceTouchingRelayMatchesPosition(t *testing.T) {
	devices := []DeviceInput{{ID: 1, DeviceCode: "D-1", DeviceStatus: "available", SafetyZone: "zone-a"}}
	evaluation, err := Evaluate(sameDeviceCues(8, 8, 1000), devices, nil, 100)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if evaluation.HighestSeverity != constants.ResultPass {
		t.Fatalf("an end-to-start relay with matching positions must pass, got %s (%#v)", evaluation.HighestSeverity, evaluation.RuleResults)
	}
}

func TestSameDeviceTouchingRelayPositionMismatchIsBlocker(t *testing.T) {
	devices := []DeviceInput{{ID: 1, DeviceCode: "D-1", DeviceStatus: "available", SafetyZone: "zone-a"}}
	evaluation, err := Evaluate(sameDeviceCues(8, 6, 1000), devices, nil, 100)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if evaluation.HighestSeverity != constants.ResultBlocker {
		t.Fatalf("expected handoff mismatch blocker, got %s (%#v)", evaluation.HighestSeverity, evaluation.RuleResults)
	}
	result := evaluation.RuleResults[0]
	if result.RuleCode != deviceHandoffCode || result.Result != constants.ResultBlocker || result.ActualValue != 2 || result.Unit != "m gap" {
		t.Fatalf("expected handoff position gap blocker, got %#v", result)
	}
	if result.WindowStartMS != 1000 || result.WindowEndMS != 1000 {
		t.Fatalf("handoff evidence window must sit on the relay instant: %#v", result)
	}
}

func TestSameDeviceSeparatedByGapHasNoContinuityObligation(t *testing.T) {
	devices := []DeviceInput{{ID: 1, DeviceCode: "D-1", DeviceStatus: "available", SafetyZone: "zone-a"}}
	// First ends at 1000/position 8, second starts at 1500/position 3: idle
	// time in between means the device can be re-rigged, so no handoff claim.
	evaluation, err := Evaluate(sameDeviceCues(8, 3, 1500), devices, nil, 100)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if evaluation.HighestSeverity != constants.ResultPass {
		t.Fatalf("separated windows must not raise handoff evidence, got %#v", evaluation.RuleResults)
	}
}

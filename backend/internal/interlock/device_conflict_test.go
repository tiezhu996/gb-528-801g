package interlock

import (
	"reflect"
	"testing"

	"stage-rigging-cue-interlock/backend/internal/constants"
)

func sameDeviceCueSet(secondStartMS int64, secondFromM float64) ([]CueInput, []DeviceInput) {
	cues := []CueInput{
		{GraphCue: GraphCue{ID: 1, CueCode: "Q-1", SequenceNo: 1, DurationMS: 2000}, Version: 4, Actions: []ActionInput{{DeviceID: 1, DurationMS: 2000, FromPositionM: 10, ToPositionM: 8, LoadKG: 100}}},
		{GraphCue: GraphCue{ID: 2, CueCode: "Q-2", SequenceNo: 2, StartOffsetMS: secondStartMS, DurationMS: 1000}, Version: 4, Actions: []ActionInput{{DeviceID: 1, DurationMS: 1000, FromPositionM: secondFromM, ToPositionM: 9, LoadKG: 100}}},
	}
	devices := []DeviceInput{{ID: 1, DeviceCode: "D-1", Name: "One", SafetyZone: "zone-a", DeviceStatus: "available"}}
	return cues, devices
}

func TestSameDeviceOverlapProducesInvalidEvidence(t *testing.T) {
	cues, devices := sameDeviceCueSet(500, 8)
	first, err := Evaluate(cues, devices, nil, 100)
	if err != nil {
		t.Fatalf("first Evaluate returned error: %v", err)
	}
	second, err := Evaluate(cues, devices, nil, 100)
	if err != nil {
		t.Fatalf("second Evaluate returned error: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("fixed input produced different evaluations:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if first.HighestSeverity != constants.ResultInvalid {
		t.Fatalf("same-device overlap must reach invalid severity, got %s", first.HighestSeverity)
	}
	if len(first.RuleResults) != 1 {
		t.Fatalf("expected exactly one conflict evidence, got %#v", first.RuleResults)
	}
	result := first.RuleResults[0]
	if result.RuleCode != "DEVICE-OVERLAP" || result.RuleType != "device_overlap" || result.Result != constants.ResultInvalid {
		t.Fatalf("unexpected overlap evidence identity: %#v", result)
	}
	if result.WindowStartMS != 500 || result.WindowEndMS != 1500 || result.ActualValue != 1000 || result.ThresholdValue != 0 {
		t.Fatalf("overlap evidence must carry the shared window, got %#v", result)
	}
	if !reflect.DeepEqual(result.CueCodes, []string{"Q-1", "Q-2"}) || !reflect.DeepEqual(result.DeviceCodes, []string{"D-1"}) {
		t.Fatalf("overlap evidence must name both cues and the device, got %#v", result)
	}
	if len(first.CollisionWindows) != 0 {
		t.Fatalf("same-device pairs must not open safety-zone collision windows, got %#v", first.CollisionWindows)
	}
}

func TestSameDeviceHandoffGapProducesBlockerEvidence(t *testing.T) {
	cues, devices := sameDeviceCueSet(2000, 8.5)
	evaluation, err := Evaluate(cues, devices, nil, 100)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if evaluation.HighestSeverity != constants.ResultBlocker {
		t.Fatalf("handoff position gap must reach blocker severity, got %s", evaluation.HighestSeverity)
	}
	if len(evaluation.RuleResults) != 1 {
		t.Fatalf("expected exactly one handoff evidence, got %#v", evaluation.RuleResults)
	}
	result := evaluation.RuleResults[0]
	if result.RuleCode != "DEVICE-HANDOFF" || result.RuleType != "device_handoff" || result.Result != constants.ResultBlocker {
		t.Fatalf("unexpected handoff evidence identity: %#v", result)
	}
	if result.WindowStartMS != 2000 || result.WindowEndMS != 2000 || result.ActualValue != 0.5 || result.ThresholdValue != 0 {
		t.Fatalf("handoff evidence must carry the junction instant and position gap, got %#v", result)
	}
}

func TestSameDeviceCleanHandoffProducesNoConflictEvidence(t *testing.T) {
	cues, devices := sameDeviceCueSet(2000, 8)
	evaluation, err := Evaluate(cues, devices, nil, 100)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if evaluation.HighestSeverity != constants.ResultPass {
		t.Fatalf("matching handoff positions must pass, got %s", evaluation.HighestSeverity)
	}
	for _, result := range evaluation.RuleResults {
		if result.RuleType == "device_overlap" || result.RuleType == "device_handoff" {
			t.Fatalf("clean handoff must not raise device conflict evidence, got %#v", result)
		}
	}
}

func TestSameDeviceIdleGapSkipsHandoffCheck(t *testing.T) {
	cues, devices := sameDeviceCueSet(2500, 9)
	evaluation, err := Evaluate(cues, devices, nil, 100)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if evaluation.HighestSeverity != constants.ResultPass {
		t.Fatalf("idle gap without a junction must not raise handoff evidence, got %s", evaluation.HighestSeverity)
	}
}

func TestSameDeviceChainFlagsOnlyTheBrokenHandoff(t *testing.T) {
	cues := []CueInput{
		{GraphCue: GraphCue{ID: 1, CueCode: "Q-1", SequenceNo: 1, DurationMS: 1000}, Version: 4, Actions: []ActionInput{{DeviceID: 1, DurationMS: 1000, FromPositionM: 10, ToPositionM: 8}}},
		{GraphCue: GraphCue{ID: 2, CueCode: "Q-2", SequenceNo: 2, StartOffsetMS: 1000, DurationMS: 1000}, Version: 4, Actions: []ActionInput{{DeviceID: 1, DurationMS: 1000, FromPositionM: 8, ToPositionM: 9}}},
		{GraphCue: GraphCue{ID: 3, CueCode: "Q-3", SequenceNo: 3, StartOffsetMS: 2000, DurationMS: 1000}, Version: 4, Actions: []ActionInput{{DeviceID: 1, DurationMS: 1000, FromPositionM: 9.5, ToPositionM: 9}}},
	}
	devices := []DeviceInput{{ID: 1, DeviceCode: "D-1", Name: "One", SafetyZone: "zone-a", DeviceStatus: "available"}}
	evaluation, err := Evaluate(cues, devices, nil, 100)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if len(evaluation.RuleResults) != 1 {
		t.Fatalf("expected exactly one broken handoff in the chain, got %#v", evaluation.RuleResults)
	}
	result := evaluation.RuleResults[0]
	if result.RuleCode != "DEVICE-HANDOFF" || result.WindowStartMS != 2000 || result.ActualValue != 0.5 {
		t.Fatalf("chain must flag only the Q-2 to Q-3 junction, got %#v", result)
	}
	if !reflect.DeepEqual(result.CueCodes, []string{"Q-2", "Q-3"}) {
		t.Fatalf("broken handoff must name the adjacent cues, got %#v", result)
	}
}

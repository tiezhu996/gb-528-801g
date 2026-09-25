package interlock

import (
	"fmt"
	"math"
	"sort"
)

type ActionInput struct {
	DeviceID      uint    `json:"device_id"`
	StartOffsetMS int64   `json:"start_offset_ms"`
	DurationMS    int64   `json:"duration_ms"`
	FromPositionM float64 `json:"from_position_m"`
	ToPositionM   float64 `json:"to_position_m"`
	LoadKG        float64 `json:"load_kg"`
}

type CueInput struct {
	GraphCue
	Version uint          `json:"version"`
	Actions []ActionInput `json:"actions"`
}

type DeviceInput struct {
	ID           uint    `json:"id"`
	DeviceCode   string  `json:"device_code"`
	Name         string  `json:"name"`
	MaxLoadKG    float64 `json:"max_load_kg"`
	MaxSpeedMS   float64 `json:"max_speed_ms"`
	TravelMinM   float64 `json:"travel_min_m"`
	TravelMaxM   float64 `json:"travel_max_m"`
	SafetyZone   string  `json:"safety_zone"`
	DeviceStatus string  `json:"device_status"`
}

type RuleThreshold struct {
	MaxLoadKG       *float64 `json:"max_load_kg,omitempty"`
	MaxSpeedMS      *float64 `json:"max_speed_ms,omitempty"`
	MinPositionM    *float64 `json:"min_position_m,omitempty"`
	MaxPositionM    *float64 `json:"max_position_m,omitempty"`
	MinimumGapMS    *float64 `json:"minimum_gap_ms,omitempty"`
	UseDeviceLimits bool     `json:"use_device_limits,omitempty"`
}

type RuleInput struct {
	ID          uint          `json:"id"`
	RuleCode    string        `json:"rule_code"`
	RuleType    string        `json:"rule_type"`
	DeviceIDs   []uint        `json:"device_ids"`
	Threshold   RuleThreshold `json:"threshold"`
	Severity    string        `json:"severity"`
	Enabled     bool          `json:"enabled"`
	RuleVersion uint          `json:"rule_version"`
	Explanation string        `json:"explanation"`
}

type TimelineEvent struct {
	CueID         uint    `json:"cue_id"`
	CueCode       string  `json:"cue_code"`
	CueSequence   int     `json:"cue_sequence"`
	CueVersion    uint    `json:"cue_version"`
	DeviceID      uint    `json:"device_id"`
	DeviceCode    string  `json:"device_code"`
	DeviceName    string  `json:"device_name"`
	SafetyZone    string  `json:"safety_zone"`
	StartMS       int64   `json:"start_ms"`
	EndMS         int64   `json:"end_ms"`
	FromPositionM float64 `json:"from_position_m"`
	ToPositionM   float64 `json:"to_position_m"`
	LoadKG        float64 `json:"load_kg"`
	SpeedMS       float64 `json:"speed_ms"`
}

type CollisionWindow struct {
	SafetyZone  string   `json:"safety_zone"`
	CueCodes    []string `json:"cue_codes"`
	DeviceIDs   []uint   `json:"device_ids"`
	DeviceCodes []string `json:"device_codes"`
	StartMS     int64    `json:"start_ms"`
	EndMS       int64    `json:"end_ms"`
}

func ExpandTimeline(ordered []GraphCue, cues map[uint]CueInput, devices map[uint]DeviceInput) ([]TimelineEvent, error) {
	events := make([]TimelineEvent, 0)
	for _, graphCue := range ordered {
		cue, ok := cues[graphCue.ID]
		if !ok {
			return nil, fmt.Errorf("cue %s is missing its action input", graphCue.CueCode)
		}
		if len(cue.Actions) == 0 {
			return nil, fmt.Errorf("cue %s has no actions", graphCue.CueCode)
		}
		seenDevices := map[uint]bool{}
		for _, action := range cue.Actions {
			device, exists := devices[action.DeviceID]
			if !exists {
				return nil, fmt.Errorf("cue %s references missing device id %d", cue.CueCode, action.DeviceID)
			}
			if device.DeviceStatus != "available" {
				return nil, fmt.Errorf("cue %s references device %s in status %s", cue.CueCode, device.DeviceCode, device.DeviceStatus)
			}
			if seenDevices[action.DeviceID] {
				return nil, fmt.Errorf("cue %s contains duplicate action for device %s", cue.CueCode, device.DeviceCode)
			}
			seenDevices[action.DeviceID] = true
			if action.StartOffsetMS < 0 || action.DurationMS <= 0 || action.StartOffsetMS+action.DurationMS > cue.DurationMS {
				return nil, fmt.Errorf("cue %s action for %s exceeds the cue time envelope", cue.CueCode, device.DeviceCode)
			}
			if action.LoadKG < 0 {
				return nil, fmt.Errorf("cue %s action for %s has negative load", cue.CueCode, device.DeviceCode)
			}
			start := cue.StartOffsetMS + action.StartOffsetMS
			end := start + action.DurationMS
			events = append(events, TimelineEvent{CueID: cue.ID, CueCode: cue.CueCode, CueSequence: cue.SequenceNo, CueVersion: cue.Version, DeviceID: device.ID, DeviceCode: device.DeviceCode, DeviceName: device.Name, SafetyZone: device.SafetyZone, StartMS: start, EndMS: end, FromPositionM: action.FromPositionM, ToPositionM: action.ToPositionM, LoadKG: action.LoadKG, SpeedMS: ActionSpeed(action)})
		}
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].StartMS == events[j].StartMS {
			if events[i].CueSequence == events[j].CueSequence {
				return events[i].DeviceCode < events[j].DeviceCode
			}
			return events[i].CueSequence < events[j].CueSequence
		}
		return events[i].StartMS < events[j].StartMS
	})
	return events, nil
}

func DetectCollisionWindows(events []TimelineEvent) []CollisionWindow {
	windows := make([]CollisionWindow, 0)
	for i := 0; i < len(events); i++ {
		for j := i + 1; j < len(events); j++ {
			left, right := events[i], events[j]
			if left.DeviceID == right.DeviceID || left.SafetyZone == "" || left.SafetyZone != right.SafetyZone {
				continue
			}
			start, end, overlaps := OverlapWindow(left.StartMS, left.EndMS, right.StartMS, right.EndMS)
			if overlaps {
				windows = append(windows, CollisionWindow{SafetyZone: left.SafetyZone, CueCodes: []string{left.CueCode, right.CueCode}, DeviceIDs: []uint{left.DeviceID, right.DeviceID}, DeviceCodes: []string{left.DeviceCode, right.DeviceCode}, StartMS: start, EndMS: end})
			}
		}
	}
	return windows
}

// DeviceConflictKind identifies how one physical device is contended by two
// actions that belong to different Cues.
type DeviceConflictKind string

const (
	// DeviceConflictOverlap means two Cues drive the same device at the same
	// time: the half-open action windows share at least one millisecond.
	DeviceConflictOverlap DeviceConflictKind = "overlap"
	// DeviceConflictHandoffMismatch means two Cues hand the device off with
	// windows touching end-to-start, but the predecessor end position does not
	// match the successor start position.
	DeviceConflictHandoffMismatch DeviceConflictKind = "handoff_mismatch"
)

// positionToleranceM is the maximum modeled endpoint gap treated as the same
// physical handoff position.
const positionToleranceM = 1e-9

// DeviceConflict is structural evidence that one device is contended by two
// different Cues. It is independent of the configurable rule set because a
// single hoist cannot follow two motion programs simultaneously.
type DeviceConflict struct {
	Kind       DeviceConflictKind
	DeviceID   uint
	DeviceCode string
	First      TimelineEvent
	Second     TimelineEvent
	StartMS    int64
	EndMS      int64
	// ActualValue is the overlap duration in milliseconds, or the absolute handoff
	// position gap in meters, depending on Kind.
	ActualValue float64
}

// DetectDeviceConflicts scans timeline events for the same physical device
// being driven by two different Cues. Overlapping action windows are an
// invalid double-drive; windows that only touch end-to-start are a legal
// relay only when the predecessor's end position matches the successor's
// start position. A gap between two windows is neither: the device is idle in
// between, so no continuity obligation applies.
func DetectDeviceConflicts(events []TimelineEvent) []DeviceConflict {
	byDevice := make(map[uint][]TimelineEvent)
	for _, event := range events {
		byDevice[event.DeviceID] = append(byDevice[event.DeviceID], event)
	}
	deviceIDs := make([]uint, 0, len(byDevice))
	for deviceID := range byDevice {
		deviceIDs = append(deviceIDs, deviceID)
	}
	sort.Slice(deviceIDs, func(i, j int) bool { return deviceIDs[i] < deviceIDs[j] })
	conflicts := make([]DeviceConflict, 0)
	for _, deviceID := range deviceIDs {
		group := append([]TimelineEvent(nil), byDevice[deviceID]...)
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].StartMS != group[j].StartMS {
				return group[i].StartMS < group[j].StartMS
			}
			if group[i].EndMS != group[j].EndMS {
				return group[i].EndMS < group[j].EndMS
			}
			if group[i].CueSequence != group[j].CueSequence {
				return group[i].CueSequence < group[j].CueSequence
			}
			return group[i].CueCode < group[j].CueCode
		})
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				left, right := group[i], group[j]
				if left.CueID == right.CueID {
					continue
				}
				if start, end, overlaps := OverlapWindow(left.StartMS, left.EndMS, right.StartMS, right.EndMS); overlaps {
					conflicts = append(conflicts, DeviceConflict{Kind: DeviceConflictOverlap, DeviceID: deviceID, DeviceCode: left.DeviceCode, First: left, Second: right, StartMS: start, EndMS: end, ActualValue: float64(end - start)})
					continue
				}
				if left.EndMS == right.StartMS {
					gap := math.Abs(left.ToPositionM - right.FromPositionM)
					if gap > positionToleranceM {
						conflicts = append(conflicts, DeviceConflict{Kind: DeviceConflictHandoffMismatch, DeviceID: deviceID, DeviceCode: left.DeviceCode, First: left, Second: right, StartMS: left.EndMS, EndMS: right.StartMS, ActualValue: gap})
					}
				}
			}
		}
	}
	return conflicts
}

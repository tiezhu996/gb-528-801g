package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"stage-rigging-cue-interlock/backend/internal/audit"
	"stage-rigging-cue-interlock/backend/internal/constants"
	"stage-rigging-cue-interlock/backend/internal/dto"
	"stage-rigging-cue-interlock/backend/internal/model"
	"stage-rigging-cue-interlock/backend/internal/repository"
	"stage-rigging-cue-interlock/backend/internal/util"

	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var contentionDBCounter atomic.Uint64

type actionSnapshot struct {
	DeviceID      uint    `json:"device_id"`
	StartOffsetMS int64   `json:"start_offset_ms"`
	DurationMS    int64   `json:"duration_ms"`
	FromPositionM float64 `json:"from_position_m"`
	ToPositionM   float64 `json:"to_position_m"`
	LoadKG        float64 `json:"load_kg"`
}

func newContentionService(t *testing.T) (*RehearsalRunService, *gorm.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:device-contention-test-%d?mode=memory&cache=shared", contentionDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.RiggingDevice{}, &model.CueDefinition{}, &model.InterlockRule{}, &model.RehearsalRun{}, &audit.Event{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	auditRepository := audit.NewRepository(db)
	deviceRepository := repository.NewRiggingDeviceRepository(db, auditRepository)
	cueRepository := repository.NewCueDefinitionRepository(db, auditRepository)
	ruleRepository := repository.NewInterlockRuleRepository(db, auditRepository)
	runRepository := repository.NewRehearsalRunRepository(db, auditRepository)
	service := NewRehearsalRunService(runRepository, cueRepository, deviceRepository, ruleRepository, 100, 40)
	return service, db
}

func seedContendedCues(t *testing.T, db *gorm.DB, secondCueStart int64, secondFrom float64) []uint {
	t.Helper()
	device := model.RiggingDevice{DeviceCode: "D-1", Name: "Shared hoist", DeviceType: "motorized_batten", MaxLoadKG: 1000, MaxSpeedMS: 2, TravelMinM: 0, TravelMaxM: 20, SafetyZone: "zone-a", DeviceStatus: "available", Version: 1}
	if err := db.Create(&device).Error; err != nil {
		t.Fatalf("create device: %v", err)
	}
	firstActions, _ := json.Marshal([]actionSnapshot{{DeviceID: device.ID, DurationMS: 1000, FromPositionM: 10, ToPositionM: 8, LoadKG: 100}})
	secondActions, _ := json.Marshal([]actionSnapshot{{DeviceID: device.ID, DurationMS: 1000, FromPositionM: secondFrom, ToPositionM: 4, LoadKG: 100}})
	cues := []model.CueDefinition{
		{CueCode: "Q-1", Name: "First drive", SequenceNo: 1, DurationMS: 1000, CueStatus: string(constants.CueLocked), Version: 1, CreatedBy: 1, ActionsJSON: datatypes.JSON(firstActions), DependenciesJSON: datatypes.JSON("[]")},
		{CueCode: "Q-2", Name: "Second drive", SequenceNo: 2, StartOffsetMS: secondCueStart, DurationMS: 1000, CueStatus: string(constants.CueLocked), Version: 1, CreatedBy: 1, ActionsJSON: datatypes.JSON(secondActions), DependenciesJSON: datatypes.JSON("[]")},
	}
	ids := make([]uint, 0, len(cues))
	for _, cue := range cues {
		if err := db.Create(&cue).Error; err != nil {
			t.Fatalf("create cue: %v", err)
		}
		ids = append(ids, cue.ID)
	}
	return ids
}

func appErrorCode(t *testing.T, err error) string {
	t.Helper()
	var appErr *util.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected an application error, got %T: %v", err, err)
	}
	return appErr.Code
}

func TestOverlappingSameDeviceRunBlockedFromSubmitAndApprove(t *testing.T) {
	service, db := newContentionService(t)
	ids := seedContendedCues(t, db, 500, 8)
	actor := audit.ActorContext{ID: 1, Username: "programmer"}
	run, err := service.Run(dto.RunRehearsalRequest{CueIDs: ids}, actor)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if run.RunStatus != constants.RunBlocked || run.HighestSeverity != constants.ResultInvalid {
		t.Fatalf("overlapping same-device cues must block as invalid, got status=%s severity=%s", run.RunStatus, run.HighestSeverity)
	}
	if _, err := service.Submit(run.ID, dto.RunTransitionRequest{Version: run.Version, Reason: "attempting to submit blocked evidence"}, actor); appErrorCode(t, err) != "BLOCKER_RUN_NOT_SUBMITTABLE" {
		t.Fatalf("submit must be refused for blocked run, got %v", err)
	}
	if _, err := service.Review(run.ID, dto.ReviewRunRequest{Version: run.Version, Decision: "approve", Reason: "attempting to approve blocked evidence"}, actor); err == nil {
		t.Fatal("approve must be refused for a blocked run that never entered review")
	}
}

func TestMatchingRelayRunStaysEvaluated(t *testing.T) {
	service, db := newContentionService(t)
	// Second cue starts exactly when the first ends and from the first end.
	ids := seedContendedCues(t, db, 1000, 8)
	actor := audit.ActorContext{ID: 1, Username: "programmer"}
	run, err := service.Run(dto.RunRehearsalRequest{CueIDs: ids}, actor)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if run.RunStatus != constants.RunEvaluated || run.HighestSeverity != constants.ResultPass {
		t.Fatalf("legal position-matched relay must evaluate cleanly, got status=%s severity=%s", run.RunStatus, run.HighestSeverity)
	}
}

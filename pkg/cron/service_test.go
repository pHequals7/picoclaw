package cron

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testStorePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "cron_store.json")
}

func TestAddJob(t *testing.T) {
	cs := NewCronService(testStorePath(t), nil)

	everyMS := int64(60000)
	schedule := CronSchedule{Kind: "every", EveryMS: &everyMS}

	job, err := cs.AddJob("test-job", schedule, "hello", JobTypeDeliver, "telegram", "123")
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}
	if job.ID == "" {
		t.Error("expected non-empty job ID")
	}
	if job.Payload.Kind != JobTypeDeliver {
		t.Errorf("expected JobTypeDeliver, got %q", job.Payload.Kind)
	}

	jobs := cs.ListJobs(true)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].Name != "test-job" {
		t.Errorf("expected name 'test-job', got %q", jobs[0].Name)
	}
}

func TestRemoveJob(t *testing.T) {
	cs := NewCronService(testStorePath(t), nil)

	everyMS := int64(60000)
	job, _ := cs.AddJob("to-remove", CronSchedule{Kind: "every", EveryMS: &everyMS}, "msg", JobTypeAgent, "", "")

	if !cs.RemoveJob(job.ID) {
		t.Error("expected RemoveJob to return true")
	}
	if len(cs.ListJobs(true)) != 0 {
		t.Error("expected 0 jobs after removal")
	}
	if cs.RemoveJob("nonexistent") {
		t.Error("expected RemoveJob to return false for nonexistent ID")
	}
}

func TestEnableDisable(t *testing.T) {
	cs := NewCronService(testStorePath(t), nil)

	everyMS := int64(60000)
	job, _ := cs.AddJob("toggle-job", CronSchedule{Kind: "every", EveryMS: &everyMS}, "msg", JobTypeDeliver, "", "")

	// Disable
	disabled := cs.EnableJob(job.ID, false)
	if disabled == nil {
		t.Fatal("EnableJob returned nil")
	}
	if disabled.Enabled {
		t.Error("expected job to be disabled")
	}
	if disabled.State.NextRunAtMS != nil {
		t.Error("expected nil NextRunAtMS when disabled")
	}

	// Re-enable
	enabled := cs.EnableJob(job.ID, true)
	if !enabled.Enabled {
		t.Error("expected job to be enabled")
	}
	if enabled.State.NextRunAtMS == nil {
		t.Error("expected non-nil NextRunAtMS when enabled")
	}
}

func TestComputeNextRun_At(t *testing.T) {
	cs := NewCronService(testStorePath(t), nil)
	now := time.Now().UnixMilli()
	future := now + 60000

	// Future "at" schedule
	nextRun := cs.computeNextRun(&CronSchedule{Kind: "at", AtMS: &future}, now)
	if nextRun == nil || *nextRun != future {
		t.Error("expected next run to equal at time")
	}

	// Past "at" schedule
	past := now - 60000
	nextRun = cs.computeNextRun(&CronSchedule{Kind: "at", AtMS: &past}, now)
	if nextRun != nil {
		t.Error("expected nil for past 'at' schedule")
	}
}

func TestComputeNextRun_Every(t *testing.T) {
	cs := NewCronService(testStorePath(t), nil)
	now := time.Now().UnixMilli()
	everyMS := int64(30000)

	nextRun := cs.computeNextRun(&CronSchedule{Kind: "every", EveryMS: &everyMS}, now)
	if nextRun == nil {
		t.Fatal("expected non-nil next run")
	}
	expected := now + 30000
	if *nextRun != expected {
		t.Errorf("expected %d, got %d", expected, *nextRun)
	}
}

func TestComputeNextRun_Cron(t *testing.T) {
	cs := NewCronService(testStorePath(t), nil)
	now := time.Now().UnixMilli()

	// Every minute cron expression
	nextRun := cs.computeNextRun(&CronSchedule{Kind: "cron", Expr: "* * * * *"}, now)
	if nextRun == nil {
		t.Fatal("expected non-nil next run for cron expr")
	}
	if *nextRun <= now {
		t.Error("expected next run to be in the future")
	}
}

func TestCheckJobs_Fires(t *testing.T) {
	var firedJobID string
	handler := func(job *CronJob) (string, error) {
		firedJobID = job.ID
		return "ok", nil
	}

	cs := NewCronService(testStorePath(t), handler)

	// Create a job that's already due
	everyMS := int64(60000)
	job, _ := cs.AddJob("due-job", CronSchedule{Kind: "every", EveryMS: &everyMS}, "fire me", JobTypeDeliver, "", "")

	// Manually set next run to the past and mark service as running
	cs.mu.Lock()
	cs.running = true
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			past := time.Now().UnixMilli() - 1000
			cs.store.Jobs[i].State.NextRunAtMS = &past
		}
	}
	cs.mu.Unlock()

	cs.checkJobs()

	if firedJobID != job.ID {
		t.Errorf("expected job %s to fire, got %q", job.ID, firedJobID)
	}
}

func TestCheckJobs_DeleteAfterRun(t *testing.T) {
	handler := func(job *CronJob) (string, error) { return "ok", nil }
	cs := NewCronService(testStorePath(t), handler)

	atMS := time.Now().UnixMilli() - 1000 // already past
	job, _ := cs.AddJob("one-shot", CronSchedule{Kind: "at", AtMS: &atMS}, "once", JobTypeDeliver, "", "")

	// Set next run to past and mark running so it fires
	cs.mu.Lock()
	cs.running = true
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			past := time.Now().UnixMilli() - 1000
			cs.store.Jobs[i].State.NextRunAtMS = &past
		}
	}
	cs.mu.Unlock()

	cs.checkJobs()

	// Job should be deleted (deleteAfterRun = true for "at" schedules)
	jobs := cs.ListJobs(true)
	for _, j := range jobs {
		if j.ID == job.ID {
			t.Error("expected one-shot job to be deleted after run")
		}
	}
}

func TestPersistence_RoundTrip(t *testing.T) {
	storePath := testStorePath(t)
	cs := NewCronService(storePath, nil)

	everyMS := int64(60000)
	cs.AddJob("persist-me", CronSchedule{Kind: "every", EveryMS: &everyMS}, "hello", JobTypeCommand, "tg", "42")

	// Create new service from same path
	cs2 := NewCronService(storePath, nil)
	jobs := cs2.ListJobs(true)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job after reload, got %d", len(jobs))
	}
	if jobs[0].Name != "persist-me" {
		t.Errorf("expected name 'persist-me', got %q", jobs[0].Name)
	}
	if jobs[0].Payload.Kind != JobTypeCommand {
		t.Errorf("expected JobTypeCommand, got %q", jobs[0].Payload.Kind)
	}
}

func TestMigrateLegacyPayloads_DeliverTrue(t *testing.T) {
	raw := `{
		"version": 1,
		"jobs": [{
			"id": "abc",
			"name": "old-job",
			"enabled": true,
			"schedule": {"kind": "every", "everyMs": 60000},
			"payload": {"kind": "agent_turn", "message": "hello", "deliver": true, "channel": "tg", "to": "42"},
			"state": {},
			"createdAtMs": 1000,
			"updatedAtMs": 1000,
			"deleteAfterRun": false
		}]
	}`

	migrated, didMigrate := MigrateLegacyPayloads([]byte(raw))
	if !didMigrate {
		t.Fatal("expected migration to occur")
	}

	var store CronStore
	if err := json.Unmarshal(migrated, &store); err != nil {
		t.Fatalf("failed to unmarshal migrated data: %v", err)
	}

	if len(store.Jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(store.Jobs))
	}
	if store.Jobs[0].Payload.Kind != JobTypeDeliver {
		t.Errorf("expected JobTypeDeliver, got %q", store.Jobs[0].Payload.Kind)
	}
}

func TestMigrateLegacyPayloads_Command(t *testing.T) {
	raw := `{
		"version": 1,
		"jobs": [{
			"id": "def",
			"name": "cmd-job",
			"enabled": true,
			"schedule": {"kind": "every", "everyMs": 60000},
			"payload": {"kind": "agent_turn", "message": "run it", "command": "ls -la", "deliver": false, "channel": "tg", "to": "42"},
			"state": {},
			"createdAtMs": 1000,
			"updatedAtMs": 1000,
			"deleteAfterRun": false
		}]
	}`

	migrated, didMigrate := MigrateLegacyPayloads([]byte(raw))
	if !didMigrate {
		t.Fatal("expected migration to occur")
	}

	var store CronStore
	json.Unmarshal(migrated, &store)
	if store.Jobs[0].Payload.Kind != JobTypeCommand {
		t.Errorf("expected JobTypeCommand, got %q", store.Jobs[0].Payload.Kind)
	}
}

func TestMigrateLegacyPayloads_AgentFalseDeliver(t *testing.T) {
	raw := `{
		"version": 1,
		"jobs": [{
			"id": "ghi",
			"name": "agent-job",
			"enabled": true,
			"schedule": {"kind": "every", "everyMs": 60000},
			"payload": {"kind": "agent_turn", "message": "process this", "deliver": false, "channel": "tg", "to": "42"},
			"state": {},
			"createdAtMs": 1000,
			"updatedAtMs": 1000,
			"deleteAfterRun": false
		}]
	}`

	migrated, didMigrate := MigrateLegacyPayloads([]byte(raw))
	if !didMigrate {
		t.Fatal("expected migration to occur")
	}

	var store CronStore
	json.Unmarshal(migrated, &store)
	if store.Jobs[0].Payload.Kind != JobTypeAgent {
		t.Errorf("expected JobTypeAgent, got %q", store.Jobs[0].Payload.Kind)
	}
}

func TestMigrateLegacyPayloads_AlreadyMigrated(t *testing.T) {
	raw := `{
		"version": 1,
		"jobs": [{
			"id": "jkl",
			"name": "new-job",
			"enabled": true,
			"schedule": {"kind": "every", "everyMs": 60000},
			"payload": {"kind": "deliver", "message": "hello", "channel": "tg", "to": "42"},
			"state": {},
			"createdAtMs": 1000,
			"updatedAtMs": 1000,
			"deleteAfterRun": false
		}]
	}`

	_, didMigrate := MigrateLegacyPayloads([]byte(raw))
	if didMigrate {
		t.Error("expected no migration for already-migrated jobs")
	}
}

func TestLoadStore_MigratesOnDisk(t *testing.T) {
	storePath := testStorePath(t)

	// Write a legacy-format store to disk
	legacy := `{
		"version": 1,
		"jobs": [{
			"id": "legacy1",
			"name": "old-deliver",
			"enabled": true,
			"schedule": {"kind": "every", "everyMs": 60000},
			"payload": {"kind": "agent_turn", "message": "hi", "deliver": true, "channel": "tg", "to": "42"},
			"state": {},
			"createdAtMs": 1000,
			"updatedAtMs": 1000,
			"deleteAfterRun": false
		}]
	}`
	os.WriteFile(storePath, []byte(legacy), 0644)

	cs := NewCronService(storePath, nil)
	jobs := cs.ListJobs(true)

	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].Payload.Kind != JobTypeDeliver {
		t.Errorf("expected JobTypeDeliver after migration, got %q", jobs[0].Payload.Kind)
	}

	// Verify the migrated data was saved back to disk
	data, _ := os.ReadFile(storePath)
	var store CronStore
	json.Unmarshal(data, &store)
	if store.Jobs[0].Payload.Kind != JobTypeDeliver {
		t.Errorf("expected migrated data on disk to have JobTypeDeliver, got %q", store.Jobs[0].Payload.Kind)
	}
}

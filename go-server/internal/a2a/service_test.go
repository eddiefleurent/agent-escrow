package a2a

import (
	"context"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
)

func setupService(t *testing.T) *Service {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mock := chain.NewMockClient()
	cfg := &config.Config{
		ChainID:        84532,
		FactoryAddress: "0xFactoryAddr",
		Port:           8080,
		A2AEnabled:     true,
		A2AAgentName:   "Test Settlement Agent",
		A2AAgentURL:    "http://localhost:8080",
		RequestTimeout: 10 * time.Second,
		TxTimeout:      90 * time.Second,
	}
	idx := indexer.New(db, mock, cfg.FactoryAddress)

	return &Service{
		DB:    db,
		Chain: mock,
		Idx:   idx,
		Cfg:   cfg,
	}
}

func TestBuildAgentCard(t *testing.T) {
	svc := setupService(t)
	card := svc.BuildAgentCard()

	if card.Name != "Test Settlement Agent" {
		t.Fatalf("expected name 'Test Settlement Agent', got %q", card.Name)
	}
	if card.URL != "http://localhost:8080" {
		t.Fatalf("expected URL 'http://localhost:8080', got %q", card.URL)
	}
	if card.Version != "1.0.0" {
		t.Fatalf("expected version '1.0.0', got %q", card.Version)
	}
	if len(card.Skills) != 8 {
		t.Fatalf("expected 8 skills, got %d", len(card.Skills))
	}

	skillIDs := make(map[string]bool)
	for _, s := range card.Skills {
		skillIDs[s.ID] = true
	}
	expectedSkills := []string{
		"escrow.create", "escrow.fund", "escrow.submit", "escrow.approve",
		"escrow.dispute", "escrow.resolve", "escrow.query", "escrow.settle_task",
	}
	for _, id := range expectedSkills {
		if !skillIDs[id] {
			t.Fatalf("missing skill: %s", id)
		}
	}
}

func TestHandleTaskSend_NoEscrowTrigger(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	task, err := svc.HandleTaskSend(ctx, TaskSendParams{
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "Create a task"}},
		},
		Metadata: map[string]string{
			"delegator_agent": "agent-a",
		},
	})
	if err != nil {
		t.Fatalf("HandleTaskSend: %v", err)
	}

	if task.ID == "" {
		t.Fatal("expected non-empty task ID")
	}
	if task.Status.State != TaskStatusSubmitted {
		t.Fatalf("expected status 'submitted', got %q", task.Status.State)
	}
	if task.Metadata["escrow_trigger"] != "false" {
		t.Fatalf("expected escrow_trigger false, got %q", task.Metadata["escrow_trigger"])
	}
}

func TestHandleTaskSend_WithEscrowTrigger(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	task, err := svc.HandleTaskSend(ctx, TaskSendParams{
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "Create an escrowed task"}},
		},
		Metadata: map[string]string{
			"delegator_agent": "agent-a",
			"verification_policy": `{
				"mode": "strict",
				"artifacts": [{"type": "unit_test_log"}],
				"escrow_trigger": true
			}`,
		},
	})
	if err != nil {
		t.Fatalf("HandleTaskSend: %v", err)
	}

	if task.Metadata["escrow_trigger"] != "true" {
		t.Fatalf("expected escrow_trigger true, got %q", task.Metadata["escrow_trigger"])
	}
}

func TestHandleTaskSend_PreservesCallerMetadata(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	task, err := svc.HandleTaskSend(ctx, TaskSendParams{
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "preserve metadata"}},
		},
		Metadata: map[string]string{
			"delegator_agent": "agent-a",
			"custom_key":      "custom-value",
			"verification_policy": `{
				"mode": "manual",
				"artifacts": [{"type": "manual_review"}],
				"escrow_trigger": true
			}`,
		},
	})
	if err != nil {
		t.Fatalf("HandleTaskSend: %v", err)
	}

	if task.Metadata["custom_key"] != "custom-value" {
		t.Fatalf("expected custom metadata preserved, got %q", task.Metadata["custom_key"])
	}
	if task.Metadata["delegator_agent"] != "agent-a" {
		t.Fatalf("expected delegator_agent preserved, got %q", task.Metadata["delegator_agent"])
	}
	if task.Metadata["escrow_trigger"] != "true" {
		t.Fatalf("expected derived escrow_trigger true, got %q", task.Metadata["escrow_trigger"])
	}
	if task.Metadata["verification_policy"] == "" {
		t.Fatal("expected derived verification_policy to be set")
	}
}

func TestHandleTaskSend_WithExplicitID(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	task, err := svc.HandleTaskSend(ctx, TaskSendParams{
		ID:        "custom-task-id",
		SessionID: "custom-session-id",
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "Hello"}},
		},
	})
	if err != nil {
		t.Fatalf("HandleTaskSend: %v", err)
	}

	if task.ID != "custom-task-id" {
		t.Fatalf("expected task ID 'custom-task-id', got %q", task.ID)
	}
	if task.SessionID != "custom-session-id" {
		t.Fatalf("expected session ID 'custom-session-id', got %q", task.SessionID)
	}
}

func TestHandleTaskSend_UpdateExisting(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	_, err := svc.HandleTaskSend(ctx, TaskSendParams{
		ID: "task-1",
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "Initial message"}},
		},
	})
	if err != nil {
		t.Fatalf("first HandleTaskSend: %v", err)
	}

	task, err := svc.HandleTaskSend(ctx, TaskSendParams{
		ID: "task-1",
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "Follow-up message"}},
		},
	})
	if err != nil {
		t.Fatalf("second HandleTaskSend: %v", err)
	}

	if task.ID != "task-1" {
		t.Fatalf("expected task ID 'task-1', got %q", task.ID)
	}
}

func TestVerificationPolicy_StrictRequiresArtifacts(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	_, err := svc.HandleTaskSend(ctx, TaskSendParams{
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "test"}},
		},
		Metadata: map[string]string{
			"verification_policy": `{"mode": "strict", "artifacts": [], "escrow_trigger": true}`,
		},
	})
	if err == nil {
		t.Fatal("expected error for strict mode without artifacts")
	}
}

func TestVerificationPolicy_InvalidMode(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	_, err := svc.HandleTaskSend(ctx, TaskSendParams{
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "test"}},
		},
		Metadata: map[string]string{
			"verification_policy": `{"mode": "invalid", "escrow_trigger": false}`,
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid verification mode")
	}
}

func TestVerificationPolicy_InvalidArtifactType(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	_, err := svc.HandleTaskSend(ctx, TaskSendParams{
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "test"}},
		},
		Metadata: map[string]string{
			"verification_policy": `{"mode": "strict", "artifacts": [{"type": "invalid_type"}], "escrow_trigger": false}`,
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid artifact type")
	}
}

func TestGetTaskStatus(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	created, err := svc.HandleTaskSend(ctx, TaskSendParams{
		ID: "status-test",
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "test"}},
		},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	task, err := svc.GetTaskStatus(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetTaskStatus: %v", err)
	}

	if task.Status.State != TaskStatusSubmitted {
		t.Fatalf("expected status 'submitted', got %q", task.Status.State)
	}
}

func TestGetTaskStatus_NotFound(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	_, err := svc.GetTaskStatus(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestCancelTask(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	_, err := svc.HandleTaskSend(ctx, TaskSendParams{
		ID: "cancel-test",
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "test"}},
		},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	task, err := svc.CancelTask(ctx, "cancel-test")
	if err != nil {
		t.Fatalf("CancelTask: %v", err)
	}

	if task.Status.State != TaskStatusCanceled {
		t.Fatalf("expected status 'canceled', got %q", task.Status.State)
	}
}

func TestCancelTask_AlreadyCanceled(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	_, err := svc.HandleTaskSend(ctx, TaskSendParams{
		ID: "double-cancel",
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "test"}},
		},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	_, err = svc.CancelTask(ctx, "double-cancel")
	if err != nil {
		t.Fatalf("first cancel: %v", err)
	}

	_, err = svc.CancelTask(ctx, "double-cancel")
	if err == nil {
		t.Fatal("expected error for canceling already-canceled task")
	}
}

func TestCancelTask_NotFound(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	_, err := svc.CancelTask(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestHandleTaskSend_UpdateTerminalState(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	_, err := svc.HandleTaskSend(ctx, TaskSendParams{
		ID: "terminal-test",
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "test"}},
		},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	_, err = svc.CancelTask(ctx, "terminal-test")
	if err != nil {
		t.Fatalf("cancel task: %v", err)
	}

	_, err = svc.HandleTaskSend(ctx, TaskSendParams{
		ID: "terminal-test",
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "should fail"}},
		},
	})
	if err == nil {
		t.Fatal("expected error for updating terminal task")
	}
}

func TestVerificationPolicy_OptimisticMode(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	task, err := svc.HandleTaskSend(ctx, TaskSendParams{
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "optimistic task"}},
		},
		Metadata: map[string]string{
			"verification_policy": `{"mode": "optimistic", "escrow_trigger": false}`,
		},
	})
	if err != nil {
		t.Fatalf("HandleTaskSend: %v", err)
	}

	if task.Status.State != TaskStatusSubmitted {
		t.Fatalf("expected status 'submitted', got %q", task.Status.State)
	}
}

func TestVerificationPolicy_ManualMode(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	task, err := svc.HandleTaskSend(ctx, TaskSendParams{
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "manual review task"}},
		},
		Metadata: map[string]string{
			"verification_policy": `{"mode": "manual", "artifacts": [{"type": "manual_review"}], "escrow_trigger": true}`,
		},
	})
	if err != nil {
		t.Fatalf("HandleTaskSend: %v", err)
	}

	if task.Metadata["escrow_trigger"] != "true" {
		t.Fatalf("expected escrow_trigger true, got %q", task.Metadata["escrow_trigger"])
	}
}

func TestSessionManagement(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()

	_, err := svc.HandleTaskSend(ctx, TaskSendParams{
		ID:        "session-task-1",
		SessionID: "session-abc",
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "first task"}},
		},
	})
	if err != nil {
		t.Fatalf("create first task: %v", err)
	}

	_, err = svc.HandleTaskSend(ctx, TaskSendParams{
		ID:        "session-task-2",
		SessionID: "session-abc",
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "second task"}},
		},
	})
	if err != nil {
		t.Fatalf("create second task: %v", err)
	}

	tasks, err := svc.DB.ListA2ATasksBySession(ctx, "session-abc")
	if err != nil {
		t.Fatalf("list tasks by session: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks in session, got %d", len(tasks))
	}
}

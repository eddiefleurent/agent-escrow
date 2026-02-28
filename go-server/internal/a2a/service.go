package a2a

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/google/uuid"
)

var ErrInvalidParams = errors.New("invalid params")

// Service encapsulates A2A settlement adapter business logic, mapping A2A task
// lifecycle to escrow operations. Follows the same shared-logic pattern as bidding.Service.
type Service struct {
	DB    *storage.DB
	Chain chain.ChainClient
	Idx   *indexer.Indexer
	Cfg   *config.Config
}

// BuildAgentCard constructs the agent card from config.
func (s *Service) BuildAgentCard() *AgentCard {
	return &AgentCard{
		Name:        s.Cfg.A2AAgentName,
		Description: "On-chain escrow settlement agent for AI delegation tasks. Implements the Intelligent AI Delegation paper (Tomašev et al., 2026) §6: A2A Task object extension with verification_policy and escrow_trigger fields.",
		URL:         s.Cfg.A2AAgentURL,
		Version:     "1.0.0",
		Provider: &Provider{
			Organization: "agent-escrow",
		},
		Capabilities: Capabilities{
			Streaming:            false,
			PushNotifications:    false,
			StateTransitionHooks: true,
		},
		Authentication: Authentication{
			Schemes: []string{"none"},
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Skills: []Skill{
			{
				ID:          "escrow.create",
				Name:        "Create Escrow",
				Description: "Create a new escrow from an A2A task with verification_policy",
				Tags:        []string{"escrow", "settlement", "create"},
			},
			{
				ID:          "escrow.fund",
				Name:        "Fund Escrow",
				Description: "Fund an existing escrow",
				Tags:        []string{"escrow", "settlement", "fund"},
			},
			{
				ID:          "escrow.submit",
				Name:        "Submit Work",
				Description: "Submit work for verification",
				Tags:        []string{"escrow", "settlement", "submit"},
			},
			{
				ID:          "escrow.approve",
				Name:        "Approve Work",
				Description: "Approve submitted work",
				Tags:        []string{"escrow", "settlement", "approve"},
			},
			{
				ID:          "escrow.dispute",
				Name:        "Dispute Work",
				Description: "Dispute submitted work",
				Tags:        []string{"escrow", "settlement", "dispute"},
			},
			{
				ID:          "escrow.resolve",
				Name:        "Resolve Dispute",
				Description: "Resolve a dispute",
				Tags:        []string{"escrow", "settlement", "resolve"},
			},
			{
				ID:          "escrow.query",
				Name:        "Query Escrow",
				Description: "Query escrow status",
				Tags:        []string{"escrow", "settlement", "query"},
			},
			{
				ID:          "escrow.settle_task",
				Name:        "Settle Task",
				Description: "End-to-end: accept A2A task, create escrow, return settlement result",
				Tags:        []string{"escrow", "settlement", "e2e"},
			},
		},
	}
}

// HandleTaskSend processes a tasks/send request, creating or updating an A2A task.
// If escrow_trigger is true in the verification_policy metadata, the task is linked
// to an on-chain escrow.
func (s *Service) HandleTaskSend(ctx context.Context, params TaskSendParams) (*Task, error) {
	taskID := params.ID
	if taskID == "" {
		taskID = uuid.New().String()
	}

	sessionID := params.SessionID
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	existing, err := s.DB.GetA2ATaskByTaskID(ctx, taskID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("lookup a2a task: %w", err)
		}
		return s.createNewTask(ctx, taskID, sessionID, params)
	}

	return s.updateExistingTask(existing, params)
}

func (s *Service) createNewTask(ctx context.Context, taskID, sessionID string, params TaskSendParams) (*Task, error) {
	var vp VerificationPolicy
	escrowTrigger := false
	vpJSON := "{}"

	if raw, ok := params.Metadata["verification_policy"]; ok {
		if err := json.Unmarshal([]byte(raw), &vp); err != nil {
			return nil, fmt.Errorf("%w: invalid verification_policy: %w", ErrInvalidParams, err)
		}
		if err := validateVerificationPolicy(vp); err != nil {
			return nil, fmt.Errorf("%w: verification_policy validation: %w", ErrInvalidParams, err)
		}
		escrowTrigger = vp.EscrowTrigger
		vpJSON = raw
	}

	delegator := ""
	if v, ok := params.Metadata["delegator_agent"]; ok {
		delegator = v
	}
	if delegator == "" {
		delegator = params.Message.Role
	}

	metadataJSON, err := json.Marshal(params.Metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}

	a2aTask, err := s.DB.CreateA2ATask(ctx, &storage.A2ATask{
		A2ATaskID:              taskID,
		SessionID:              sessionID,
		DelegatorAgent:         delegator,
		VerificationPolicyJSON: vpJSON,
		EscrowTrigger:          escrowTrigger,
		A2AStatus:              string(TaskStatusSubmitted),
		MetadataJSON:           string(metadataJSON),
	})
	if err != nil {
		return nil, fmt.Errorf("create a2a task: %w", err)
	}

	slog.Info("a2a task created",
		"a2a_task_id", taskID,
		"session_id", sessionID,
		"escrow_trigger", escrowTrigger,
		"verification_mode", vp.Mode,
	)

	return buildTaskResponse(a2aTask, "Task accepted"), nil
}

func (s *Service) updateExistingTask(existing *storage.A2ATask, params TaskSendParams) (*Task, error) {
	if existing.A2AStatus == string(TaskStatusCompleted) ||
		existing.A2AStatus == string(TaskStatusFailed) ||
		existing.A2AStatus == string(TaskStatusCanceled) {
		return nil, fmt.Errorf("task %s is in terminal state: %s", existing.A2ATaskID, existing.A2AStatus)
	}

	slog.Info("a2a task send idempotent",
		"a2a_task_id", existing.A2ATaskID,
		"status", existing.A2AStatus,
	)

	return buildTaskResponse(existing, "Task already exists"), nil
}

// GetTaskStatus returns the current status of an A2A task.
func (s *Service) GetTaskStatus(ctx context.Context, taskID string) (*Task, error) {
	a2aTask, err := s.DB.GetA2ATaskByTaskID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("get task %s: %w", taskID, err)
	}

	statusMsg := "Task status: " + a2aTask.A2AStatus
	if a2aTask.EscrowID != nil {
		escrow, err := s.DB.GetEscrow(ctx, *a2aTask.EscrowID)
		if err == nil {
			statusMsg = fmt.Sprintf("Task status: %s, escrow status: %s, escrow address: %s",
				a2aTask.A2AStatus, escrow.Status, escrow.EscrowAddress)
		}
	}

	return buildTaskResponse(a2aTask, statusMsg), nil
}

// CancelTask cancels an A2A task if it's not in a terminal state.
func (s *Service) CancelTask(ctx context.Context, taskID string) (*Task, error) {
	a2aTask, err := s.DB.GetA2ATaskByTaskID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("get task %s: %w", taskID, err)
	}

	if a2aTask.A2AStatus == string(TaskStatusCompleted) ||
		a2aTask.A2AStatus == string(TaskStatusFailed) ||
		a2aTask.A2AStatus == string(TaskStatusCanceled) {
		return nil, fmt.Errorf("task %s is in terminal state: %s", a2aTask.A2ATaskID, a2aTask.A2AStatus)
	}

	if err := s.DB.UpdateA2ATaskStatus(ctx, a2aTask.ID, string(TaskStatusCanceled)); err != nil {
		return nil, fmt.Errorf("cancel task: %w", err)
	}

	slog.Info("a2a task canceled", "a2a_task_id", taskID)

	a2aTask.A2AStatus = string(TaskStatusCanceled)
	return buildTaskResponse(a2aTask, "Task canceled"), nil
}

func validateVerificationPolicy(vp VerificationPolicy) error {
	switch vp.Mode {
	case "strict", "optimistic", "manual":
	case "":
		return errors.New("mode is required")
	default:
		return fmt.Errorf("invalid mode: %s (must be strict, optimistic, or manual)", vp.Mode)
	}

	if vp.Mode == "strict" && len(vp.Artifacts) == 0 {
		return errors.New("strict mode requires at least one verification artifact")
	}

	for i, a := range vp.Artifacts {
		switch a.Type {
		case VerificationArtifactUnitTestLog, VerificationArtifactZKSnarkTrace, VerificationArtifactManualReview:
		default:
			return fmt.Errorf("artifact[%d]: invalid type %q (must be unit_test_log, zk_snark_trace, or manual_review)", i, a.Type)
		}
	}

	return nil
}

func buildTaskResponse(a2aTask *storage.A2ATask, message string) *Task {
	metadata := make(map[string]string)
	if a2aTask.MetadataJSON != "" {
		if err := json.Unmarshal([]byte(a2aTask.MetadataJSON), &metadata); err != nil {
			slog.Warn("failed to decode task metadata, using derived metadata only",
				"a2a_task_id", a2aTask.A2ATaskID,
				"error", err,
			)
			metadata = make(map[string]string)
		}
	}
	if metadata == nil {
		metadata = make(map[string]string)
	}

	metadata["escrow_trigger"] = strconv.FormatBool(a2aTask.EscrowTrigger)
	metadata["verification_policy"] = a2aTask.VerificationPolicyJSON

	return &Task{
		ID:        a2aTask.A2ATaskID,
		SessionID: a2aTask.SessionID,
		Status: TaskSummary{
			State: TaskStatus(a2aTask.A2AStatus),
			Message: &Message{
				Role: "agent",
				Parts: []Part{
					{Type: "text", Text: message},
				},
			},
		},
		Metadata: metadata,
	}
}

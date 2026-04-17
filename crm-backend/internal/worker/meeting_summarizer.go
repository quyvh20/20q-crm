package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"crm-backend/internal/ai"
	"crm-backend/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type MeetingSummaryPayload struct {
	Transcript string     `json:"transcript"`
	DealID     *uuid.UUID `json:"deal_id,omitempty"`
	ContactID  *uuid.UUID `json:"contact_id,omitempty"`
}

type ParsedMeetingSummary struct {
	Summary     string `json:"summary"`
	ActionItems []struct {
		Title    string `json:"title"`
		Due      string `json:"due"`
		Priority string `json:"priority"` // low, medium, high
	} `json:"action_items"`
}

type FinalSummaryResult struct {
	Summary      string         `json:"summary"`
	CreatedTasks []domain.Task  `json:"created_tasks"`
}

// ProcessMeetingSummary summarizes transcript and auto-generates tasks.
func ProcessMeetingSummary(ctx context.Context, q *AIJobQueue, job *AIJob) (json.RawMessage, error) {
	var payload MeetingSummaryPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	prompt := fmt.Sprintf(`Analyze this meeting transcript and extract a short summary plus any action items/tasks mentioned.
Transcript:
"""%s"""

You MUST return strictly valid JSON matching this exact schema:
{
  "summary": "Brief 2-3 sentence overview",
  "action_items": [
    {"title": "Send contract to Acme", "due": "2024-05-10T12:00:00Z", "priority": "high"}
  ]
}

CRITICAL: If the transcript explicitly mentions actions, deliverables, or follow-ups (e.g. 'I will prepare the release notes'), you MUST populate the "action_items" array. Extract all tasks unconditionally. Do NOT wrap inside markdown blocks. Output only raw JSON starting with '{'.`, payload.Transcript)

	msgs := []ai.Message{{Role: "user", Content: prompt}}

	// 1. Call gateway
	resp, err := q.GetGateway().Complete(ctx, job.OrgID, job.UserID, ai.TaskMeetingSummary, msgs)
	if err != nil {
		return nil, fmt.Errorf("AI completion failed: %w", err)
	}

	q.logger.Info("AI Summary Response", zap.String("content", resp.Content))

	var parsed ParsedMeetingSummary
	if err := json.Unmarshal([]byte(resp.Content), &parsed); err != nil {
		return nil, fmt.Errorf("AI did not return valid JSON: %w (Response: %s)", err, resp.Content)
	}


	// 2. Transact tasks to DB
	db := q.GetDB()
	var createdTasks []domain.Task
	
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Log the summary as an Activity first
		activity := domain.Activity{
			OrgID:      job.OrgID,
			Type:       "meeting",
			UserID:     &job.UserID,
			DealID:     payload.DealID,
			ContactID:  payload.ContactID,
			Title:      func() *string { s := "AI Meeting Summary"; return &s }(),
			Body:       &parsed.Summary,
			OccurredAt: time.Now(),
		}
		if err := tx.Create(&activity).Error; err != nil {
			return err
		}

		// Create extracted tasks
		for _, item := range parsed.ActionItems {
			var dueAt *time.Time
			if item.Due != "" {
				t, e := time.Parse(time.RFC3339, item.Due)
				if e == nil {
					dueAt = &t
				}
			}
			
			task := domain.Task{
				OrgID:      job.OrgID,
				Title:      item.Title,
				DealID:     payload.DealID,
				ContactID:  payload.ContactID,
				DueAt:      dueAt,
				Priority:   item.Priority,
			}
			if task.Priority == "" {
				task.Priority = "medium"
			}
			
			if err := tx.Create(&task).Error; err != nil {
				q.logger.Error("failed to create task from summary", zap.Error(err))
				continue // graceful degradation
			}
			createdTasks = append(createdTasks, task)
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to save items to DB: %w", err)
	}

	finalResult := FinalSummaryResult{
		Summary:      parsed.Summary,
		CreatedTasks: createdTasks,
	}

	return json.Marshal(finalResult)
}

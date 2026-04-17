package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"crm-backend/internal/ai"
	"crm-backend/internal/domain"
	"github.com/google/uuid"
)

type SentimentPayload struct {
	ActivityID uuid.UUID `json:"activity_id"`
}

func ProcessSentimentAnalysis(ctx context.Context, q *AIJobQueue, job *AIJob) (json.RawMessage, error) {
	var payload SentimentPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	db := q.GetDB()
	var act domain.Activity
	if err := db.WithContext(ctx).First(&act, "id = ? AND org_id = ?", payload.ActivityID, job.OrgID).Error; err != nil {
		return nil, fmt.Errorf("activity not found: %w", err)
	}

	if act.Body == nil || *act.Body == "" {
		return nil, nil // No body to analyze
	}

	prompt := fmt.Sprintf(`Analyze the sentiment of this CRM activity note.
Note: "%s"

You MUST return strictly valid JSON matching this schema, without markdown formatting:
{"sentiment": "positive", "confidence": 0.8}
Valid sentiments are 'positive', 'neutral', 'negative'.`, *act.Body)

	msgs := []ai.Message{{Role: "user", Content: prompt}}

	resp, err := q.GetGateway().Complete(ctx, job.OrgID, job.UserID, ai.TaskSentiment, msgs)
	if err != nil {
		return nil, fmt.Errorf("AI completion failed: %w", err)
	}

	var result struct {
		Sentiment string `json:"sentiment"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		return nil, fmt.Errorf("invalid AI json: %w", err)
	}

	// Update the activity in the DB
	if err := db.WithContext(ctx).Model(&domain.Activity{}).
		Where("id = ? AND org_id = ?", payload.ActivityID, job.OrgID).
		Update("sentiment", result.Sentiment).Error; err != nil {
		return nil, fmt.Errorf("failed to save sentiment: %w", err)
	}

	return json.RawMessage(resp.Content), nil
}

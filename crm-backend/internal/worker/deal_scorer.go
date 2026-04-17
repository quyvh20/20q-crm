package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"crm-backend/internal/ai"
	"crm-backend/internal/domain"
	"github.com/google/uuid"
)

type DealScorePayload struct {
	DealID     uuid.UUID `json:"deal_id"`
}

// ProcessDealScore analyzes a deal and computes a score via Anthropic.
func ProcessDealScore(ctx context.Context, q *AIJobQueue, job *AIJob) (json.RawMessage, error) {
	var payload DealScorePayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	// 1. Fetch deal data
	db := q.GetDB()
	var deal domain.Deal
	if err := db.WithContext(ctx).Preload("Contact").Preload("Stage").First(&deal, "id = ? AND org_id = ?", payload.DealID, job.OrgID).Error; err != nil {
		return nil, fmt.Errorf("deal not found: %w", err)
	}

	// 2. Fetch recent activities
	var activities []domain.Activity
	db.WithContext(ctx).Where("deal_id = ?", deal.ID).Order("occurred_at DESC").Limit(20).Find(&activities)

	// 3. Format prompt
	actSummary := ""
	for _, a := range activities {
		body := ""
		if a.Body != nil {
			body = *a.Body
		}
		actSummary += fmt.Sprintf("- [%s] %s: %s\n", a.OccurredAt.Format("2006-01-02"), a.Type, body)
	}

	stageName := "None"
	if deal.Stage != nil {
		stageName = deal.Stage.Name
	}
	daysInStage := 0
	if !deal.CreatedAt.IsZero() {
		daysInStage = int(time.Since(deal.CreatedAt).Hours() / 24)
	}

	prompt := fmt.Sprintf(`Analyze this CRM deal and estimate the win probability (0-100).
Deal Title: "%s"
Stage: %s
Value: $%f
Days open: %d
Recent Activities:
%s

You MUST return strictly valid JSON matching this schema, completely unformatted:
{"score": 75, "factors": ["+ recent positive meeting", "- high value requires approval"], "recommendation": "Follow up with pricing"}`, deal.Title, stageName, deal.Value, daysInStage, actSummary)

	msgs := []ai.Message{{Role: "user", Content: prompt}}

	// 4. Call gateway
	resp, err := q.GetGateway().Complete(ctx, job.OrgID, job.UserID, ai.TaskDealScore, msgs)
	if err != nil {
		return nil, fmt.Errorf("AI completion failed: %w", err)
	}

	// Make sure we have JSON
	if !json.Valid([]byte(resp.Content)) {
		return nil, fmt.Errorf("AI did not return valid JSON: %s", resp.Content)
	}

	// Optionally cache the result in Redis for subsequent direct HTTP hits
	cacheKey := fmt.Sprintf("deal_score:%s", deal.ID.String())
	q.GetRedis().Set(ctx, cacheKey, resp.Content, time.Hour) // cache for 1 hour

	return json.RawMessage(resp.Content), nil
}

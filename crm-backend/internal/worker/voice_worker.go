package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"crm-backend/internal/ai"
	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type VoiceNoteJobPayload struct {
	VoiceNoteID  uuid.UUID `json:"voice_note_id"`
	OrgID        uuid.UUID `json:"org_id"`
	Filename     string    `json:"filename"`
	LanguageCode string    `json:"language_code"`
	FileURL      string    `json:"file_url"`
	StorageMode  string    `json:"storage_mode"`
}

type ParsedVoiceInsight struct {
	Summary     string   `json:"summary"`
	KeyPoints   []string `json:"key_points"`
	ActionItems []struct {
		Title    string `json:"title"`
		Due      string `json:"due,omitempty"`
		Priority string `json:"priority"`
	} `json:"action_items"`
	ExtractedContactUpdates struct {
		PhoneNumbers    []string `json:"phone_numbers,omitempty"`
		Emails          []string `json:"emails,omitempty"`
		Budget          string   `json:"budget,omitempty"`
		NextMeetingDate string   `json:"next_meeting_date,omitempty"`
		CompanyName     string   `json:"company_name,omitempty"`
		Notes           string   `json:"notes,omitempty"`
	} `json:"extracted_contact_updates"`
	Sentiment string `json:"sentiment"`
}

func ProcessVoiceNote(ctx context.Context, q *AIJobQueue, job *AIJob) (json.RawMessage, error) {
	var payload VoiceNoteJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	db := q.GetDB()
	var note domain.VoiceNote
	if err := db.WithContext(ctx).
		Preload("Contact").
		Preload("Deal").
		First(&note, "id = ? AND org_id = ?", payload.VoiceNoteID, job.OrgID).Error; err != nil {
		return nil, fmt.Errorf("voice note not found: %w", err)
	}

	setError := func(msg string) {
		errMsg := msg
		db.WithContext(ctx).Model(&note).Updates(map[string]interface{}{
			"status":        "error",
			"error_message": errMsg,
		})
		// Publish SSE so frontend reacts immediately
		sseChan := fmt.Sprintf("sse:%s", job.OrgID.String())
		eventPayload := map[string]interface{}{
			"type":          "voice_note_error",
			"voice_note_id": note.ID.String(),
			"error":         errMsg,
		}
		eventData, _ := json.Marshal(eventPayload)
		q.GetRedis().Publish(ctx, sseChan, eventData)
	}

	if err := db.WithContext(ctx).Model(&note).Update("status", "processing").Error; err != nil {
		return nil, fmt.Errorf("update status: %w", err)
	}

	var audioFilePath string
	var err error

	switch payload.StorageMode {
	case "r2":
		path, err := fetchRemoteAudioToFile(ctx, payload.FileURL)
		if err != nil {
			setError(fmt.Sprintf("download audio from R2: %s", err.Error()))
			return nil, fmt.Errorf("download audio: %w", err)
		}
		audioFilePath = path
		defer os.Remove(audioFilePath)
	case "local":
		// URL is /api/voice/preview/filename.wav -> map back to physical path
		filename := payload.FileURL[19:]
		audioFilePath = filepath.Join("storage", "voice_notes", filename)
	default:
		setError(fmt.Sprintf("unknown storage mode: %s", payload.StorageMode))
		return nil, fmt.Errorf("unknown storage mode: %s", payload.StorageMode)
	}

	chunks, downsampleErr := processAndChunkAudio(ctx, q.logger, audioFilePath)
	if downsampleErr != nil {
		q.logger.Error("failed to downsample/chunk audio, reading single bulk file", zap.Error(downsampleErr))
		bulkData, bulkErr := os.ReadFile(audioFilePath)
		if bulkErr != nil {
			setError(fmt.Sprintf("failed to read raw audio file: %s", bulkErr.Error()))
			return nil, bulkErr
		}
		const whisperMaxBytes = 25 * 1024 * 1024 // 25 MB CF Whisper limit
		if len(bulkData) > whisperMaxBytes {
			msg := fmt.Sprintf("audio file too large for transcription (%d MB). ffmpeg is required to split large files — please contact support.", len(bulkData)/(1024*1024))
			setError(msg)
			return nil, fmt.Errorf(msg)
		}
		q.logger.Info("using raw audio file (no ffmpeg)", zap.Int("size_bytes", len(bulkData)))
		chunks = [][]byte{bulkData}
	}

	gateway := q.GetGateway()
	var fullTranscript strings.Builder

	for i, chunkBytes := range chunks {
		q.logger.Info(fmt.Sprintf("starting Whisper transcription for chunk %d/%d", i+1, len(chunks)), zap.String("note_id", note.ID.String()), zap.String("lang", payload.LanguageCode))
		transcribeResult, err := gateway.TranscribeAudio(ctx, chunkBytes, fmt.Sprintf("%s_part%d.wav", payload.Filename, i), payload.LanguageCode)
		if err != nil {
			setError(fmt.Sprintf("transcription failed on chunk %d: %s", i+1, err.Error()))
			return nil, fmt.Errorf("transcribe chunk %d: %w", i+1, err)
		}
		if fullTranscript.Len() > 0 {
			fullTranscript.WriteString(" ")
		}
		fullTranscript.WriteString(strings.TrimSpace(transcribeResult.Text))
		q.logger.Info("chunk transcription complete", zap.Int("chunk", i+1), zap.Int("chars", len(transcribeResult.Text)))
	}

	transcript := fullTranscript.String()

	langInstruction := "the exact SAME language as the transcript"
	if payload.LanguageCode != "" && payload.LanguageCode != "auto" {
		langInstruction = fmt.Sprintf("ISO-639-1 language code '%s'", payload.LanguageCode)
	}

	var bgContext strings.Builder
	if note.Contact != nil {
		bgContext.WriteString(fmt.Sprintf("Existing Contact Details:\n- Name: %s %s\n", note.Contact.FirstName, note.Contact.LastName))
		if note.Contact.Email != nil {
			bgContext.WriteString(fmt.Sprintf("- Email: %s\n", *note.Contact.Email))
		}
		if note.Contact.Phone != nil {
			bgContext.WriteString(fmt.Sprintf("- Phone: %s\n", *note.Contact.Phone))
		}
	}
	if note.Deal != nil {
		bgContext.WriteString(fmt.Sprintf("\nExisting Deal Details:\n- Title: %s\n- Value: %.2f\n", note.Deal.Title, note.Deal.Value))
	}
	if bgContext.Len() == 0 {
		bgContext.WriteString("No existing background context available.\n")
	}

	insightPrompt := fmt.Sprintf(`You are a CRM intelligence assistant. Analyze this transcript and extract structured CRM data into JSON.

Background Context (Ground Truth):
%s

CRITICAL RULES FOR 'ExtractedContactUpdates':
1. Compare the Transcript against the Background Context.
2. ONLY extract phone numbers, emails, or names if they are NEW or DIFFERENT from the Background Context.
3. If the speaker confirms the existing information is correct without changing it, DO NOT include it in the updates.

Transcript:
"""%s"""

Return ONLY a valid JSON object with this exact schema (no markdown, no explanation):
{
  "summary": "2-3 sentence overview of the conversation",
  "key_points": ["point1", "point2"],
  "action_items": [
    {"title": "Follow up with contract", "due": "2024-05-10T12:00:00Z", "priority": "high"}
  ],
  "extracted_contact_updates": {
    "phone_numbers": [],
    "emails": [],
    "budget": "",
    "next_meeting_date": "",
    "company_name": "",
    "notes": ""
  },
  "sentiment": "positive"
}

Rules:
- sentiment must be one of: positive, neutral, negative, mixed
- action_items priority must be: low, medium, or high
- Output ONLY raw JSON starting with '{'
- MULTILINGUAL REQUIREMENT: You MUST generate the summary, key_points, and action_items text in %s!`, bgContext.String(), transcript, langInstruction)

	msgs := []ai.Message{{Role: "user", Content: insightPrompt}}
	resp, err := gateway.Complete(ctx, job.OrgID, job.UserID, ai.TaskVoiceIntelligence, msgs)
	if err != nil {
		setError(fmt.Sprintf("intelligence extraction failed: %s", err.Error()))
		return nil, fmt.Errorf("kimi analysis: %w", err)
	}

	var insight ParsedVoiceInsight
	if err := json.Unmarshal([]byte(resp.Content), &insight); err != nil {
		setError(fmt.Sprintf("parse AI response: %s (response: %s)", err.Error(), resp.Content))
		return nil, fmt.Errorf("parse insight JSON: %w", err)
	}

	keyPointsJSON, _ := json.Marshal(insight.KeyPoints)
	actionItemsJSON, _ := json.Marshal(insight.ActionItems)
	contactUpdatesJSON, _ := json.Marshal(insight.ExtractedContactUpdates)

	updates := map[string]interface{}{
		"status":                    "done",
		"transcript":                transcript,
		"summary":                   insight.Summary,
		"key_points":                string(keyPointsJSON),
		"action_items":              string(actionItemsJSON),
		"extracted_contact_updates": string(contactUpdatesJSON),
		"sentiment":                 insight.Sentiment,
		"error_message":             nil,
	}
	if err := db.WithContext(ctx).Model(&note).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("save insights to db: %w", err)
	}

	hasContactUpdates := false
	cu := insight.ExtractedContactUpdates
	if len(cu.PhoneNumbers) > 0 || len(cu.Emails) > 0 || cu.Budget != "" || cu.NextMeetingDate != "" || cu.CompanyName != "" {
		hasContactUpdates = true
	}

	sseChan := fmt.Sprintf("sse:%s", job.OrgID.String())
	eventPayload := map[string]interface{}{
		"type":                   "voice_note_ready",
		"voice_note_id":          note.ID.String(),
		"has_contact_updates":    hasContactUpdates,
		"sentiment":              insight.Sentiment,
	}
	if hasContactUpdates {
		eventPayload["profile_update_suggestion"] = true
		eventPayload["message"] = "AI extracted new contact data from your voice note. Review and apply?"
	}
	eventData, _ := json.Marshal(eventPayload)
	q.GetRedis().Publish(ctx, sseChan, eventData)

	q.logger.Info("voice note processed successfully",
		zap.String("note_id", note.ID.String()),
		zap.Bool("has_contact_updates", hasContactUpdates),
		zap.String("sentiment", insight.Sentiment),
	)

	return json.Marshal(map[string]interface{}{
		"voice_note_id":       note.ID.String(),
		"sentiment":           insight.Sentiment,
		"has_contact_updates": hasContactUpdates,
	})
}

func fetchRemoteAudioToFile(ctx context.Context, url string) (string, error) {
	_ = ctx
	return "", fmt.Errorf("R2 fetch not yet implemented; configure fetchRemoteAudioToFile with your R2 S3-compatible client. URL: %s", url)
}

func processAndChunkAudio(ctx context.Context, logger *zap.Logger, inputPath string) ([][]byte, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		logger.Warn("ffmpeg not found in PATH, skipping downsample and chunk")
		return nil, fmt.Errorf("ffmpeg missing")
	}

	tempDir := os.TempDir()
	baseID := uuid.New().String()
	outputPattern := filepath.Join(tempDir, fmt.Sprintf("out_%s_%%03d.wav", baseID))

	// 10 minutes = 600 seconds. 16kHz mono 16-bit WAV is ~19MB per 10min, safely under 25MB Whisper limits.
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", inputPath, "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", "-f", "segment", "-segment_time", "600", outputPattern)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %w, output: %s", err, string(out))
	}

	matches, err := filepath.Glob(filepath.Join(tempDir, fmt.Sprintf("out_%s_*.wav", baseID)))
	if err != nil {
		return nil, fmt.Errorf("glob output files: %w", err)
	}
	sort.Strings(matches)

	var chunks [][]byte
	for _, match := range matches {
		defer os.Remove(match)
		data, err := os.ReadFile(match)
		if err != nil {
			return nil, fmt.Errorf("read chunk file: %w", err)
		}
		chunks = append(chunks, data)
	}

	var originalSize int64
	if stat, err := os.Stat(inputPath); err == nil {
		originalSize = stat.Size()
	}
	logger.Info("audio chunked and downsampled successfully", zap.Int("num_chunks", len(chunks)), zap.Int64("original_size", originalSize))
	return chunks, nil
}

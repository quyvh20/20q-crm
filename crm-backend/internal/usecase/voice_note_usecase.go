package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"crm-backend/internal/domain"
	"crm-backend/internal/worker"
	"crm-backend/pkg/config"

	"github.com/google/uuid"
)

type voiceNoteUseCase struct {
	repo    domain.VoiceNoteRepository
	queue   *worker.AIJobQueue
	cfg     *config.Config
	contactRepo domain.ContactRepository
}

func NewVoiceNoteUseCase(
	repo domain.VoiceNoteRepository,
	queue *worker.AIJobQueue,
	cfg *config.Config,
	contactRepo domain.ContactRepository,
) domain.VoiceNoteUseCase {
	return &voiceNoteUseCase{
		repo:        repo,
		queue:       queue,
		cfg:         cfg,
		contactRepo: contactRepo,
	}
}

func (uc *voiceNoteUseCase) Upload(ctx context.Context, orgID, userID uuid.UUID, input domain.UploadVoiceNoteInput) (*domain.VoiceNote, string, error) {
	storageMode := "local"
	var fileURL string

	if uc.cfg.R2BucketName != "" {
		uploaded, err := uploadToR2(ctx, uc.cfg, input.AudioBytes, input.OriginalName)
		if err != nil {
			return nil, "", fmt.Errorf("R2 upload failed: %w", err)
		}
		fileURL = uploaded
		storageMode = "r2"
	} else {
		// Native disk write fallback
		if err := os.MkdirAll(filepath.Join("storage", "voice_notes"), 0755); err != nil {
			return nil, "", fmt.Errorf("failed to create storage dir: %w", err)
		}
		noteID := uuid.New()
		filename := fmt.Sprintf("%s_%s", noteID.String(), input.OriginalName)
		filePath := filepath.Join("storage", "voice_notes", filename)
		
		if err := os.WriteFile(filePath, input.AudioBytes, 0644); err != nil {
			return nil, "", fmt.Errorf("failed to save local file: %w", err)
		}
		fileURL = "/api/voice/preview/" + filename
	}

	status := "uploaded"
	if input.AutoAnalyze {
		status = "pending"
	}

	note := &domain.VoiceNote{
		OrgID:           orgID,
		UserID:          userID,
		ContactID:       input.ContactID,
		DealID:          input.DealID,
		FileURL:         fileURL,
		DurationSeconds: input.DurationSeconds,
		LanguageCode:    input.LanguageCode,
		Status:          status,
	}
	if note.LanguageCode == "" {
		note.LanguageCode = "en"
	}

	if err := uc.repo.Create(ctx, note); err != nil {
		return nil, "", fmt.Errorf("create voice note record: %w", err)
	}

	if !input.AutoAnalyze {
		return note, "", nil
	}

	payload := worker.VoiceNoteJobPayload{
		VoiceNoteID:  note.ID,
		OrgID:        orgID,
		Filename:     input.OriginalName,
		LanguageCode: note.LanguageCode,
		FileURL:      fileURL,
		StorageMode:  storageMode,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("marshal job payload: %w", err)
	}

	job := &worker.AIJob{
		JobID:    uuid.New(),
		OrgID:    orgID,
		UserID:   userID,
		TaskType: "voice_note",
		Payload:  payloadBytes,
	}
	if err := uc.queue.Enqueue(ctx, job); err != nil {
		return nil, "", fmt.Errorf("enqueue voice job: %w", err)
	}

	return note, job.JobID.String(), nil
}

func (uc *voiceNoteUseCase) Analyze(ctx context.Context, orgID, userID, noteID uuid.UUID) error {
	note, err := uc.repo.GetByID(ctx, orgID, noteID)
	if err != nil {
		return fmt.Errorf("get note: %w", err)
	}
	// Allow retry from "pending" too — the job may have been lost if the worker
	// crashed or Redis dropped the queue entry, leaving the note stuck forever.
	if note.Status != "uploaded" && note.Status != "error" && note.Status != "pending" {
		return fmt.Errorf("note is not in an analyzable state (current: %s)", note.Status)
	}

	note.Status = "pending"
	if err := uc.repo.Update(ctx, note); err != nil {
		return fmt.Errorf("update status to pending: %w", err)
	}

	storageMode := "r2"
	if len(note.FileURL) >= 19 && note.FileURL[:19] == "/api/voice/preview/" {
		storageMode = "local"
	}

	payload := worker.VoiceNoteJobPayload{
		VoiceNoteID:  note.ID,
		OrgID:        orgID,
		Filename:     "file",
		LanguageCode: note.LanguageCode,
		FileURL:      note.FileURL,
		StorageMode:  storageMode,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal job payload: %w", err)
	}

	job := &worker.AIJob{
		JobID:    uuid.New(),
		OrgID:    orgID,
		UserID:   userID,
		TaskType: "voice_note",
		Payload:  payloadBytes,
	}
	if err := uc.queue.Enqueue(ctx, job); err != nil {
		return fmt.Errorf("enqueue voice job: %w", err)
	}

	return nil
}

func (uc *voiceNoteUseCase) List(ctx context.Context, orgID uuid.UUID, f domain.VoiceNoteFilter) ([]domain.VoiceNote, error) {
	return uc.repo.List(ctx, orgID, f)
}

func (uc *voiceNoteUseCase) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.VoiceNote, error) {
	return uc.repo.GetByID(ctx, orgID, id)
}

func (uc *voiceNoteUseCase) ApplyContactUpdates(ctx context.Context, orgID, id uuid.UUID) error {
	note, err := uc.repo.GetByID(ctx, orgID, id)
	if err != nil {
		return fmt.Errorf("get voice note: %w", err)
	}
	if note.ContactID == nil {
		return fmt.Errorf("voice note has no linked contact")
	}

	var updates struct {
		PhoneNumbers    []string `json:"phone_numbers"`
		Emails          []string `json:"emails"`
		Budget          string   `json:"budget"`
		NextMeetingDate string   `json:"next_meeting_date"`
		CompanyName     string   `json:"company_name"`
		Notes           string   `json:"notes"`
	}
	if len(note.ExtractedContactUpdates) > 0 {
		if err := json.Unmarshal(note.ExtractedContactUpdates, &updates); err != nil {
			return fmt.Errorf("parse extracted updates: %w", err)
		}
	}

	contact, err := uc.contactRepo.GetByID(ctx, orgID, *note.ContactID)
	if err != nil {
		return fmt.Errorf("get contact: %w", err)
	}

	changed := false
	if len(updates.PhoneNumbers) > 0 && (contact.Phone == nil || *contact.Phone == "") {
		phone := updates.PhoneNumbers[0]
		contact.Phone = &phone
		changed = true
	}
	if len(updates.Emails) > 0 && (contact.Email == nil || *contact.Email == "") {
		email := updates.Emails[0]
		contact.Email = &email
		changed = true
	}

	if changed {
		if err = uc.contactRepo.Update(ctx, contact); err != nil {
			return fmt.Errorf("update contact: %w", err)
		}
	}

	return nil
}

func (uc *voiceNoteUseCase) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	note, err := uc.repo.GetByID(ctx, orgID, id)
	if err != nil {
		return fmt.Errorf("voice note not found: %w", err)
	}

	// Physical disk garbage collection hook
	if len(note.FileURL) >= 19 && note.FileURL[:19] == "/api/voice/preview/" {
		go func(path string) {
			filename := path[19:]
			physicalPath := filepath.Join("storage", "voice_notes", filename)
			_ = os.Remove(physicalPath) // Asynchronous cleanup
		}(note.FileURL)
	}

	return uc.repo.Delete(ctx, orgID, id)
}

func uploadToR2(_ context.Context, cfg *config.Config, _ []byte, _ string) (string, error) {
	return "", fmt.Errorf("R2 upload not implemented. Configure R2_ACCOUNT_ID, R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY, R2_BUCKET_NAME, R2_PUBLIC_URL in .env and implement this function using the AWS S3-compatible SDK (bucket endpoint: %s.r2.cloudflarestorage.com)", cfg.R2BucketName)
}

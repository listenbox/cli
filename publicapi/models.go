package publicapi

import (
	"encoding/json"
	"fmt"
)

type APIKeyWhoami struct {
	Email  string          `json:"email"`
	Id     ApiKeyID        `json:"id"`
	Name   *NonEmptyString `json:"name,omitempty"`
	Scopes []ApiKeyScope   `json:"scopes"`
	TeamId TeamID          `json:"team_id"`
}

type ApiKeyID = string

type ApiKeyScope string

const (
	ApiKeyScopeTeamRead       ApiKeyScope = "team:read"
	ApiKeyScopeTeamManage     ApiKeyScope = "team:manage"
	ApiKeyScopeShowRead       ApiKeyScope = "show:read"
	ApiKeyScopeShowCreate     ApiKeyScope = "show:create"
	ApiKeyScopeShowUpdate     ApiKeyScope = "show:update"
	ApiKeyScopeEpisodeCreate  ApiKeyScope = "episode:create"
	ApiKeyScopeEpisodeDelete  ApiKeyScope = "episode:delete"
	ApiKeyScopeEpisodeUpdate  ApiKeyScope = "episode:update"
	ApiKeyScopeEpisodePublish ApiKeyScope = "episode:publish"
)

type AssignableTeamRole string

const (
	AssignableTeamRoleWrite AssignableTeamRole = "write"
	AssignableTeamRoleRead  AssignableTeamRole = "read"
)

type CLIAuthorizationApprovedEvent struct {
	TeamId TeamID `json:"team_id"`
	Type   string `json:"type"`
}

type CLIAuthorizationCode = string

type CLIAuthorizationDeniedEvent struct {
	Type string `json:"type"`
}

// CLIAuthorizationEvent is generated from an OpenAPI oneOf schema.
// oneOf variant: CLIAuthorizationPendingEvent
// oneOf variant: CLIAuthorizationApprovedEvent
// oneOf variant: CLIAuthorizationDeniedEvent
// oneOf variant: CLIAuthorizationExpiredEvent
type CLIAuthorizationEvent struct {
	CLIAuthorizationPendingEvent  *CLIAuthorizationPendingEvent
	CLIAuthorizationApprovedEvent *CLIAuthorizationApprovedEvent
	CLIAuthorizationDeniedEvent   *CLIAuthorizationDeniedEvent
	CLIAuthorizationExpiredEvent  *CLIAuthorizationExpiredEvent
}

func (dst *CLIAuthorizationEvent) UnmarshalJSON(data []byte) error {
	var discriminator struct {
		Value string `json:"type"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return fmt.Errorf("decode CLIAuthorizationEvent discriminator: %w", err)
	}
	switch discriminator.Value {
	case "cli.authorization.approved":
		var decoded CLIAuthorizationApprovedEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode CLIAuthorizationEvent as CLIAuthorizationApprovedEvent: %w", err)
		}
		*dst = CLIAuthorizationEvent{CLIAuthorizationApprovedEvent: &decoded}
		return nil
	case "cli.authorization.denied":
		var decoded CLIAuthorizationDeniedEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode CLIAuthorizationEvent as CLIAuthorizationDeniedEvent: %w", err)
		}
		*dst = CLIAuthorizationEvent{CLIAuthorizationDeniedEvent: &decoded}
		return nil
	case "cli.authorization.expired":
		var decoded CLIAuthorizationExpiredEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode CLIAuthorizationEvent as CLIAuthorizationExpiredEvent: %w", err)
		}
		*dst = CLIAuthorizationEvent{CLIAuthorizationExpiredEvent: &decoded}
		return nil
	case "cli.authorization.pending":
		var decoded CLIAuthorizationPendingEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode CLIAuthorizationEvent as CLIAuthorizationPendingEvent: %w", err)
		}
		*dst = CLIAuthorizationEvent{CLIAuthorizationPendingEvent: &decoded}
		return nil
	default:
		return fmt.Errorf("unsupported CLIAuthorizationEvent discriminator %q", discriminator.Value)
	}
}

func (src CLIAuthorizationEvent) MarshalJSON() ([]byte, error) {
	matchCount := 0
	if src.CLIAuthorizationPendingEvent != nil {
		matchCount++
	}
	if src.CLIAuthorizationApprovedEvent != nil {
		matchCount++
	}
	if src.CLIAuthorizationDeniedEvent != nil {
		matchCount++
	}
	if src.CLIAuthorizationExpiredEvent != nil {
		matchCount++
	}
	if matchCount != 1 {
		return nil, fmt.Errorf("CLIAuthorizationEvent must contain exactly one variant, got %d", matchCount)
	}
	if src.CLIAuthorizationPendingEvent != nil {
		return json.Marshal(src.CLIAuthorizationPendingEvent)
	}
	if src.CLIAuthorizationApprovedEvent != nil {
		return json.Marshal(src.CLIAuthorizationApprovedEvent)
	}
	if src.CLIAuthorizationDeniedEvent != nil {
		return json.Marshal(src.CLIAuthorizationDeniedEvent)
	}
	if src.CLIAuthorizationExpiredEvent != nil {
		return json.Marshal(src.CLIAuthorizationExpiredEvent)
	}
	return nil, fmt.Errorf("CLIAuthorizationEvent has no variant")
}

func (src CLIAuthorizationEvent) GetActualInstance() any {
	if src.CLIAuthorizationPendingEvent != nil {
		return src.CLIAuthorizationPendingEvent
	}
	if src.CLIAuthorizationApprovedEvent != nil {
		return src.CLIAuthorizationApprovedEvent
	}
	if src.CLIAuthorizationDeniedEvent != nil {
		return src.CLIAuthorizationDeniedEvent
	}
	if src.CLIAuthorizationExpiredEvent != nil {
		return src.CLIAuthorizationExpiredEvent
	}
	return nil
}

func CLIAuthorizationPendingEventAsCLIAuthorizationEvent(v CLIAuthorizationPendingEvent) CLIAuthorizationEvent {
	return CLIAuthorizationEvent{CLIAuthorizationPendingEvent: &v}
}

func CLIAuthorizationApprovedEventAsCLIAuthorizationEvent(v CLIAuthorizationApprovedEvent) CLIAuthorizationEvent {
	return CLIAuthorizationEvent{CLIAuthorizationApprovedEvent: &v}
}

func CLIAuthorizationDeniedEventAsCLIAuthorizationEvent(v CLIAuthorizationDeniedEvent) CLIAuthorizationEvent {
	return CLIAuthorizationEvent{CLIAuthorizationDeniedEvent: &v}
}

func CLIAuthorizationExpiredEventAsCLIAuthorizationEvent(v CLIAuthorizationExpiredEvent) CLIAuthorizationEvent {
	return CLIAuthorizationEvent{CLIAuthorizationExpiredEvent: &v}
}

type CLIAuthorizationExpiredEvent struct {
	Type string `json:"type"`
}

type CLIAuthorizationPendingEvent struct {
	Credential      string `json:"credential"`
	Type            string `json:"type"`
	VerificationUrl string `json:"verification_url"`
}

type CompleteEpisodeUploadSession struct {
	Publication EpisodePublication `json:"publication"`
}

type CompleteImageUpload struct {
	ObjectKey ImageUploadObjectKey `json:"object_key"`
}

type CompletedEpisodeUploadPart struct {
	Etag       NonEmptyString        `json:"etag"`
	PartNumber MediaUploadPartNumber `json:"part_number"`
	Size       int64                 `json:"size"`
}

type CreateCLIAuthorization struct {
	Scopes []ApiKeyScope `json:"scopes"`
}

type CreateEpisodeUploadSession struct {
	ByteLength   int64                    `json:"byte_length"`
	ContentType  EpisodeUploadContentType `json:"content_type"`
	Description  *NonEmptyString          `json:"description,omitempty"`
	FileName     NonEmptyString           `json:"file_name"`
	ShowSlug     ShowSlug                 `json:"show_slug"`
	SourceSha256 SHA256Hex                `json:"source_sha256"`
	ThumbnailUrl *string                  `json:"thumbnail_url,omitempty"`
	Title        NonEmptyString           `json:"title"`
}

type CreateImageUploadPresign struct {
	ByteLength  int64           `json:"byte_length"`
	ContentType string          `json:"content_type"`
	FileName    *NonEmptyString `json:"file_name,omitempty"`
	ShowId      ShowID          `json:"show_id"`
}

type CreateShow struct {
	Id           ShowID         `json:"id"`
	ImageAssetId *ImageAssetID  `json:"image_asset_id,omitempty"`
	Language     ShowLanguage   `json:"language"`
	Slug         ShowSlug       `json:"slug"`
	SourceKind   ShowSourceKind `json:"source_kind"`
	Title        NonEmptyString `json:"title"`
}

type CreateShowInvitation struct {
	Email string             `json:"email"`
	Role  AssignableTeamRole `json:"role"`
}

type CreateTeamInvitation struct {
	Email string             `json:"email"`
	Role  AssignableTeamRole `json:"role"`
}

type CreatedCLIAuthorization struct {
	Code            CLIAuthorizationCode `json:"code"`
	Credential      string               `json:"credential"`
	VerificationUrl string               `json:"verification_url"`
}

type CreatedImageUploadPresign struct {
	ContentType  string               `json:"content_type"`
	ExpiresAt    int64                `json:"expires_at"`
	ImageAssetId ImageAssetID         `json:"image_asset_id"`
	Method       string               `json:"method"`
	ObjectKey    ImageUploadObjectKey `json:"object_key"`
	PublicUrl    string               `json:"public_url"`
	UploadUrl    string               `json:"upload_url"`
}

type CreatedPublicEpisodeDeletion struct {
	EpisodeDeletionRunId EpisodeDeletionRunID `json:"episode_deletion_run_id"`
}

type CreatedPublicRSSImport struct {
	ImportRunId ImportRunID `json:"import_run_id"`
}

type CreatedPublicShowDeletion struct {
	ShowDeletionRunId ShowDeletionRunID `json:"show_deletion_run_id"`
}

type Episode struct {
	Description   *string                    `json:"description,omitempty"`
	Enclosures    []EpisodeEnclosure         `json:"enclosures"`
	Id            EpisodeID                  `json:"id"`
	Link          *string                    `json:"link,omitempty"`
	Processing    *EpisodeProcessingSnapshot `json:"processing,omitempty"`
	RepublishedAt *UnixMillis                `json:"republished_at,omitempty"`
	ShowId        ShowID                     `json:"show_id"`
	Slug          *NonEmptyString            `json:"slug,omitempty"`
	Status        EpisodeStatus              `json:"status"`
	ThumbnailUrl  *string                    `json:"thumbnail_url,omitempty"`
	Title         NonEmptyString             `json:"title"`
	VideoHlsUrl   *string                    `json:"video_hls_url,omitempty"`
}

type EpisodeDeletionCompletedEvent struct {
	EpisodeDeletionRunId EpisodeDeletionRunID `json:"episode_deletion_run_id"`
	EpisodeId            EpisodeID            `json:"episode_id"`
	Status               string               `json:"status"`
	Type                 string               `json:"type"`
}

// EpisodeDeletionEvent is generated from an OpenAPI oneOf schema.
// oneOf variant: EpisodeDeletionProgressEvent
// oneOf variant: EpisodeDeletionTerminalEvent
type EpisodeDeletionEvent struct {
	EpisodeDeletionProgressEvent *EpisodeDeletionProgressEvent
	EpisodeDeletionTerminalEvent *EpisodeDeletionTerminalEvent
}

func (dst *EpisodeDeletionEvent) UnmarshalJSON(data []byte) error {
	var discriminator struct {
		Value string `json:"type"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return fmt.Errorf("decode EpisodeDeletionEvent discriminator: %w", err)
	}
	switch discriminator.Value {
	case "progress":
		var decoded EpisodeDeletionProgressEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode EpisodeDeletionEvent as EpisodeDeletionProgressEvent: %w", err)
		}
		*dst = EpisodeDeletionEvent{EpisodeDeletionProgressEvent: &decoded}
		return nil
	case "terminal":
		var decoded EpisodeDeletionTerminalEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode EpisodeDeletionEvent as EpisodeDeletionTerminalEvent: %w", err)
		}
		*dst = EpisodeDeletionEvent{EpisodeDeletionTerminalEvent: &decoded}
		return nil
	default:
		return fmt.Errorf("unsupported EpisodeDeletionEvent discriminator %q", discriminator.Value)
	}
}

func (src EpisodeDeletionEvent) MarshalJSON() ([]byte, error) {
	matchCount := 0
	if src.EpisodeDeletionProgressEvent != nil {
		matchCount++
	}
	if src.EpisodeDeletionTerminalEvent != nil {
		matchCount++
	}
	if matchCount != 1 {
		return nil, fmt.Errorf("EpisodeDeletionEvent must contain exactly one variant, got %d", matchCount)
	}
	if src.EpisodeDeletionProgressEvent != nil {
		return json.Marshal(src.EpisodeDeletionProgressEvent)
	}
	if src.EpisodeDeletionTerminalEvent != nil {
		return json.Marshal(src.EpisodeDeletionTerminalEvent)
	}
	return nil, fmt.Errorf("EpisodeDeletionEvent has no variant")
}

func (src EpisodeDeletionEvent) GetActualInstance() any {
	if src.EpisodeDeletionProgressEvent != nil {
		return src.EpisodeDeletionProgressEvent
	}
	if src.EpisodeDeletionTerminalEvent != nil {
		return src.EpisodeDeletionTerminalEvent
	}
	return nil
}

func EpisodeDeletionProgressEventAsEpisodeDeletionEvent(v EpisodeDeletionProgressEvent) EpisodeDeletionEvent {
	return EpisodeDeletionEvent{EpisodeDeletionProgressEvent: &v}
}

func EpisodeDeletionTerminalEventAsEpisodeDeletionEvent(v EpisodeDeletionTerminalEvent) EpisodeDeletionEvent {
	return EpisodeDeletionEvent{EpisodeDeletionTerminalEvent: &v}
}

type EpisodeDeletionFailedEvent struct {
	EpisodeDeletionRunId EpisodeDeletionRunID `json:"episode_deletion_run_id"`
	ErrorCode            NonEmptyString       `json:"error_code"`
	ErrorMessage         NonEmptyString       `json:"error_message"`
	Status               string               `json:"status"`
	Type                 string               `json:"type"`
}

type EpisodeDeletionProgressEvent struct {
	CurrentStep          NonEmptyString       `json:"current_step"`
	EpisodeDeletionRunId EpisodeDeletionRunID `json:"episode_deletion_run_id"`
	Percent              int32                `json:"percent"`
	Status               string               `json:"status"`
	Type                 string               `json:"type"`
}

type EpisodeDeletionRunID = string

// EpisodeDeletionTerminalEvent is generated from an OpenAPI oneOf schema.
// oneOf variant: EpisodeDeletionCompletedEvent
// oneOf variant: EpisodeDeletionFailedEvent
type EpisodeDeletionTerminalEvent struct {
	EpisodeDeletionCompletedEvent *EpisodeDeletionCompletedEvent
	EpisodeDeletionFailedEvent    *EpisodeDeletionFailedEvent
}

func (dst *EpisodeDeletionTerminalEvent) UnmarshalJSON(data []byte) error {
	var discriminator struct {
		Value string `json:"status"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return fmt.Errorf("decode EpisodeDeletionTerminalEvent discriminator: %w", err)
	}
	switch discriminator.Value {
	case "completed":
		var decoded EpisodeDeletionCompletedEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode EpisodeDeletionTerminalEvent as EpisodeDeletionCompletedEvent: %w", err)
		}
		*dst = EpisodeDeletionTerminalEvent{EpisodeDeletionCompletedEvent: &decoded}
		return nil
	case "failed":
		var decoded EpisodeDeletionFailedEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode EpisodeDeletionTerminalEvent as EpisodeDeletionFailedEvent: %w", err)
		}
		*dst = EpisodeDeletionTerminalEvent{EpisodeDeletionFailedEvent: &decoded}
		return nil
	default:
		return fmt.Errorf("unsupported EpisodeDeletionTerminalEvent discriminator %q", discriminator.Value)
	}
}

func (src EpisodeDeletionTerminalEvent) MarshalJSON() ([]byte, error) {
	matchCount := 0
	if src.EpisodeDeletionCompletedEvent != nil {
		matchCount++
	}
	if src.EpisodeDeletionFailedEvent != nil {
		matchCount++
	}
	if matchCount != 1 {
		return nil, fmt.Errorf("EpisodeDeletionTerminalEvent must contain exactly one variant, got %d", matchCount)
	}
	if src.EpisodeDeletionCompletedEvent != nil {
		return json.Marshal(src.EpisodeDeletionCompletedEvent)
	}
	if src.EpisodeDeletionFailedEvent != nil {
		return json.Marshal(src.EpisodeDeletionFailedEvent)
	}
	return nil, fmt.Errorf("EpisodeDeletionTerminalEvent has no variant")
}

func (src EpisodeDeletionTerminalEvent) GetActualInstance() any {
	if src.EpisodeDeletionCompletedEvent != nil {
		return src.EpisodeDeletionCompletedEvent
	}
	if src.EpisodeDeletionFailedEvent != nil {
		return src.EpisodeDeletionFailedEvent
	}
	return nil
}

func EpisodeDeletionCompletedEventAsEpisodeDeletionTerminalEvent(v EpisodeDeletionCompletedEvent) EpisodeDeletionTerminalEvent {
	return EpisodeDeletionTerminalEvent{EpisodeDeletionCompletedEvent: &v}
}

func EpisodeDeletionFailedEventAsEpisodeDeletionTerminalEvent(v EpisodeDeletionFailedEvent) EpisodeDeletionTerminalEvent {
	return EpisodeDeletionTerminalEvent{EpisodeDeletionFailedEvent: &v}
}

type EpisodeEnclosure struct {
	ByteLength      int64                  `json:"byte_length"`
	ContentType     NonEmptyString         `json:"content_type"`
	DurationSeconds int64                  `json:"duration_seconds"`
	FeedId          FeedID                 `json:"feed_id"`
	Format          EpisodeEnclosureFormat `json:"format"`
	Url             string                 `json:"url"`
}

type EpisodeEnclosureFormat string

const (
	EpisodeEnclosureFormatMp3  EpisodeEnclosureFormat = "mp3"
	EpisodeEnclosureFormatM4a  EpisodeEnclosureFormat = "m4a"
	EpisodeEnclosureFormatWav  EpisodeEnclosureFormat = "wav"
	EpisodeEnclosureFormatFlac EpisodeEnclosureFormat = "flac"
	EpisodeEnclosureFormatMp4  EpisodeEnclosureFormat = "mp4"
)

type EpisodeID = string

type EpisodeListItem struct {
	AudioUrl        *string                    `json:"audio_url,omitempty"`
	Description     *string                    `json:"description,omitempty"`
	DurationSeconds *int64                     `json:"duration_seconds,omitempty"`
	Id              EpisodeID                  `json:"id"`
	Processing      *EpisodeProcessingSnapshot `json:"processing,omitempty"`
	RepublishedAt   *UnixMillis                `json:"republished_at,omitempty"`
	ShowId          ShowID                     `json:"show_id"`
	Slug            *NonEmptyString            `json:"slug,omitempty"`
	Status          EpisodeStatus              `json:"status"`
	ThumbnailUrl    *string                    `json:"thumbnail_url,omitempty"`
	Title           NonEmptyString             `json:"title"`
	VideoHlsUrl     *string                    `json:"video_hls_url,omitempty"`
}

type EpisodePage struct {
	Episodes   []EpisodeListItem  `json:"episodes"`
	NextCursor *EpisodePageCursor `json:"next_cursor,omitempty"`
}

type EpisodePageCursor = string

type EpisodePageLimit = int32

// EpisodeProcessingEvent is generated from an OpenAPI oneOf schema.
// oneOf variant: EpisodeProcessingProgressEvent
// oneOf variant: EpisodeProcessingSafeHandoffEvent
// oneOf variant: EpisodeProcessingFailedEvent
type EpisodeProcessingEvent struct {
	EpisodeProcessingProgressEvent    *EpisodeProcessingProgressEvent
	EpisodeProcessingSafeHandoffEvent *EpisodeProcessingSafeHandoffEvent
	EpisodeProcessingFailedEvent      *EpisodeProcessingFailedEvent
}

func (dst *EpisodeProcessingEvent) UnmarshalJSON(data []byte) error {
	var discriminator struct {
		Value string `json:"type"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return fmt.Errorf("decode EpisodeProcessingEvent discriminator: %w", err)
	}
	switch discriminator.Value {
	case "episode.processing.failed":
		var decoded EpisodeProcessingFailedEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode EpisodeProcessingEvent as EpisodeProcessingFailedEvent: %w", err)
		}
		*dst = EpisodeProcessingEvent{EpisodeProcessingFailedEvent: &decoded}
		return nil
	case "episode.processing.progress":
		var decoded EpisodeProcessingProgressEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode EpisodeProcessingEvent as EpisodeProcessingProgressEvent: %w", err)
		}
		*dst = EpisodeProcessingEvent{EpisodeProcessingProgressEvent: &decoded}
		return nil
	case "episode.processing.safe_handoff":
		var decoded EpisodeProcessingSafeHandoffEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode EpisodeProcessingEvent as EpisodeProcessingSafeHandoffEvent: %w", err)
		}
		*dst = EpisodeProcessingEvent{EpisodeProcessingSafeHandoffEvent: &decoded}
		return nil
	default:
		return fmt.Errorf("unsupported EpisodeProcessingEvent discriminator %q", discriminator.Value)
	}
}

func (src EpisodeProcessingEvent) MarshalJSON() ([]byte, error) {
	matchCount := 0
	if src.EpisodeProcessingProgressEvent != nil {
		matchCount++
	}
	if src.EpisodeProcessingSafeHandoffEvent != nil {
		matchCount++
	}
	if src.EpisodeProcessingFailedEvent != nil {
		matchCount++
	}
	if matchCount != 1 {
		return nil, fmt.Errorf("EpisodeProcessingEvent must contain exactly one variant, got %d", matchCount)
	}
	if src.EpisodeProcessingProgressEvent != nil {
		return json.Marshal(src.EpisodeProcessingProgressEvent)
	}
	if src.EpisodeProcessingSafeHandoffEvent != nil {
		return json.Marshal(src.EpisodeProcessingSafeHandoffEvent)
	}
	if src.EpisodeProcessingFailedEvent != nil {
		return json.Marshal(src.EpisodeProcessingFailedEvent)
	}
	return nil, fmt.Errorf("EpisodeProcessingEvent has no variant")
}

func (src EpisodeProcessingEvent) GetActualInstance() any {
	if src.EpisodeProcessingProgressEvent != nil {
		return src.EpisodeProcessingProgressEvent
	}
	if src.EpisodeProcessingSafeHandoffEvent != nil {
		return src.EpisodeProcessingSafeHandoffEvent
	}
	if src.EpisodeProcessingFailedEvent != nil {
		return src.EpisodeProcessingFailedEvent
	}
	return nil
}

func EpisodeProcessingProgressEventAsEpisodeProcessingEvent(v EpisodeProcessingProgressEvent) EpisodeProcessingEvent {
	return EpisodeProcessingEvent{EpisodeProcessingProgressEvent: &v}
}

func EpisodeProcessingSafeHandoffEventAsEpisodeProcessingEvent(v EpisodeProcessingSafeHandoffEvent) EpisodeProcessingEvent {
	return EpisodeProcessingEvent{EpisodeProcessingSafeHandoffEvent: &v}
}

func EpisodeProcessingFailedEventAsEpisodeProcessingEvent(v EpisodeProcessingFailedEvent) EpisodeProcessingEvent {
	return EpisodeProcessingEvent{EpisodeProcessingFailedEvent: &v}
}

type EpisodeProcessingFailedEvent struct {
	EpisodeId    EpisodeID       `json:"episode_id"`
	ErrorCode    NonEmptyString  `json:"error_code"`
	ErrorMessage NonEmptyString  `json:"error_message"`
	RunId        ProcessingRunID `json:"run_id"`
	Status       string          `json:"status"`
	Type         string          `json:"type"`
}

type EpisodeProcessingProgressEvent struct {
	CurrentStep NonEmptyString          `json:"current_step"`
	Percent     int32                   `json:"percent"`
	RunId       ProcessingRunID         `json:"run_id"`
	Status      EpisodeProcessingStatus `json:"status"`
	Type        string                  `json:"type"`
}

type EpisodeProcessingSafeHandoffEvent struct {
	EpisodeId      EpisodeID       `json:"episode_id"`
	MediaVersionId MediaVersionID  `json:"media_version_id"`
	RunId          ProcessingRunID `json:"run_id"`
	Status         string          `json:"status"`
	Type           string          `json:"type"`
	YoutubeVideoId *NonEmptyString `json:"youtube_video_id,omitempty"`
}

type EpisodeProcessingSnapshot struct {
	CurrentStep  NonEmptyString          `json:"current_step"`
	ErrorCode    *NonEmptyString         `json:"error_code,omitempty"`
	ErrorMessage *NonEmptyString         `json:"error_message,omitempty"`
	Percent      int32                   `json:"percent"`
	RunId        ProcessingRunID         `json:"run_id"`
	Status       EpisodeProcessingStatus `json:"status"`
}

type EpisodeProcessingStatus string

const (
	EpisodeProcessingStatusQueued     EpisodeProcessingStatus = "queued"
	EpisodeProcessingStatusProcessing EpisodeProcessingStatus = "processing"
	EpisodeProcessingStatusCompleted  EpisodeProcessingStatus = "completed"
	EpisodeProcessingStatusFailed     EpisodeProcessingStatus = "failed"
)

type EpisodePublication string

const (
	EpisodePublicationDraft   EpisodePublication = "draft"
	EpisodePublicationPublish EpisodePublication = "publish"
)

type EpisodeStatus string

const (
	EpisodeStatusDraft     EpisodeStatus = "draft"
	EpisodeStatusPublished EpisodeStatus = "published"
)

type EpisodeUploadContentType string

const (
	EpisodeUploadContentTypeAudioMpeg      EpisodeUploadContentType = "audio/mpeg"
	EpisodeUploadContentTypeAudioMp4       EpisodeUploadContentType = "audio/mp4"
	EpisodeUploadContentTypeAudioWav       EpisodeUploadContentType = "audio/wav"
	EpisodeUploadContentTypeAudioFlac      EpisodeUploadContentType = "audio/flac"
	EpisodeUploadContentTypeVideoMp4       EpisodeUploadContentType = "video/mp4"
	EpisodeUploadContentTypeVideoQuicktime EpisodeUploadContentType = "video/quicktime"
)

type EpisodeUploadSession struct {
	ByteLength            int64                        `json:"byte_length"`
	CompletedParts        []CompletedEpisodeUploadPart `json:"completed_parts"`
	ContentType           EpisodeUploadContentType     `json:"content_type"`
	Description           *string                      `json:"description,omitempty"`
	EpisodeId             *EpisodeID                   `json:"episode_id,omitempty"`
	ExpiresAt             UnixMillis                   `json:"expires_at"`
	FileName              NonEmptyString               `json:"file_name"`
	MaxParallelism        int32                        `json:"max_parallelism"`
	ObjectKey             R2ObjectKey                  `json:"object_key"`
	PartSize              int64                        `json:"part_size"`
	Phase                 EpisodeUploadSessionPhase    `json:"phase"`
	Processing            *EpisodeProcessingSnapshot   `json:"processing,omitempty"`
	ShowId                ShowID                       `json:"show_id"`
	SourceDurationSeconds *int64                       `json:"source_duration_seconds,omitempty"`
	SourceSha256          SHA256Hex                    `json:"source_sha256"`
	TeamId                TeamID                       `json:"team_id"`
	ThumbnailUrl          *string                      `json:"thumbnail_url,omitempty"`
	Title                 NonEmptyString               `json:"title"`
	UploadSessionId       UploadSessionID              `json:"upload_session_id"`
}

type EpisodeUploadSessionPhase string

const (
	EpisodeUploadSessionPhaseCreated    EpisodeUploadSessionPhase = "created"
	EpisodeUploadSessionPhaseUploading  EpisodeUploadSessionPhase = "uploading"
	EpisodeUploadSessionPhaseProcessing EpisodeUploadSessionPhase = "processing"
	EpisodeUploadSessionPhaseCompleted  EpisodeUploadSessionPhase = "completed"
	EpisodeUploadSessionPhaseFailed     EpisodeUploadSessionPhase = "failed"
	EpisodeUploadSessionPhaseExpired    EpisodeUploadSessionPhase = "expired"
)

type FeedID = string

type ImageAsset struct {
	ByteLength      int64        `json:"byte_length"`
	ContentType     string       `json:"content_type"`
	HasTransparency bool         `json:"has_transparency"`
	HeightPixels    int64        `json:"height_pixels"`
	Id              ImageAssetID `json:"id"`
	PublicUrl       string       `json:"public_url"`
	WidthPixels     int64        `json:"width_pixels"`
}

type ImageAssetID = string

type ImageUploadObjectKey = string

type ImportRSSRequest struct {
	Slug      *ShowSlug `json:"slug,omitempty"`
	SourceUrl string    `json:"source_url"`
}

type ImportRunID = string

type MediaUploadPartNumber = int32

type MediaVersionID = string

type NonEmptyString = string

type OpenAPIJSONDocument struct {
}

type OpenAPIYAMLDocument = string

type PatchEpisodeUploadSession struct {
	Description  *string `json:"description,omitempty"`
	ThumbnailUrl *string `json:"thumbnail_url,omitempty"`
}

type PendingShowInvitation struct {
	Email     string             `json:"email"`
	ExpiresAt UnixMillis         `json:"expires_at"`
	Id        ShowInvitationID   `json:"id"`
	InvitedAt UnixMillis         `json:"invited_at"`
	Role      AssignableTeamRole `json:"role"`
}

type PendingTeamInvitation struct {
	Email     string             `json:"email"`
	ExpiresAt UnixMillis         `json:"expires_at"`
	Id        TeamInvitationID   `json:"id"`
	InvitedAt UnixMillis         `json:"invited_at"`
	Role      AssignableTeamRole `json:"role"`
}

type PresignEpisodeUploadSessionParts struct {
	PartNumbers []MediaUploadPartNumber `json:"part_numbers"`
}

type PresignedEpisodeUploadPart struct {
	ExpiresAt  UnixMillis            `json:"expires_at"`
	Method     string                `json:"method"`
	PartNumber MediaUploadPartNumber `json:"part_number"`
	UploadUrl  string                `json:"upload_url"`
}

type PresignedEpisodeUploadParts struct {
	Parts           []PresignedEpisodeUploadPart `json:"parts"`
	UploadSessionId UploadSessionID              `json:"upload_session_id"`
}

type ProcessingRunID = string

type PublicRSSImportCompletedTerminalEvent struct {
	FeedId   FeedID   `json:"feed_id"`
	ShowId   ShowID   `json:"show_id"`
	ShowSlug ShowSlug `json:"show_slug"`
	Status   string   `json:"status"`
	TeamId   TeamID   `json:"team_id"`
	Type     string   `json:"type"`
}

// PublicRSSImportEvent is generated from an OpenAPI oneOf schema.
// oneOf variant: RSSImportProgressEvent
// oneOf variant: PublicRSSImportTerminalEvent
type PublicRSSImportEvent struct {
	RSSImportProgressEvent       *RSSImportProgressEvent
	PublicRSSImportTerminalEvent *PublicRSSImportTerminalEvent
}

func (dst *PublicRSSImportEvent) UnmarshalJSON(data []byte) error {
	var discriminator struct {
		Value string `json:"type"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return fmt.Errorf("decode PublicRSSImportEvent discriminator: %w", err)
	}
	switch discriminator.Value {
	case "progress":
		var decoded RSSImportProgressEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode PublicRSSImportEvent as RSSImportProgressEvent: %w", err)
		}
		*dst = PublicRSSImportEvent{RSSImportProgressEvent: &decoded}
		return nil
	case "terminal":
		var decoded PublicRSSImportTerminalEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode PublicRSSImportEvent as PublicRSSImportTerminalEvent: %w", err)
		}
		*dst = PublicRSSImportEvent{PublicRSSImportTerminalEvent: &decoded}
		return nil
	default:
		return fmt.Errorf("unsupported PublicRSSImportEvent discriminator %q", discriminator.Value)
	}
}

func (src PublicRSSImportEvent) MarshalJSON() ([]byte, error) {
	matchCount := 0
	if src.RSSImportProgressEvent != nil {
		matchCount++
	}
	if src.PublicRSSImportTerminalEvent != nil {
		matchCount++
	}
	if matchCount != 1 {
		return nil, fmt.Errorf("PublicRSSImportEvent must contain exactly one variant, got %d", matchCount)
	}
	if src.RSSImportProgressEvent != nil {
		return json.Marshal(src.RSSImportProgressEvent)
	}
	if src.PublicRSSImportTerminalEvent != nil {
		return json.Marshal(src.PublicRSSImportTerminalEvent)
	}
	return nil, fmt.Errorf("PublicRSSImportEvent has no variant")
}

func (src PublicRSSImportEvent) GetActualInstance() any {
	if src.RSSImportProgressEvent != nil {
		return src.RSSImportProgressEvent
	}
	if src.PublicRSSImportTerminalEvent != nil {
		return src.PublicRSSImportTerminalEvent
	}
	return nil
}

func RSSImportProgressEventAsPublicRSSImportEvent(v RSSImportProgressEvent) PublicRSSImportEvent {
	return PublicRSSImportEvent{RSSImportProgressEvent: &v}
}

func PublicRSSImportTerminalEventAsPublicRSSImportEvent(v PublicRSSImportTerminalEvent) PublicRSSImportEvent {
	return PublicRSSImportEvent{PublicRSSImportTerminalEvent: &v}
}

// PublicRSSImportTerminalEvent is generated from an OpenAPI oneOf schema.
// oneOf variant: PublicRSSImportCompletedTerminalEvent
// oneOf variant: RSSImportFailedTerminalEvent
// oneOf variant: RSSImportCancelledTerminalEvent
type PublicRSSImportTerminalEvent struct {
	PublicRSSImportCompletedTerminalEvent *PublicRSSImportCompletedTerminalEvent
	RSSImportFailedTerminalEvent          *RSSImportFailedTerminalEvent
	RSSImportCancelledTerminalEvent       *RSSImportCancelledTerminalEvent
}

func (dst *PublicRSSImportTerminalEvent) UnmarshalJSON(data []byte) error {
	var discriminator struct {
		Value string `json:"status"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return fmt.Errorf("decode PublicRSSImportTerminalEvent discriminator: %w", err)
	}
	switch discriminator.Value {
	case "cancelled":
		var decoded RSSImportCancelledTerminalEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode PublicRSSImportTerminalEvent as RSSImportCancelledTerminalEvent: %w", err)
		}
		*dst = PublicRSSImportTerminalEvent{RSSImportCancelledTerminalEvent: &decoded}
		return nil
	case "completed":
		var decoded PublicRSSImportCompletedTerminalEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode PublicRSSImportTerminalEvent as PublicRSSImportCompletedTerminalEvent: %w", err)
		}
		*dst = PublicRSSImportTerminalEvent{PublicRSSImportCompletedTerminalEvent: &decoded}
		return nil
	case "failed":
		var decoded RSSImportFailedTerminalEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode PublicRSSImportTerminalEvent as RSSImportFailedTerminalEvent: %w", err)
		}
		*dst = PublicRSSImportTerminalEvent{RSSImportFailedTerminalEvent: &decoded}
		return nil
	default:
		return fmt.Errorf("unsupported PublicRSSImportTerminalEvent discriminator %q", discriminator.Value)
	}
}

func (src PublicRSSImportTerminalEvent) MarshalJSON() ([]byte, error) {
	matchCount := 0
	if src.PublicRSSImportCompletedTerminalEvent != nil {
		matchCount++
	}
	if src.RSSImportFailedTerminalEvent != nil {
		matchCount++
	}
	if src.RSSImportCancelledTerminalEvent != nil {
		matchCount++
	}
	if matchCount != 1 {
		return nil, fmt.Errorf("PublicRSSImportTerminalEvent must contain exactly one variant, got %d", matchCount)
	}
	if src.PublicRSSImportCompletedTerminalEvent != nil {
		return json.Marshal(src.PublicRSSImportCompletedTerminalEvent)
	}
	if src.RSSImportFailedTerminalEvent != nil {
		return json.Marshal(src.RSSImportFailedTerminalEvent)
	}
	if src.RSSImportCancelledTerminalEvent != nil {
		return json.Marshal(src.RSSImportCancelledTerminalEvent)
	}
	return nil, fmt.Errorf("PublicRSSImportTerminalEvent has no variant")
}

func (src PublicRSSImportTerminalEvent) GetActualInstance() any {
	if src.PublicRSSImportCompletedTerminalEvent != nil {
		return src.PublicRSSImportCompletedTerminalEvent
	}
	if src.RSSImportFailedTerminalEvent != nil {
		return src.RSSImportFailedTerminalEvent
	}
	if src.RSSImportCancelledTerminalEvent != nil {
		return src.RSSImportCancelledTerminalEvent
	}
	return nil
}

func PublicRSSImportCompletedTerminalEventAsPublicRSSImportTerminalEvent(v PublicRSSImportCompletedTerminalEvent) PublicRSSImportTerminalEvent {
	return PublicRSSImportTerminalEvent{PublicRSSImportCompletedTerminalEvent: &v}
}

func RSSImportFailedTerminalEventAsPublicRSSImportTerminalEvent(v RSSImportFailedTerminalEvent) PublicRSSImportTerminalEvent {
	return PublicRSSImportTerminalEvent{RSSImportFailedTerminalEvent: &v}
}

func RSSImportCancelledTerminalEventAsPublicRSSImportTerminalEvent(v RSSImportCancelledTerminalEvent) PublicRSSImportTerminalEvent {
	return PublicRSSImportTerminalEvent{RSSImportCancelledTerminalEvent: &v}
}

type R2ObjectKey = string

type RSSImportCancelledTerminalEvent struct {
	Status string `json:"status"`
	Type   string `json:"type"`
}

type RSSImportEntitlementError struct {
	Code                string                   `json:"code"`
	CurrentEntitlement  VideoDeliveryEntitlement `json:"current_entitlement"`
	MediaKind           RSSImportMediaKind       `json:"media_kind"`
	PricingUrl          string                   `json:"pricing_url"`
	RequiredEntitlement VideoDeliveryEntitlement `json:"required_entitlement"`
}

type RSSImportFailedTerminalEvent struct {
	ErrorCode            NonEmptyString  `json:"error_code"`
	ErrorMessage         NonEmptyString  `json:"error_message"`
	HttpStatus           *int64          `json:"http_status,omitempty"`
	LimitSeconds         *int64          `json:"limit_seconds,omitempty"`
	PricingUrl           *string         `json:"pricing_url,omitempty"`
	RemainingSeconds     *int64          `json:"remaining_seconds,omitempty"`
	RequestedSeconds     *int64          `json:"requested_seconds,omitempty"`
	RequiredNextPlan     *NonEmptyString `json:"required_next_plan,omitempty"`
	ReservedSeconds      *int64          `json:"reserved_seconds,omitempty"`
	RetainedSeconds      *int64          `json:"retained_seconds,omitempty"`
	SalesContactRequired *bool           `json:"sales_contact_required,omitempty"`
	Status               string          `json:"status"`
	Type                 string          `json:"type"`
}

type RSSImportMediaKind string

const (
	RSSImportMediaKindAudio RSSImportMediaKind = "audio"
	RSSImportMediaKindVideo RSSImportMediaKind = "video"
)

type RSSImportProgressEvent struct {
	CurrentStep NonEmptyString `json:"current_step"`
	Percent     int32          `json:"percent"`
	Type        string         `json:"type"`
}

type SHA256Hex = string

type Show struct {
	Id         ShowID         `json:"id"`
	Language   ShowLanguage   `json:"language"`
	Slug       ShowSlug       `json:"slug"`
	SourceKind ShowSourceKind `json:"source_kind"`
	TeamId     TeamID         `json:"team_id"`
	Title      NonEmptyString `json:"title"`
}

type ShowAccessSource string

const (
	ShowAccessSourceTeam ShowAccessSource = "team"
	ShowAccessSourceShow ShowAccessSource = "show"
)

type ShowDeletionCompletedEvent struct {
	ShowDeletionRunId ShowDeletionRunID `json:"show_deletion_run_id"`
	ShowSlug          ShowSlug          `json:"show_slug"`
	Status            string            `json:"status"`
	Type              string            `json:"type"`
}

// ShowDeletionEvent is generated from an OpenAPI oneOf schema.
// oneOf variant: ShowDeletionProgressEvent
// oneOf variant: ShowDeletionTerminalEvent
type ShowDeletionEvent struct {
	ShowDeletionProgressEvent *ShowDeletionProgressEvent
	ShowDeletionTerminalEvent *ShowDeletionTerminalEvent
}

func (dst *ShowDeletionEvent) UnmarshalJSON(data []byte) error {
	var discriminator struct {
		Value string `json:"type"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return fmt.Errorf("decode ShowDeletionEvent discriminator: %w", err)
	}
	switch discriminator.Value {
	case "progress":
		var decoded ShowDeletionProgressEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode ShowDeletionEvent as ShowDeletionProgressEvent: %w", err)
		}
		*dst = ShowDeletionEvent{ShowDeletionProgressEvent: &decoded}
		return nil
	case "terminal":
		var decoded ShowDeletionTerminalEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode ShowDeletionEvent as ShowDeletionTerminalEvent: %w", err)
		}
		*dst = ShowDeletionEvent{ShowDeletionTerminalEvent: &decoded}
		return nil
	default:
		return fmt.Errorf("unsupported ShowDeletionEvent discriminator %q", discriminator.Value)
	}
}

func (src ShowDeletionEvent) MarshalJSON() ([]byte, error) {
	matchCount := 0
	if src.ShowDeletionProgressEvent != nil {
		matchCount++
	}
	if src.ShowDeletionTerminalEvent != nil {
		matchCount++
	}
	if matchCount != 1 {
		return nil, fmt.Errorf("ShowDeletionEvent must contain exactly one variant, got %d", matchCount)
	}
	if src.ShowDeletionProgressEvent != nil {
		return json.Marshal(src.ShowDeletionProgressEvent)
	}
	if src.ShowDeletionTerminalEvent != nil {
		return json.Marshal(src.ShowDeletionTerminalEvent)
	}
	return nil, fmt.Errorf("ShowDeletionEvent has no variant")
}

func (src ShowDeletionEvent) GetActualInstance() any {
	if src.ShowDeletionProgressEvent != nil {
		return src.ShowDeletionProgressEvent
	}
	if src.ShowDeletionTerminalEvent != nil {
		return src.ShowDeletionTerminalEvent
	}
	return nil
}

func ShowDeletionProgressEventAsShowDeletionEvent(v ShowDeletionProgressEvent) ShowDeletionEvent {
	return ShowDeletionEvent{ShowDeletionProgressEvent: &v}
}

func ShowDeletionTerminalEventAsShowDeletionEvent(v ShowDeletionTerminalEvent) ShowDeletionEvent {
	return ShowDeletionEvent{ShowDeletionTerminalEvent: &v}
}

type ShowDeletionFailedEvent struct {
	ErrorCode         NonEmptyString    `json:"error_code"`
	ErrorMessage      NonEmptyString    `json:"error_message"`
	ShowDeletionRunId ShowDeletionRunID `json:"show_deletion_run_id"`
	Status            string            `json:"status"`
	Type              string            `json:"type"`
}

type ShowDeletionProgressEvent struct {
	CurrentStep       NonEmptyString    `json:"current_step"`
	Percent           int32             `json:"percent"`
	ShowDeletionRunId ShowDeletionRunID `json:"show_deletion_run_id"`
	Status            string            `json:"status"`
	Type              string            `json:"type"`
}

type ShowDeletionRunID = string

// ShowDeletionTerminalEvent is generated from an OpenAPI oneOf schema.
// oneOf variant: ShowDeletionCompletedEvent
// oneOf variant: ShowDeletionFailedEvent
type ShowDeletionTerminalEvent struct {
	ShowDeletionCompletedEvent *ShowDeletionCompletedEvent
	ShowDeletionFailedEvent    *ShowDeletionFailedEvent
}

func (dst *ShowDeletionTerminalEvent) UnmarshalJSON(data []byte) error {
	var discriminator struct {
		Value string `json:"status"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return fmt.Errorf("decode ShowDeletionTerminalEvent discriminator: %w", err)
	}
	switch discriminator.Value {
	case "completed":
		var decoded ShowDeletionCompletedEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode ShowDeletionTerminalEvent as ShowDeletionCompletedEvent: %w", err)
		}
		*dst = ShowDeletionTerminalEvent{ShowDeletionCompletedEvent: &decoded}
		return nil
	case "failed":
		var decoded ShowDeletionFailedEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("decode ShowDeletionTerminalEvent as ShowDeletionFailedEvent: %w", err)
		}
		*dst = ShowDeletionTerminalEvent{ShowDeletionFailedEvent: &decoded}
		return nil
	default:
		return fmt.Errorf("unsupported ShowDeletionTerminalEvent discriminator %q", discriminator.Value)
	}
}

func (src ShowDeletionTerminalEvent) MarshalJSON() ([]byte, error) {
	matchCount := 0
	if src.ShowDeletionCompletedEvent != nil {
		matchCount++
	}
	if src.ShowDeletionFailedEvent != nil {
		matchCount++
	}
	if matchCount != 1 {
		return nil, fmt.Errorf("ShowDeletionTerminalEvent must contain exactly one variant, got %d", matchCount)
	}
	if src.ShowDeletionCompletedEvent != nil {
		return json.Marshal(src.ShowDeletionCompletedEvent)
	}
	if src.ShowDeletionFailedEvent != nil {
		return json.Marshal(src.ShowDeletionFailedEvent)
	}
	return nil, fmt.Errorf("ShowDeletionTerminalEvent has no variant")
}

func (src ShowDeletionTerminalEvent) GetActualInstance() any {
	if src.ShowDeletionCompletedEvent != nil {
		return src.ShowDeletionCompletedEvent
	}
	if src.ShowDeletionFailedEvent != nil {
		return src.ShowDeletionFailedEvent
	}
	return nil
}

func ShowDeletionCompletedEventAsShowDeletionTerminalEvent(v ShowDeletionCompletedEvent) ShowDeletionTerminalEvent {
	return ShowDeletionTerminalEvent{ShowDeletionCompletedEvent: &v}
}

func ShowDeletionFailedEventAsShowDeletionTerminalEvent(v ShowDeletionFailedEvent) ShowDeletionTerminalEvent {
	return ShowDeletionTerminalEvent{ShowDeletionFailedEvent: &v}
}

type ShowID = string

type ShowInvitationID = string

type ShowLanguage = string

type ShowMember struct {
	AccessSource ShowAccessSource `json:"access_source"`
	JoinedAt     UnixMillis       `json:"joined_at"`
	Role         TeamRole         `json:"role"`
	User         User             `json:"user"`
}

type ShowMemberCollection struct {
	Invitations []PendingShowInvitation `json:"invitations"`
	Members     []ShowMember            `json:"members"`
}

type ShowSlug = string

type ShowSourceKind string

const (
	ShowSourceKindAudio ShowSourceKind = "audio"
	ShowSourceKindVideo ShowSourceKind = "video"
)

type TeamID = string

type TeamInvitationID = string

type TeamMember struct {
	JoinedAt UnixMillis `json:"joined_at"`
	Role     TeamRole   `json:"role"`
	User     User       `json:"user"`
}

type TeamMemberCollection struct {
	Invitations []PendingTeamInvitation `json:"invitations"`
	Members     []TeamMember            `json:"members"`
}

type TeamRole string

const (
	TeamRoleOwner TeamRole = "owner"
	TeamRoleWrite TeamRole = "write"
	TeamRoleRead  TeamRole = "read"
)

type UnixMillis = int64

type UpdateShowMemberRole struct {
	Role AssignableTeamRole `json:"role"`
}

type UpdateTeamMemberRole struct {
	Role AssignableTeamRole `json:"role"`
}

type UploadSessionID = string

type User struct {
	Email      string          `json:"email"`
	Id         UserID          `json:"id"`
	Name       *NonEmptyString `json:"name,omitempty"`
	PictureUrl *string         `json:"picture_url,omitempty"`
}

type UserID = string

type ValidationErr struct {
	Errors  []ValidationIssue `json:"errors"`
	Message NonEmptyString    `json:"message"`
}

type ValidationIssue struct {
	Code    ValidationIssueCode `json:"code"`
	Field   *string             `json:"field,omitempty"`
	In      ValidationLocation  `json:"in"`
	Message NonEmptyString      `json:"message"`
	Path    *[]String           `json:"path,omitempty"`
}

type ValidationIssueCode = string

type ValidationLocation string

const (
	ValidationLocationBody   ValidationLocation = "body"
	ValidationLocationQuery  ValidationLocation = "query"
	ValidationLocationPath   ValidationLocation = "path"
	ValidationLocationHeader ValidationLocation = "header"
	ValidationLocationCookie ValidationLocation = "cookie"
)

type VideoDeliveryEntitlement string

const (
	VideoDeliveryEntitlementFree    VideoDeliveryEntitlement = "free"
	VideoDeliveryEntitlementAudio   VideoDeliveryEntitlement = "audio"
	VideoDeliveryEntitlementVideoHd VideoDeliveryEntitlement = "video_hd"
	VideoDeliveryEntitlementVideo4k VideoDeliveryEntitlement = "video_4k"
)

type String = string

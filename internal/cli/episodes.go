package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	publicapi "github.com/listenbox/listenbox-cli/publicapi"
)

const (
	episodeResumeVersion                   = 4
	episodeResumeDirectoryMode os.FileMode = 0o700
	episodeResumeFileMode      os.FileMode = 0o600
	episodeProgressComplete                = int64(100)
	episodeErrorResponseLimit              = 64 << 10
	episodeJSONResponseLimit               = 1 << 20
	episodeAudioMIME                       = "audio/mpeg"
	episodeShowSlugJSONField               = "show_slug"
	episodeMultipartMaxParts               = 10000
	episodeListMaxPageLimit                = 500
	episodeFinalizingPhase                 = "finalizing"
)

type episodeUploadInterruptedError struct{}

func (*episodeUploadInterruptedError) Error() string {
	return "episode source upload interrupted; resumable state saved"
}

type episodeSourceIdentity struct {
	AbsolutePath string
	ByteLength   int64
	ContentType  string
	SHA256       string
}

type episodeResumePart struct {
	PartNumber int32  `json:"part_number"`
	ETag       string `json:"etag"`
	Size       int64  `json:"size"`
}

type episodeResumeRecord struct {
	Version         int                 `json:"version"`
	EpisodeID       string              `json:"episode_id,omitempty"`
	UploadSessionID string              `json:"upload_session_id,omitempty"`
	TeamID          string              `json:"team_id,omitempty"`
	ShowID          string              `json:"show_id,omitempty"`
	ManagementURL   string              `json:"management_url,omitempty"`
	FilePath        string              `json:"file_path"`
	FileSHA256      string              `json:"file_sha256"`
	FileByteLength  int64               `json:"file_byte_length"`
	CompletedParts  []episodeResumePart `json:"completed_parts"`
	ExpiresAt       int64               `json:"expires_at"`
	Phase           string              `json:"phase"`
}

type episodeUploadSession struct {
	TeamID          string              `json:"team_id"`
	ShowID          string              `json:"show_id"`
	EpisodeID       string              `json:"episode_id,omitempty"`
	UploadSessionID string              `json:"upload_session_id"`
	ObjectKey       string              `json:"object_key"`
	ContentType     string              `json:"content_type"`
	ByteLength      int64               `json:"byte_length"`
	PartSize        int64               `json:"part_size"`
	MaxParallelism  int32               `json:"max_parallelism"`
	ExpiresAt       int64               `json:"expires_at"`
	Phase           string              `json:"phase"`
	CompletedParts  []episodeResumePart `json:"completed_parts"`
}

type completedEpisode struct {
	ID     string `json:"id"`
	ShowID string `json:"show_id"`
}

type episodeUploadPartPresign struct {
	PartNumber int32  `json:"part_number"`
	Method     string `json:"method"`
	UploadURL  string `json:"upload_url"`
	ExpiresAt  int64  `json:"expires_at"`
}

type episodeUploadPartPresigns struct {
	UploadSessionID string                     `json:"upload_session_id"`
	Parts           []episodeUploadPartPresign `json:"parts"`
}

type episodeCreateEvent struct {
	Type           string `json:"type"`
	Status         string `json:"status"`
	CurrentStep    string `json:"current_step"`
	Percent        int64  `json:"percent"`
	ErrorCode      string `json:"error_code"`
	ErrorMessage   string `json:"error_message"`
	EpisodeID      string `json:"episode_id"`
	RunID          string `json:"run_id"`
	YouTubeVideoID string `json:"youtube_video_id"`
}

type episodeUploadClient struct {
	apiOrigin   string
	credential  string
	httpClient  *http.Client
	traceWriter io.Writer
}

func runEpisodesCommand(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	if len(args) == 0 {
		writeEpisodesUsage(stderr)
		return errors.New("episodes command is required")
	}
	switch args[0] {
	case helpCommandName, shortHelpFlag, longHelpFlag:
		if len(args) == 1 {
			writeEpisodesUsage(stdout)
			return nil
		}
		if len(args) == nestedCommandArgs {
			switch args[1] {
			case createCommandName:
				writeEpisodesCreateUsage(stdout)
				return nil
			case listCommandName:
				writeEpisodesListUsage(stdout)
				return nil
			case deleteCommandName:
				writeEpisodesDeleteUsage(stdout)
				return nil
			}
		}
		return fmt.Errorf("episodes help received unknown command %q", args[1])
	case createCommandName:
		return runEpisodesCreateCommand(
			ctx, args[1:], stdout, stderr, home, configPath, configExplicit, defaultConfig,
		)
	case listCommandName:
		return runEpisodesListCommand(
			ctx, args[1:], stdout, stderr, home, configPath, configExplicit, defaultConfig,
		)
	case deleteCommandName:
		return runEpisodesDeleteCommand(
			ctx, args[1:], stdout, stderr, home, configPath, configExplicit, defaultConfig,
		)
	default:
		writeEpisodesUsage(stderr)
		return fmt.Errorf("unknown episodes command %q", args[0])
	}
}

func runEpisodesListCommand(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	flags := flag.NewFlagSet("listenbox episodes list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	show := ""
	limit := 0
	flags.StringVar(&configPath, configFlagName, configPath, "path to CLI config file")
	flags.StringVar(&show, showFlagName, "", "existing show slug (required)")
	flags.IntVar(&limit, limitFlagName, 0, "episodes requested per page (1-500)")
	flags.Usage = func() { writeEpisodesListUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse episodes list flags: %w", err)
	}
	configExplicit = configExplicit || flagWasSet(flags, configFlagName)
	if flags.NArg() != 0 {
		return fmt.Errorf("episodes list received unexpected argument %q", flags.Arg(0))
	}
	show = strings.TrimSpace(show)
	if show == "" {
		return errors.New("episodes list: --show is required")
	}
	if !showSlugPattern.MatchString(show) {
		return errors.New("episodes list: --show must be a valid show slug")
	}
	if flagWasSet(flags, limitFlagName) && (limit < 1 || limit > episodeListMaxPageLimit) {
		return errors.New("episodes list: --limit must be from 1 through 500")
	}
	return listEpisodes(ctx, stdout, home, configPath, configExplicit, defaultConfig, show, limit)
}

func listEpisodes(
	ctx context.Context,
	stdout io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
	show string,
	limit int,
) error {
	config, err := loadCLIConfig(configPath, configExplicit, home, defaultConfig)
	if err != nil {
		return err
	}
	auth, ok := readUsableStoredAuth(defaultAuthPath(home))
	if !ok {
		return errors.New("list episodes: not logged in; run listenbox login")
	}
	client, err := newPublicClient(config.apiOrigin, auth.APIKey, true)
	if err != nil {
		return err
	}

	params := publicapi.ListEpisodesParams{ShowSlug: show}
	if limit != 0 {
		//nolint:gosec // Flag validation bounds the value to 1 through 500 before conversion.
		pageLimit := publicapi.EpisodePageLimit(limit)
		params.Limit = &pageLimit
	}
	seenCursors := map[string]struct{}{}
	for {
		response, listErr := client.ListEpisodes(ctx, params)
		if listErr != nil {
			return fmt.Errorf("list episodes for show %q: %w", show, listErr)
		}
		next, err := streamEpisodeListPage(stdout, response, show, config.apiOrigin)
		if err != nil {
			return err
		}
		if next == nil {
			return nil
		}
		if *next == "" {
			return fmt.Errorf("list episodes for show %q returned an empty next cursor", show)
		}
		if _, repeated := seenCursors[*next]; repeated {
			return fmt.Errorf("list episodes for show %q returned repeated cursor %q", show, *next)
		}
		seenCursors[*next] = struct{}{}
		params.Cursor = next
	}
}

func streamEpisodeListPage(
	stdout io.Writer,
	response *publicapi.ListEpisodesResponse,
	show string,
	apiOrigin string,
) (*string, error) {
	if response == nil {
		return nil, fmt.Errorf("list episodes for show %q against %q returned no response", show, apiOrigin)
	}
	if response.Status200 == nil {
		return nil, fmt.Errorf("list episodes for show %q returned HTTP status %d", show, response.StatusCode)
	}
	for _, episode := range response.Status200.Episodes {
		if _, err := fmt.Fprintln(stdout, episode.Id); err != nil {
			return nil, fmt.Errorf("print episode ID %q: %w", episode.Id, err)
		}
	}
	return response.Status200.NextCursor, nil
}

func runEpisodesCreateCommand(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	flags := flag.NewFlagSet("listenbox episodes create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	arguments := episodesCreateArguments{Publication: publicapi.EpisodePublicationDraft}
	description := ""
	publication := string(arguments.Publication)
	flags.StringVar(&configPath, configFlagName, configPath, "path to CLI config file")
	flags.StringVar(&arguments.Show, showFlagName, "", "existing show slug (required)")
	flags.StringVar(&arguments.Title, titleFlagName, "", "episode title (required)")
	flags.StringVar(&description, descriptionFlagName, "", "optional episode description")
	flags.StringVar(&arguments.File, fileFlagName, "", "audio or video source path (required)")
	flags.StringVar(&publication, publicationFlagName, publication, "draft or publish after processing")
	flags.Usage = func() { writeEpisodesCreateUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse episodes create flags: %w", err)
	}
	if flagWasSet(flags, descriptionFlagName) {
		arguments.Description = &description
	}
	arguments.Publication = publicapi.EpisodePublication(publication)
	configExplicit = configExplicit || flagWasSet(flags, configFlagName)
	if flags.NArg() != 0 {
		return fmt.Errorf("episodes create received unexpected argument %q", flags.Arg(0))
	}
	if err := validateEpisodesCreateArguments(&arguments); err != nil {
		return err
	}
	return createEpisode(ctx, stdout, stderr, home, configPath, configExplicit, defaultConfig, arguments)
}

//nolint:cyclop,funlen // Coordinates explicit durable boundaries and output failures.
func createEpisode(
	ctx context.Context,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
	args episodesCreateArguments,
) error {
	source, err := inspectEpisodeSource(args.File)
	if err != nil {
		return err
	}
	config, err := loadCLIConfig(configPath, configExplicit, home, defaultConfig)
	if err != nil {
		return err
	}
	auth, ok := readUsableStoredAuth(defaultAuthPath(home))
	if !ok {
		return errors.New("create episode: not logged in; run listenbox login")
	}
	resumePath, record, err := loadOrCreateEpisodeResume(home, args, source)
	if err != nil {
		return err
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return errors.New("create episode: default HTTP transport is unavailable")
	}
	client := &episodeUploadClient{
		apiOrigin:  config.apiOrigin,
		credential: auth.APIKey,
		httpClient: &http.Client{Transport: transport.Clone()},
	}
	if config.printTraceIDs {
		client.traceWriter = stderr
	}
	wasFinalizing := record.Phase == episodeFinalizingPhase
	var session episodeUploadSession
	if !wasFinalizing {
		session, err = client.createOrResume(ctx, args, source)
		if err != nil {
			return err
		}
		record.EpisodeID = session.EpisodeID
		record.UploadSessionID = session.UploadSessionID
		record.TeamID = session.TeamID
		record.ShowID = session.ShowID
		record.CompletedParts = session.CompletedParts
		record.ExpiresAt = session.ExpiresAt
		record.Phase = session.Phase
	}
	if err := writeEpisodeResume(resumePath, record); err != nil {
		return err
	}
	if record.Phase != episodeFinalizingPhase {
		if err := uploadMissingEpisodeParts(ctx, stderr, source, session, record, resumePath, client); err != nil {
			return err
		}
		record.Phase = episodeFinalizingPhase
		if err := writeEpisodeResume(resumePath, record); err != nil {
			return err
		}
	}
	episode, err := client.complete(ctx, record.UploadSessionID, args.Publication)
	if err != nil {
		return err
	}
	record.EpisodeID = episode.ID
	record.ShowID = episode.ShowID
	record.Phase = processingStatus
	managementURL, err := episodeManagementURL(
		config.dashboardOrigin, record.TeamID, record.ShowID, record.EpisodeID,
	)
	if err != nil {
		return err
	}
	record.ManagementURL = managementURL
	if err := writeEpisodeResume(resumePath, record); err != nil {
		return err
	}
	event, err := client.waitForSafeHandoff(ctx, stderr, record.UploadSessionID)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil
		}
		return err
	}
	record.Phase = importCompletedStatus
	if err := writeEpisodeResume(resumePath, record); err != nil {
		return err
	}
	result := "Created draft episode"
	if args.Publication == publicapi.EpisodePublicationPublish {
		result = "Published episode"
	}
	if _, err := fmt.Fprintf(
		stdout, "%s %q\nOpen in Listenbox: %s\n", result, args.Title, record.ManagementURL,
	); err != nil {
		return fmt.Errorf("print created episode: %w", err)
	}
	if event.YouTubeVideoID != "" {
		if _, err := fmt.Fprintf(
			stdout,
			"YouTube (processing): https://www.youtube.com/watch?v=%s\n",
			event.YouTubeVideoID,
		); err != nil {
			return fmt.Errorf("print YouTube processing URL: %w", err)
		}
	}
	return nil
}

func inspectEpisodeSource(path string) (episodeSourceIdentity, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return episodeSourceIdentity{}, fmt.Errorf("resolve --file %q: %w", path, err)
	}
	file, err := os.Open(absolutePath)
	if err != nil {
		return episodeSourceIdentity{}, fmt.Errorf("open --file %q: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return episodeSourceIdentity{}, fmt.Errorf("inspect --file %q: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return episodeSourceIdentity{}, fmt.Errorf("--file %q must be a non-empty regular file", path)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return episodeSourceIdentity{}, fmt.Errorf("hash --file %q: %w", path, err)
	}
	contentType, err := episodeContentTypeForFileName(absolutePath)
	if err != nil {
		return episodeSourceIdentity{}, fmt.Errorf("inspect --file %q: %w", path, err)
	}
	return episodeSourceIdentity{
		AbsolutePath: absolutePath, ByteLength: info.Size(), ContentType: contentType,
		SHA256: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func episodeContentTypeForFileName(fileName string) (string, error) {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(fileName))) {
	case ".mp3":
		return string(publicapi.EpisodeUploadContentTypeAudioMpeg), nil
	case ".m4a":
		return string(publicapi.EpisodeUploadContentTypeAudioMp4), nil
	case ".wav":
		return string(publicapi.EpisodeUploadContentTypeAudioWav), nil
	case ".flac":
		return string(publicapi.EpisodeUploadContentTypeAudioFlac), nil
	case ".mp4", ".m4v":
		return string(publicapi.EpisodeUploadContentTypeVideoMp4), nil
	case ".mov":
		return string(publicapi.EpisodeUploadContentTypeVideoQuicktime), nil
	default:
		return "", fmt.Errorf(
			"unsupported media upload file %q; accepted extensions: .mp3, .m4a, .wav, .flac, .mp4, .m4v, .mov",
			fileName,
		)
	}
}

//nolint:cyclop // Validates every persisted resume invariant before mutation.
func loadOrCreateEpisodeResume(
	home string,
	args episodesCreateArguments,
	source episodeSourceIdentity,
) (string, episodeResumeRecord, error) {
	resumeIdentity := sha256.Sum256([]byte(args.Show + "\x00" + source.SHA256))
	directory := filepath.Join(home, ".config", "listenbox", "episode-resume")
	path := filepath.Join(directory, hex.EncodeToString(resumeIdentity[:])+".json")
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", episodeResumeRecord{}, fmt.Errorf("read episode resume record %q: %w", path, err)
	}
	if os.IsNotExist(err) {
		record := episodeResumeRecord{
			Version:  episodeResumeVersion,
			FilePath: source.AbsolutePath, FileSHA256: source.SHA256, FileByteLength: source.ByteLength,
			CompletedParts: []episodeResumePart{}, Phase: "prepared",
		}
		if err := writeEpisodeResume(path, record); err != nil {
			return "", record, err
		}
		return path, record, nil
	}
	var record episodeResumeRecord
	if decodeErr := json.Unmarshal(raw, &record); decodeErr != nil {
		return "", record, fmt.Errorf("decode episode resume record %q: %w", path, decodeErr)
	}
	if record.Version != episodeResumeVersion {
		return "", record, fmt.Errorf(
			"episode resume record %q has an unsupported version; remove it to restart explicitly",
			path,
		)
	}
	if record.FileSHA256 != source.SHA256 || record.FileByteLength != source.ByteLength {
		return "", record, fmt.Errorf(
			"--file identity changed since resumable upload %s; remove %q to restart explicitly",
			record.UploadSessionID,
			path,
		)
	}
	if record.ExpiresAt > 0 && record.ExpiresAt <= time.Now().UnixMilli() &&
		record.Phase != "finalizing" && record.Phase != processingStatus && record.Phase != "completed" {
		return "", record, fmt.Errorf(
			"resumable upload %s expired; remove %q to restart explicitly",
			record.UploadSessionID,
			path,
		)
	}
	record.FilePath = source.AbsolutePath
	return path, record, nil
}

func writeEpisodeResume(path string, record episodeResumeRecord) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, episodeResumeDirectoryMode); err != nil {
		return fmt.Errorf("create episode resume directory %q: %w", directory, err)
	}
	if err := os.Chmod(directory, episodeResumeDirectoryMode); err != nil {
		return fmt.Errorf("secure episode resume directory %q: %w", directory, err)
	}
	temporary, err := os.CreateTemp(directory, ".episode-resume-*")
	if err != nil {
		return fmt.Errorf("create temporary episode resume record: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(episodeResumeFileMode); err != nil {
		_ = temporary.Close()
		return err
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(record); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode episode resume record: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync episode resume record: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close episode resume record: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace episode resume record %q: %w", path, err)
	}
	return syncAuthDirectory(directory)
}

//nolint:cyclop // Each accepted part has its own durable save boundary.
func uploadMissingEpisodeParts(
	ctx context.Context,
	stderr io.Writer,
	source episodeSourceIdentity,
	session episodeUploadSession,
	record episodeResumeRecord,
	resumePath string,
	client *episodeUploadClient,
) error {
	accepted := make(map[int32]episodeResumePart, len(session.CompletedParts))
	for _, part := range session.CompletedParts {
		accepted[part.PartNumber] = part
	}
	partCount64 := (source.ByteLength + session.PartSize - 1) / session.PartSize
	if partCount64 > episodeMultipartMaxParts {
		return errors.New("episode source requires too many multipart parts")
	}
	partCount := int32(partCount64) // #nosec G115 -- bounded by the multipart maximum above.
	missing := make([]int32, 0, partCount)
	for partNumber := int32(1); partNumber <= partCount; partNumber++ {
		if _, ok := accepted[partNumber]; !ok {
			missing = append(missing, partNumber)
		}
	}
	if err := printSavedEpisodeProgress(stderr, accepted, source.ByteLength); err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}
	presigns, err := client.presign(ctx, session.UploadSessionID, missing)
	if err != nil {
		return err
	}
	file, err := os.Open(source.AbsolutePath)
	if err != nil {
		return fmt.Errorf("reopen --file %q: %w", source.AbsolutePath, err)
	}
	defer file.Close()
	for _, presign := range presigns.Parts {
		start := int64(presign.PartNumber-1) * session.PartSize
		length := min(session.PartSize, source.ByteLength-start)
		etag, err := client.uploadPart(ctx, presign, io.NewSectionReader(file, start, length), length)
		if err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return &episodeUploadInterruptedError{}
			}
			return err
		}
		accepted[presign.PartNumber] = episodeResumePart{
			PartNumber: presign.PartNumber, ETag: etag, Size: length,
		}
		record.CompletedParts = sortedEpisodeResumeParts(accepted)
		record.Phase = "uploading"
		if err := writeEpisodeResume(resumePath, record); err != nil {
			return err
		}
		if err := printSavedEpisodeProgress(stderr, accepted, source.ByteLength); err != nil {
			return err
		}
	}
	return nil
}

func sortedEpisodeResumeParts(parts map[int32]episodeResumePart) []episodeResumePart {
	result := make([]episodeResumePart, 0, len(parts))
	for _, part := range parts {
		result = append(result, part)
	}
	slices.SortFunc(result, func(a, b episodeResumePart) int { return int(a.PartNumber - b.PartNumber) })
	return result
}

func printSavedEpisodeProgress(
	writer io.Writer,
	parts map[int32]episodeResumePart,
	total int64,
) error {
	var saved int64
	for _, part := range parts {
		saved += part.Size
	}
	percent := min(episodeProgressComplete, saved*episodeProgressComplete/total)
	if _, err := fmt.Fprintf(writer, "Source upload: %d%% saved\n", percent); err != nil {
		return fmt.Errorf("print source upload progress: %w", err)
	}
	return nil
}

func (client *episodeUploadClient) createOrResume(
	ctx context.Context,
	args episodesCreateArguments,
	source episodeSourceIdentity,
) (episodeUploadSession, error) {
	body := map[string]any{
		episodeShowSlugJSONField: args.Show, "title": args.Title,
		"file_name": filepath.Base(source.AbsolutePath), "content_type": source.ContentType,
		"byte_length": source.ByteLength, "source_sha256": source.SHA256,
	}
	if args.Description != nil {
		body[descriptionFlagName] = *args.Description
	}
	var session episodeUploadSession
	err := client.doJSON(
		ctx, http.MethodPost, "/s/episode-upload-sessions", body, &session, http.StatusOK, http.StatusCreated,
	)
	return session, err
}

func (client *episodeUploadClient) presign(
	ctx context.Context,
	uploadSessionID string,
	partNumbers []int32,
) (episodeUploadPartPresigns, error) {
	var result episodeUploadPartPresigns
	err := client.doJSON(
		ctx, http.MethodPost,
		"/s/episode-upload-sessions/"+uploadSessionID+"/parts/presign",
		map[string]any{"part_numbers": partNumbers}, &result, http.StatusCreated,
	)
	return result, err
}

func (client *episodeUploadClient) complete(
	ctx context.Context,
	uploadSessionID string,
	publication publicapi.EpisodePublication,
) (completedEpisode, error) {
	var result completedEpisode
	err := client.doJSON(
		ctx, http.MethodPost, "/s/episode-upload-sessions/"+uploadSessionID+"/complete",
		map[string]any{publicationFlagName: publication}, &result, http.StatusOK, http.StatusCreated,
	)
	return result, err
}

func (client *episodeUploadClient) uploadPart(
	ctx context.Context,
	presign episodeUploadPartPresign,
	body io.Reader,
	length int64,
) (string, error) {
	request, err := http.NewRequestWithContext(ctx, presign.Method, presign.UploadURL, body)
	if err != nil {
		return "", fmt.Errorf("create upload request for part %d: %w", presign.PartNumber, err)
	}
	request.ContentLength = length
	response, err := client.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("upload source part %d: %w", presign.PartNumber, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, episodeErrorResponseLimit))
		return "", fmt.Errorf(
			"upload source part %d returned HTTP %d: %s",
			presign.PartNumber,
			response.StatusCode,
			strings.TrimSpace(string(message)),
		)
	}
	etag := response.Header.Get("ETag")
	if etag == "" {
		return "", fmt.Errorf("upload source part %d returned no ETag", presign.PartNumber)
	}
	return etag, nil
}

func (client *episodeUploadClient) doJSON(
	ctx context.Context,
	method string,
	path string,
	body any,
	target any,
	wantStatus ...int,
) error {
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode episode upload request: %w", err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.apiOrigin+path, requestBody)
	if err != nil {
		return fmt.Errorf("create episode upload request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.credential)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("send episode upload request: %w", err)
	}
	defer response.Body.Close()
	if err := client.printResponseTrace(response); err != nil {
		return err
	}
	if !slices.Contains(wantStatus, response.StatusCode) {
		message, _ := io.ReadAll(io.LimitReader(response.Body, episodeErrorResponseLimit))
		return fmt.Errorf("episode upload returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, episodeJSONResponseLimit))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode episode upload response: %w", err)
	}
	return nil
}

//nolint:cyclop,funlen // SSE parsing keeps each malformed and terminal condition explicit.
func (client *episodeUploadClient) waitForSafeHandoff(
	ctx context.Context,
	stderr io.Writer,
	uploadSessionID string,
) (episodeCreateEvent, error) {
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, client.apiOrigin+"/s/episode-upload-sessions/"+uploadSessionID+"/events", nil,
	)
	if err != nil {
		return episodeCreateEvent{}, err
	}
	request.Header.Set("Authorization", "Bearer "+client.credential)
	request.Header.Set("Accept", "text/event-stream")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return episodeCreateEvent{}, fmt.Errorf("attach to episode processing: %w", err)
	}
	defer response.Body.Close()
	if err := client.printResponseTrace(response); err != nil {
		return episodeCreateEvent{}, err
	}
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, episodeErrorResponseLimit))
		return episodeCreateEvent{}, fmt.Errorf(
			"episode processing stream returned HTTP %d: %s",
			response.StatusCode,
			strings.TrimSpace(string(message)),
		)
	}
	scanner := bufio.NewScanner(response.Body)
	var data string
	for scanner.Scan() {
		line := scanner.Text()
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			data = strings.TrimSpace(value)
			continue
		}
		if line != "" || data == "" {
			continue
		}
		var event episodeCreateEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return event, fmt.Errorf("decode episode processing event: %w", err)
		}
		data = ""
		switch event.Type {
		case "episode.processing.progress":
			if _, err := fmt.Fprintf(stderr, "%s: %d%%\n", episodeProgressLabel(event.CurrentStep), event.Percent); err != nil {
				return event, err
			}
		case "episode.processing.safe_handoff":
			return event, nil
		case "episode.processing.failed":
			return event, fmt.Errorf("episode processing failed (%s): %s", event.ErrorCode, event.ErrorMessage)
		default:
			return event, fmt.Errorf("episode processing emitted unknown event type %q", event.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		return episodeCreateEvent{}, fmt.Errorf("read episode processing stream: %w", err)
	}
	return episodeCreateEvent{}, errors.New("episode processing stream ended before safe handoff")
}

func episodeProgressLabel(step string) string {
	switch step {
	case "queued", "processing_audio", "listenbox_processed":
		return "Listenbox processing"
	case "video_preparation", "video_prepared":
		return "Video preparation"
	case "youtube_upload":
		return "YouTube upload"
	default:
		return "Episode processing"
	}
}

func episodeManagementURL(origin string, teamID string, showID string, episodeID string) (string, error) {
	if !validCompletionID(teamID, "team_") || !validCompletionID(showID, "shw_") ||
		!validCompletionID(episodeID, "ep_") {
		return "", errors.New("episode create returned invalid management identifiers")
	}
	result, err := url.JoinPath(origin, teamID, "shows", showID, "episodes", episodeID, "edit")
	if err != nil {
		return "", fmt.Errorf("build episode management URL: %w", err)
	}
	return result, nil
}

func (client *episodeUploadClient) printResponseTrace(response *http.Response) error {
	if client.traceWriter == nil {
		return nil
	}
	traceID := response.Header.Get("X-Trace-Id")
	decoded, err := hex.DecodeString(traceID)
	if err != nil || len(decoded) != 16 || traceID != strings.ToLower(traceID) || strings.Trim(traceID, "0") == "" {
		return fmt.Errorf("print episode request trace ID: invalid X-Trace-Id %q", traceID)
	}
	if _, err := fmt.Fprintf(client.traceWriter, "Trace ID: %s\n", traceID); err != nil {
		return fmt.Errorf("print episode request trace ID %q: %w", traceID, err)
	}
	return nil
}

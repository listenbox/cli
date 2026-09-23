//nolint:lll // Media manifests and generated API mappings are kept together for review.
package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	youtube "github.com/kkdai/youtube/v2"
	publicapi "github.com/listenbox/listenbox-cli/publicapi"
)

const (
	youtubeTransferTimeout = 30 * time.Minute
	youtubeCleanupTimeout  = 10 * time.Second
	youtubeFileMode        = 0o600
	youtubeDirectoryMode   = 0o700
	youtubeProcessWait     = 2 * time.Second
	youtubeResponseLimit   = 64 << 10
	youtubeVideoKind       = "video"
	youtubeAudioKind       = "audio"
)

func isYouTubeSource(source string) bool {
	parsed, err := url.Parse(source)
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "youtube.com", "www.youtube.com", "m.youtube.com", "youtu.be":
		return true
	default:
		return false
	}
}

//nolint:cyclop // Local download, media processing and upload failures retain explicit recovery boundaries.
func importYouTube(ctx context.Context, stdout, stderr io.Writer, home, configPath string, configExplicit bool, defaultConfig loadedCLIConfig, sourceURL, slug string, slugExplicit bool) error {
	config, err := loadCLIConfig(configPath, configExplicit, home, defaultConfig)
	if err != nil {
		return err
	}
	auth, ok := readUsableStoredAuth(defaultAuthPath(home))
	if !ok {
		return errors.New("import YouTube: not logged in; run listenbox login")
	}
	api, err := newPublicClient(config.apiOrigin, auth.APIKey, true)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: youtubeTransferTimeout}
	extractor := &youtube.Client{HTTPClient: client}
	title, videos, err := discoverYouTube(ctx, extractor, sourceURL)
	if err != nil {
		return err
	}
	if !slugExplicit {
		digest := sha256.Sum256([]byte(sourceURL))
		slug = "youtube-" + hex.EncodeToString(digest[:8])
	}
	show, err := ensureYouTubeShow(ctx, api, title, slug)
	if err != nil {
		return err
	}
	progress := newImportProgress(stderr, writerIsTerminal(stderr))
	defer progress.cleanFailureLine()
	for index, id := range videos {
		if err := importYouTubeVideo(ctx, extractor, client, api, slug, id, stdout, config.printTraceIDs); err != nil {
			return fmt.Errorf("import YouTube video %q: %w", id, err)
		}
		//nolint:gosec // The quotient is bounded to 0..100 by the playlist loop.
		if err := progress.update(uint32((index + 1) * 100 / len(videos))); err != nil {
			return err
		}
	}
	if err := progress.complete(); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, slug); err != nil {
		return err
	}
	managementURL, err := showManagementURL(config.dashboardOrigin, show.TeamId, show.Id)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stderr, "Open in Listenbox: %s\n", managementURL)
	return err
}

func discoverYouTube(ctx context.Context, client *youtube.Client, sourceURL string) (string, []string, error) {
	parsed, err := url.Parse(sourceURL)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" {
		return "", nil, fmt.Errorf("invalid YouTube source URL %q", sourceURL)
	}
	if parsed.Query().Get("list") != "" {
		playlist, err := client.GetPlaylistContext(ctx, sourceURL)
		if err != nil {
			return "", nil, fmt.Errorf("read YouTube playlist: %w", err)
		}
		ids := make([]string, 0, len(playlist.Videos))
		seen := make(map[string]bool, len(playlist.Videos))
		for _, entry := range playlist.Videos {
			if entry.ID != "" && !seen[entry.ID] {
				ids = append(ids, entry.ID)
				seen[entry.ID] = true
			}
		}
		if len(ids) == 0 {
			return "", nil, errors.New("YouTube playlist has no public videos")
		}
		return playlist.Title, ids, nil
	}
	video, err := client.GetVideoContext(ctx, sourceURL)
	if err != nil {
		return "", nil, fmt.Errorf("read YouTube video: %w", err)
	}
	return video.Title, []string{video.ID}, nil
}

func ensureYouTubeShow(ctx context.Context, client *publicapi.Client, title, slug string) (publicapi.Show, error) {
	listed, err := client.ListShows(ctx)
	if err != nil {
		return publicapi.Show{}, err
	}
	if listed == nil || listed.Status200 == nil {
		return publicapi.Show{}, errors.New("cannot list writable shows")
	}
	for _, show := range *listed.Status200 {
		if show.Slug == slug {
			if show.SourceKind != youtubeVideoKind {
				return publicapi.Show{}, fmt.Errorf("show %q is not a video show", slug)
			}
			return show, nil
		}
	}
	id, err := mintShowID()
	if err != nil {
		return publicapi.Show{}, err
	}
	created, err := client.CreateShow(ctx, publicapi.CreateShowParams{Body: publicapi.CreateShow{Id: id, Title: title, Slug: slug, SourceKind: youtubeVideoKind, Language: "en"}})
	if err != nil {
		return publicapi.Show{}, err
	}
	if created == nil {
		return publicapi.Show{}, errors.New("create YouTube show returned no response")
	}
	if created.Status201 == nil {
		return publicapi.Show{}, handleCreateShowResponse(io.Discard, "", slug, created)
	}
	return *created.Status201, nil
}

//nolint:cyclop,funlen // Local download, media processing and upload failures retain explicit recovery boundaries.
func importYouTubeVideo(ctx context.Context, extractor *youtube.Client, client *http.Client, api *publicapi.Client, slug, id string, stdout io.Writer, printTraceIDs bool) (retErr error) {
	video, err := extractor.GetVideoContext(ctx, id)
	if err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "listenbox-youtube-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	if err := downloadYouTubeVideo(ctx, extractor, video, directory); err != nil {
		return err
	}
	duration, err := packageYouTubeVideo(ctx, directory)
	if err != nil {
		return err
	}
	objects, err := describeYouTubePackage(directory)
	if err != nil {
		return err
	}
	response, err := api.CreateEpisodePackage(ctx, publicapi.CreateEpisodePackageParams{Body: publicapi.CreateEpisodePackage{
		ShowSlug: slug, SourceUrl: "https://www.youtube.com/watch?v=" + id, Title: video.Title, Description: &video.Description,
		DurationSeconds: duration, Objects: objects,
	}})
	if err != nil {
		return err
	}
	if response != nil && response.Status402 != nil {
		admission := response.Status402
		return fmt.Errorf("video storage quota: %d seconds requested, %d remaining; %s", admission.RequestedSeconds, admission.RemainingSeconds, admission.PricingUrl)
	}
	if response == nil || response.Status201 == nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		return fmt.Errorf("reserve prepared media: HTTP %d", status)
	}
	session := *response.Status201
	if session.Status == importCompletedStatus {
		return nil
	}
	defer func() {
		if retErr == nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), youtubeCleanupTimeout)
		defer cancel()
		response, err := api.CancelEpisodePackage(cleanupCtx, publicapi.CancelEpisodePackageParams{UploadSessionId: session.UploadSessionId})
		// A completed package survives an acknowledgement loss.
		if err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("cancel pending package: %w", err))
		} else if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusConflict {
			retErr = errors.Join(retErr, fmt.Errorf("cancel pending package: HTTP %d", response.StatusCode))
		}
	}()

	if printTraceIDs {
		if err := printImportTraceID(stdout, response.Raw.Header.Get("X-Trace-Id")); err != nil {
			return err
		}
	}
	for index, object := range objects {
		if err := uploadYouTubeObject(ctx, api, client, session, int64(index), object, directory); err != nil {
			return err
		}
	}
	completed, err := api.CompleteEpisodePackage(ctx, publicapi.CompleteEpisodePackageParams{UploadSessionId: session.UploadSessionId})
	if err != nil {
		return err
	}
	if completed == nil || completed.Status200 == nil || completed.Status200.Status != importCompletedStatus {
		return errors.New("prepared media did not complete")
	}
	return nil
}

//nolint:cyclop // Local download, media processing and upload failures retain explicit recovery boundaries.
func downloadYouTubeVideo(ctx context.Context, client *youtube.Client, video *youtube.Video, directory string) error {
	formats := slices.Clone(video.Formats)
	slices.SortFunc(formats, func(a, b youtube.Format) int { return b.Height - a.Height })
	var selected *youtube.Format
	for index := range formats {
		if strings.Contains(formats[index].MimeType, "avc1") && formats[index].Height <= 1080 {
			selected = &formats[index]
			break
		}
	}
	if selected == nil {
		return errors.New("YouTube video has no AVC rendition at or below 1080p")
	}
	videoPath := filepath.Join(directory, "source-video.mp4")
	if err := downloadYouTubeStream(ctx, client, video, selected, videoPath); err != nil {
		return err
	}
	args := []string{"-i", videoPath}
	if selected.AudioChannels == 0 {
		var audio *youtube.Format
		for index := range formats {
			candidate := &formats[index]
			if strings.HasPrefix(candidate.MimeType, "audio/") && (audio == nil || candidate.Bitrate > audio.Bitrate) {
				audio = candidate
			}
		}
		if audio == nil {
			return errors.New("YouTube video has no audio stream")
		}
		audioPath := filepath.Join(directory, "source-audio")
		if err := downloadYouTubeStream(ctx, client, video, audio, audioPath); err != nil {
			return err
		}
		args = append(args, "-i", audioPath, "-map", "0:v:0", "-map", "1:a:0")
	} else {
		args = append(args, "-map", "0:v:0", "-map", "0:a:0")
	}
	args = append(args, "-map_metadata", "-1", "-c:v", "copy", "-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", filepath.Join(directory, "video.mp4"))
	_, err := runBundledMedia(ctx, "ffmpeg", args...)
	return err
}

func downloadYouTubeStream(ctx context.Context, client *youtube.Client, video *youtube.Video, format *youtube.Format, destination string) error {
	streamURL, err := client.GetStreamURLContext(ctx, video, format)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Origin", "https://youtube.com")
	response, err := client.HTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download YouTube media: HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, youtubeFileMode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, response.Body)
	return errors.Join(copyErr, file.Close())
}

func runBundledMedia(ctx context.Context, name string, args ...string) ([]byte, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	fileName := name
	if runtime.GOOS == "windows" {
		fileName += ".exe"
	}
	program := filepath.Join(filepath.Dir(executable), fileName)
	if name == "ffmpeg" {
		args = append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}, args...)
	}
	command := exec.CommandContext(ctx, program, args...)
	command.WaitDelay = youtubeProcessWait
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("bundled %s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return output, nil
}

func packageYouTubeVideo(ctx context.Context, directory string) (int64, error) {
	source := filepath.Join(directory, "video.mp4")
	var dimensions struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}
	encoded, err := runBundledMedia(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "json", source)
	if err != nil {
		return 0, err
	}
	if err := json.Unmarshal(encoded, &dimensions); err != nil {
		return 0, err
	}
	if len(dimensions.Streams) != 1 {
		return 0, errors.New("processed video has no video stream")
	}
	for _, kind := range []string{youtubeVideoKind, youtubeAudioKind} {
		root := filepath.Join(directory, "hls", kind)
		if err := os.MkdirAll(root, youtubeDirectoryMode); err != nil {
			return 0, err
		}
		mapping := "0:v:0"
		if kind == youtubeAudioKind {
			mapping = "0:a:0"
		}
		_, err := runBundledMedia(ctx, "ffmpeg", "-i", source, "-map", mapping, "-map_metadata", "-1", "-c", "copy", "-f", "hls", "-hls_time", "6", "-hls_playlist_type", "vod", "-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "init.mp4", "-hls_segment_filename", filepath.Join(root, "segment-%05d.m4s"), filepath.Join(root, "index.m3u8"))
		if err != nil {
			return 0, err
		}
	}
	master := fmt.Sprintf("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-INDEPENDENT-SEGMENTS\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"Audio\",DEFAULT=YES,AUTOSELECT=YES,URI=\"audio/index.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=12000000,RESOLUTION=%dx%d,AUDIO=\"audio\"\nvideo/index.m3u8\n", dimensions.Streams[0].Width, dimensions.Streams[0].Height)
	if err := os.WriteFile(filepath.Join(directory, "hls", "master.m3u8"), []byte(master), youtubeFileMode); err != nil {
		return 0, err
	}
	videoDuration, err := localPlaylistDuration(filepath.Join(directory, "hls", youtubeVideoKind, "index.m3u8"))
	if err != nil {
		return 0, err
	}
	audioDuration, err := localPlaylistDuration(filepath.Join(directory, "hls", youtubeAudioKind, "index.m3u8"))
	if err != nil {
		return 0, err
	}
	return int64(math.Ceil(min(videoDuration, audioDuration))), nil
}

func localPlaylistDuration(file string) (float64, error) {
	content, err := os.ReadFile(file)
	if err != nil {
		return 0, err
	}
	var total float64
	for line := range strings.SplitSeq(string(content), "\n") {
		if value, ok := strings.CutPrefix(line, "#EXTINF:"); ok {
			seconds, _, _ := strings.Cut(value, ",")
			duration, err := strconv.ParseFloat(seconds, 64)
			if err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 {
				return 0, fmt.Errorf("invalid HLS segment duration %q", seconds)
			}
			total += duration
		}
	}
	if total <= 0 {
		return 0, fmt.Errorf("playlist %q has no segments", file)
	}
	return total, nil
}

func describeYouTubePackage(directory string) ([]publicapi.PreparedMediaObject, error) {
	objects := []publicapi.PreparedMediaObject{}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	err = filepath.WalkDir(directory, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		name, err := filepath.Rel(directory, file)
		if err != nil {
			return err
		}
		name = filepath.ToSlash(name)
		if name != "video.mp4" && !strings.HasPrefix(name, "hls/") {
			return nil
		}
		contentType := "video/mp4"
		if strings.HasSuffix(name, ".m3u8") {
			contentType = "application/vnd.apple.mpegurl"
		} else if strings.HasPrefix(name, "hls/audio/") {
			contentType = "audio/mp4"
		}
		source, err := root.Open(name)
		if err != nil {
			return err
		}
		hash := sha256.New()
		length, err := io.Copy(hash, source)
		closeErr := source.Close()
		if err := errors.Join(err, closeErr); err != nil {
			return err
		}
		objects = append(objects, publicapi.PreparedMediaObject{Name: name, ContentType: contentType, ByteLength: length, Sha256: hex.EncodeToString(hash.Sum(nil))})
		return nil
	})
	return objects, err
}

//nolint:cyclop // Local download, media processing and upload failures retain explicit recovery boundaries.
func uploadYouTubeObject(ctx context.Context, api *publicapi.Client, client *http.Client, session publicapi.EpisodePackage, ordinal int64, object publicapi.PreparedMediaObject, directory string) error {
	file, err := os.Open(filepath.Join(directory, filepath.FromSlash(object.Name)))
	if err != nil {
		return err
	}
	defer file.Close()
	if session.PartSize < 5<<20 {
		return fmt.Errorf("invalid upload part size %d", session.PartSize)
	}
	for start, number := int64(0), int32(1); start < object.ByteLength; start, number = start+session.PartSize, number+1 {
		response, err := api.PresignEpisodePackageParts(ctx, publicapi.PresignEpisodePackagePartsParams{UploadSessionId: session.UploadSessionId, ObjectIndex: ordinal, Body: publicapi.PresignEpisodeUploadSessionParts{PartNumbers: []int32{number}}})
		if err != nil {
			return err
		}
		if response == nil || response.Status200 == nil {
			return errors.New("cannot sign media object upload")
		}
		if len(response.Status200.Parts) == 0 {
			return nil
		}
		if len(response.Status200.Parts) != 1 {
			return errors.New("invalid signed part response")
		}
		part := response.Status200.Parts[0]
		length := min(session.PartSize, object.ByteLength-start)
		request, err := http.NewRequestWithContext(ctx, http.MethodPut, part.UploadUrl, io.NewSectionReader(file, start, length))
		if err != nil {
			return err
		}
		request.ContentLength = length
		uploaded, err := client.Do(request)
		if err != nil {
			return err
		}
		_, readErr := io.Copy(io.Discard, io.LimitReader(uploaded.Body, youtubeResponseLimit))
		closeErr := uploaded.Body.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return err
		}
		if uploaded.StatusCode != http.StatusOK {
			return fmt.Errorf("upload media object %q: HTTP %d", object.Name, uploaded.StatusCode)
		}
	}
	return nil
}

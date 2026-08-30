package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	publicapi "github.com/listenbox/listenbox-cli/publicapi"
)

const (
	showArtworkUploadTimeout = 2 * time.Minute
	showIDRandomByteLength   = 8
)

func listShows(
	ctx context.Context,
	stdout io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	config, err := loadCLIConfig(configPath, configExplicit, home, defaultConfig)
	if err != nil {
		return err
	}
	auth, ok := readUsableStoredAuth(defaultAuthPath(home))
	if !ok {
		return errors.New("list shows: not logged in; run listenbox login")
	}
	client, err := newPublicClient(config.apiOrigin, auth.APIKey, true)
	if err != nil {
		return err
	}
	response, err := client.ListShows(ctx)
	if err != nil {
		return fmt.Errorf("list shows: %w", err)
	}
	if response == nil {
		return fmt.Errorf("list shows against %q returned no response", config.apiOrigin)
	}
	return handleListShowsResponse(stdout, response)
}

func handleListShowsResponse(stdout io.Writer, response *publicapi.ListShowsResponse) error {
	switch {
	case response.Status200 != nil:
		for _, show := range *response.Status200 {
			if _, err := fmt.Fprintln(stdout, show.Slug); err != nil {
				return fmt.Errorf("print show slug %q: %w", show.Slug, err)
			}
		}
		return nil
	case response.Status400 != nil:
		return fmt.Errorf("list shows: %s", response.Status400.Message)
	case response.StatusCode == http.StatusUnauthorized:
		return errors.New("list shows: authentication failed; run listenbox login")
	default:
		return fmt.Errorf("list shows returned HTTP status %d", response.StatusCode)
	}
}

func createShow(
	ctx context.Context,
	stdout io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
	title string,
	slug string,
	sourceKind publicapi.ShowSourceKind,
	artworkPath string,
	language string,
) error {
	config, err := loadCLIConfig(configPath, configExplicit, home, defaultConfig)
	if err != nil {
		return err
	}
	auth, ok := readUsableStoredAuth(defaultAuthPath(home))
	if !ok {
		return errors.New("create show: not logged in; run listenbox login")
	}
	client, err := newPublicClient(config.apiOrigin, auth.APIKey, true)
	if err != nil {
		return err
	}
	showID, err := mintShowID()
	if err != nil {
		return fmt.Errorf("create show %q: %w", slug, err)
	}
	var imageAssetID *publicapi.ImageAssetID
	if artworkPath != "" {
		uploadClient := &http.Client{Timeout: showArtworkUploadTimeout}
		uploadedID, err := uploadShowArtwork(
			ctx,
			client,
			uploadClient,
			showID,
			artworkPath,
		)
		if err != nil {
			return fmt.Errorf("create show %q artwork: %w", slug, err)
		}
		imageAssetID = &uploadedID
	}
	response, err := client.CreateShow(ctx, publicapi.CreateShowParams{
		Body: publicapi.CreateShow{
			Id:           showID,
			ImageAssetId: imageAssetID,
			Title:        title,
			Slug:         slug,
			SourceKind:   sourceKind,
			Language:     language,
		},
	})
	if err != nil {
		return fmt.Errorf("create show %q: %w", slug, err)
	}
	if response == nil {
		return fmt.Errorf("create show %q against %q returned no response", slug, config.apiOrigin)
	}
	return handleCreateShowResponse(stdout, config.dashboardOrigin, slug, response)
}

func uploadShowArtwork(
	ctx context.Context,
	client *publicapi.Client,
	uploadClient *http.Client,
	showID publicapi.ShowID,
	path string,
) (publicapi.ImageAssetID, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %q: %w", path, err)
	}
	contentType, ok := showArtworkContentType(raw)
	if !ok {
		return "", errors.New("artwork must be a JPEG or PNG image")
	}
	if err := validateShowArtworkDimensions(raw); err != nil {
		return "", err
	}
	fileName := filepath.Base(path)
	presign, err := client.CreateImageUploadPresign(
		ctx,
		publicapi.CreateImageUploadPresignParams{
			Body: publicapi.CreateImageUploadPresign{
				ByteLength:  int64(len(raw)),
				ContentType: contentType,
				FileName:    &fileName,
				ShowId:      showID,
			},
		},
	)
	if err != nil {
		return "", fmt.Errorf("prepare upload: %w", err)
	}
	if presign.Status201 == nil {
		return "", fmt.Errorf("prepare upload returned HTTP %d", presign.StatusCode)
	}
	if err := putShowArtwork(ctx, uploadClient, presign.Status201, raw, contentType); err != nil {
		return "", err
	}

	completed, err := client.CompleteImageUpload(
		ctx,
		publicapi.CompleteImageUploadParams{
			ImageAssetId: presign.Status201.ImageAssetId,
			Body: publicapi.CompleteImageUpload{
				ObjectKey: presign.Status201.ObjectKey,
			},
		},
	)
	if err != nil {
		return "", fmt.Errorf("verify upload: %w", err)
	}
	if completed.Status201 == nil {
		return "", fmt.Errorf("verify upload returned HTTP %d", completed.StatusCode)
	}
	return completed.Status201.Id, nil
}

func mintShowID() (publicapi.ShowID, error) {
	randomBytes := make([]byte, showIDRandomByteLength)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("mint show ID: %w", err)
	}
	return "shw_" + hex.EncodeToString(randomBytes), nil
}

func validateShowArtworkDimensions(raw []byte) error {
	width, height, err := showArtworkDimensions(raw)
	if err != nil {
		return err
	}
	if width != height || width < 1400 || width > 3000 {
		return fmt.Errorf(
			"upload a square image between 1400 and 3000 pixels. Attempted resolution: %d × %d pixels",
			width,
			height,
		)
	}
	return nil
}

func showArtworkDimensions(raw []byte) (int, int, error) {
	if len(raw) >= 3 && raw[0] == 0xff && raw[1] == 0xd8 && raw[2] == 0xff {
		config, err := jpeg.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			return 0, 0, fmt.Errorf("read artwork dimensions: %w", err)
		}
		return config.Width, config.Height, nil
	}
	config, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return 0, 0, fmt.Errorf("read artwork dimensions: %w", err)
	}
	return config.Width, config.Height, nil
}

func putShowArtwork(
	ctx context.Context,
	client *http.Client,
	presign *publicapi.CreatedImageUploadPresign,
	raw []byte,
	contentType string,
) error {
	request, err := http.NewRequestWithContext(
		ctx,
		presign.Method,
		presign.UploadUrl,
		bytes.NewReader(raw),
	)
	if err != nil {
		return fmt.Errorf("create upload request: %w", err)
	}
	request.ContentLength = int64(len(raw))
	request.Header.Set("Content-Type", contentType)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("upload artwork: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("upload artwork returned HTTP %d", response.StatusCode)
	}
	return nil
}

func showArtworkContentType(raw []byte) (string, bool) {
	if len(raw) >= 3 && raw[0] == 0xff && raw[1] == 0xd8 && raw[2] == 0xff {
		return "image/jpeg", true
	}
	pngSignature := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	if len(raw) >= len(pngSignature) && bytes.Equal(raw[:len(pngSignature)], pngSignature) {
		return "image/png", true
	}
	return "", false
}

func handleCreateShowResponse(
	stdout io.Writer,
	dashboardOrigin string,
	slug string,
	response *publicapi.CreateShowResponse,
) error {
	switch {
	case response.Status201 != nil:
		showURL, err := showManagementURL(dashboardOrigin, response.Status201.TeamId, response.Status201.Id)
		if err != nil {
			return fmt.Errorf("create show %q returned invalid show location: %w", slug, err)
		}
		if _, err := fmt.Fprintf(
			stdout,
			"Created show %q\nOpen in Listenbox: %s\n",
			response.Status201.Slug,
			showURL,
		); err != nil {
			return fmt.Errorf("print created show slug %q: %w", response.Status201.Slug, err)
		}
		return nil
	case response.Status400 != nil:
		return fmt.Errorf("create show %q: %s", slug, response.Status400.Message)
	case response.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("create show %q: authentication failed; run listenbox login", slug)
	case response.StatusCode == http.StatusForbidden:
		return fmt.Errorf("create show %q: API key lacks show:create scope", slug)
	case response.StatusCode == http.StatusConflict:
		return fmt.Errorf("create show: slug %q already exists", slug)
	default:
		return fmt.Errorf("create show %q returned HTTP status %d", slug, response.StatusCode)
	}
}

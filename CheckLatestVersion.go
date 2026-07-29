package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/Masterminds/semver"
	"github.com/reugn/async"
)

const (
	frankenPHPRepository = "https://api.github.com/repos/php/frankenphp/releases/latest"
	frankenPHPAssetName  = "frankenphp-windows-x86_64.zip"
	phpMyAdminTagsURL    = "https://api.github.com/repos/phpmyadmin/phpmyadmin/tags?per_page=100"
	phpMyAdminArchiveURL = "https://github.com/phpmyadmin/phpmyadmin/archive/refs/tags/%s.tar.gz"
	githubAPIUserAgent   = "phpMyAdmin-Desktop"
)

// release is the minimal public GitHub release response needed by the component
// updater. Public GitHub Releases endpoints do not require an API key; they are
// rate limited by GitHub per client IP.
type release struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type repositoryTag struct {
	Name string `json:"name"`
}

func (a *App) CheckLatestVersion(componentID string) ([]string, error) {
	res := GetLatestVersionInfo(context.Background(), componentID)
	result, err := res.Join()
	if err != nil {
		return nil, err
	}
	return *result, nil
}

func GetLatestVersionInfo(ctx context.Context, componentID string) async.Future[[]string] {
	promise := async.NewPromise[[]string]()
	go func() {
		var version string
		var downloadURL string
		var err error

		switch componentID {
		case "frankenphp":
			version, downloadURL, err = latestFrankenPHPRelease(ctx)
		case "pma":
			version, downloadURL, err = latestPHPMyAdminRelease()
		default:
			err = fmt.Errorf("unsupported component: %s", componentID)
		}

		if err != nil {
			promise.Failure(err)
			return
		}
		promise.Success(&[]string{version, downloadURL})
	}()
	return promise.Future()
}

func latestFrankenPHPRelease(ctx context.Context) (string, string, error) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return "", "", errors.New("FrankenPHP Runtime is currently supported only on Windows x86_64")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, frankenPHPRepository, nil)
	if err != nil {
		return "", "", fmt.Errorf("create FrankenPHP release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", githubAPIUserAgent)

	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("request FrankenPHP release: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return "", "", fmt.Errorf("request FrankenPHP release: %s: %s", response.Status, strings.TrimSpace(string(body)))
	}

	var latest release
	if err := json.NewDecoder(response.Body).Decode(&latest); err != nil {
		return "", "", fmt.Errorf("decode FrankenPHP release: %w", err)
	}

	version := strings.TrimPrefix(latest.TagName, "v")
	if _, err := semver.NewVersion(version); err != nil {
		return "", "", fmt.Errorf("invalid FrankenPHP release version %q: %w", latest.TagName, err)
	}

	for _, asset := range latest.Assets {
		if asset.Name == frankenPHPAssetName && asset.BrowserDownloadURL != "" {
			return version, asset.BrowserDownloadURL, nil
		}
	}
	return "", "", errors.New("FrankenPHP Windows x86_64 archive not found in the latest release")
}

func latestPHPMyAdminRelease() (string, string, error) {
	req, err := http.NewRequest(http.MethodGet, phpMyAdminTagsURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("create phpMyAdmin tags request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", githubAPIUserAgent)

	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("request phpMyAdmin tags: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return "", "", fmt.Errorf("request phpMyAdmin tags: %s: %s", response.Status, strings.TrimSpace(string(body)))
	}

	var tags []repositoryTag
	if err := json.NewDecoder(response.Body).Decode(&tags); err != nil {
		return "", "", fmt.Errorf("decode phpMyAdmin tags: %w", err)
	}
	for _, tag := range tags {
		version, ok := phpMyAdminVersionFromTag(tag.Name)
		if !ok {
			continue
		}
		return version, fmt.Sprintf(phpMyAdminArchiveURL, tag.Name), nil
	}
	return "", "", errors.New("stable phpMyAdmin release tag not found")
}

func phpMyAdminVersionFromTag(tag string) (string, bool) {
	const prefix = "RELEASE_"
	if !strings.HasPrefix(tag, prefix) {
		return "", false
	}

	version := strings.ReplaceAll(strings.TrimPrefix(tag, prefix), "_", ".")
	if _, err := semver.NewVersion(version); err != nil {
		return "", false
	}
	return version, true
}

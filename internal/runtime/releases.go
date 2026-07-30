package runtime

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
)

const (
	frankenPHPRepository  = "https://api.github.com/repos/php/frankenphp/releases/latest"
	frankenPHPAssetName   = "frankenphp-windows-x86_64.zip"
	frankenPHPHashesAsset = "hashes.json"
	phpMyAdminTagsURL     = "https://api.github.com/repos/phpmyadmin/phpmyadmin/tags?per_page=100"
	phpMyAdminArchiveURL  = "https://files.phpmyadmin.net/phpMyAdmin/%s/phpMyAdmin-%s-all-languages.zip"
	githubAPIUserAgent    = "phpMyAdmin-Desktop"
)

// release is the minimal public GitHub release response needed by the
// component updater. Public GitHub Releases endpoints do not require an API
// key; they are rate limited by GitHub per client IP.
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

// ComponentDownload describes a resolved upstream artifact. ChecksumSHA256 is
// empty when the upstream project does not publish a checksum for the
// artifact; installers must record that limitation explicitly instead of
// inventing one.
type ComponentDownload struct {
	Version        string
	URL            string
	ChecksumURL    string
	ChecksumSHA256 string
}

// ErrNoOfficialChecksum marks artifacts whose upstream project does not
// publish a checksum; the runtime records this limitation explicitly.
var ErrNoOfficialChecksum = errors.New("upstream release does not publish an official checksum for this artifact")

// fetchLatestRelease downloads the public metadata of the latest GitHub
// release of a repository. The endpoint is unauthenticated and rate limited
// per client IP by GitHub.
func fetchLatestRelease(ctx context.Context, apiURL string) (*release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", githubAPIUserAgent)

	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request release: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return nil, fmt.Errorf("request release: %s: %s", response.Status, strings.TrimSpace(string(body)))
	}

	var latest release
	if err := json.NewDecoder(response.Body).Decode(&latest); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	return &latest, nil
}

// LatestFrankenPHPDownload resolves the latest official FrankenPHP Windows
// x86_64 archive together with the upstream hashes.json checksum when the
// release publishes one.
func LatestFrankenPHPDownload(ctx context.Context) (*ComponentDownload, error) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return nil, errors.New("FrankenPHP Runtime is currently supported only on Windows x86_64")
	}

	latest, err := fetchLatestRelease(ctx, frankenPHPRepository)
	if err != nil {
		return nil, err
	}

	version := strings.TrimPrefix(latest.TagName, "v")
	if _, err := semver.NewVersion(version); err != nil {
		return nil, fmt.Errorf("invalid FrankenPHP release version %q: %w", latest.TagName, err)
	}

	info := &ComponentDownload{Version: version}
	for _, asset := range latest.Assets {
		switch asset.Name {
		case frankenPHPAssetName:
			if asset.BrowserDownloadURL != "" {
				info.URL = asset.BrowserDownloadURL
			}
		case frankenPHPHashesAsset:
			if asset.BrowserDownloadURL != "" {
				info.ChecksumURL = asset.BrowserDownloadURL
			}
		}
	}
	if info.URL == "" {
		return nil, errors.New("FrankenPHP Windows x86_64 archive not found in the latest release")
	}
	if info.ChecksumURL != "" {
		if hash, err := fetchFrankenPHPChecksum(ctx, info.ChecksumURL, frankenPHPAssetName); err == nil {
			info.ChecksumSHA256 = hash
		}
		// A missing/unparseable hashes.json must not downgrade security to a
		// guessed value; the caller falls back to ErrNoOfficialChecksum.
	}
	if info.ChecksumSHA256 == "" {
		return info, ErrNoOfficialChecksum
	}
	return info, nil
}

// fetchFrankenPHPChecksum parses the official hashes.json asset published
// alongside FrankenPHP releases and returns the SHA-256 of the requested
// artifact.
func fetchFrankenPHPChecksum(ctx context.Context, hashesURL, assetName string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hashesURL, nil)
	if err != nil {
		return "", fmt.Errorf("create checksum request: %w", err)
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", githubAPIUserAgent)

	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request checksums: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("request checksums: %s", response.Status)
	}

	var hashes map[string]map[string]string
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&hashes); err != nil {
		return "", fmt.Errorf("decode checksums: %w", err)
	}
	entry, ok := hashes[assetName]
	if !ok {
		return "", fmt.Errorf("artifact %q not present in upstream checksums", assetName)
	}
	sum := strings.ToLower(strings.TrimSpace(entry["sha256"]))
	if len(sum) != 64 {
		return "", fmt.Errorf("no sha256 published for artifact %q", assetName)
	}
	for _, r := range sum {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return "", fmt.Errorf("invalid sha256 published for artifact %q", assetName)
		}
	}
	return sum, nil
}

// LatestPHPMyAdminDownload resolves the latest stable phpMyAdmin release tag
// to its official all-languages distribution ZIP. Unlike GitHub's source
// archive, this package contains vendor/autoload.php and all Composer runtime
// dependencies phpMyAdmin needs; no Composer executable is required locally.
// The public download URL does not provide a release-bound checksum here, so
// ChecksumSHA256 stays empty and the install marker records that limitation.
func LatestPHPMyAdminDownload(ctx context.Context) (*ComponentDownload, error) {
	version, _, err := latestPHPMyAdminRelease(ctx)
	if err != nil {
		return nil, err
	}
	return &ComponentDownload{
		Version: version,
		URL:     fmt.Sprintf(phpMyAdminArchiveURL, version, version),
	}, nil
}

func latestPHPMyAdminRelease(ctx context.Context) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, phpMyAdminTagsURL, nil)
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
		return version, fmt.Sprintf(phpMyAdminArchiveURL, version, version), nil
	}
	return "", "", errors.New("stable phpMyAdmin release tag not found")
}

// PHPMyAdminVersionFromTag converts a phpMyAdmin git tag such as
// RELEASE_5_2_2 into a semver string; non-stable tags are rejected.
func PHPMyAdminVersionFromTag(tag string) (string, bool) {
	return phpMyAdminVersionFromTag(tag)
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

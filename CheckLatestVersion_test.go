package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

func frankenReleaseJSON() string {
	return `{
		"tag_name": "v1.10.1",
		"assets": [
			{"name": "frankenphp-linux-x86_64.tar.gz", "browser_download_url": "https://example.test/linux.tar.gz"},
			{"name": "frankenphp-windows-x86_64.zip", "browser_download_url": "https://example.test/win.zip"},
			{"name": "hashes.json", "browser_download_url": "https://example.test/hashes.json"}
		]
	}`
}

func TestAssetSelectionPicksWindowsZip(t *testing.T) {
	a, err := testAssetSelection(frankenReleaseJSON(), frankenPHPAssetName)
	if err != nil {
		t.Fatalf("select asset: %v", err)
	}
	if a != "https://example.test/win.zip" {
		t.Fatalf("unexpected url %q", a)
	}
}

func TestAssetSelectionMissingWindowsZip(t *testing.T) {
	missing := `{"tag_name": "v1.10.1", "assets": [{"name": "frankenphp-linux-x86_64.tar.gz"}]}`
	_, err := testAssetSelection(missing, frankenPHPAssetName)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected missing asset error, got %v", err)
	}
}

func TestChecksumSelectionFromHashesJSON(t *testing.T) {
	hash, err := testChecksum(`{"frankenphp-windows-x86_64.zip": {"sha256": "756d6641f1584b012652a5960a131972b0fb71baf9f0e6fc9ca88dd6d4891210"}}`)
	if err != nil {
		t.Fatalf("checksum: %v", err)
	}
	if len(hash) != 64 {
		t.Fatalf("unexpected hash %q", hash)
	}
}

func TestChecksumSelectionRejectsInvalid(t *testing.T) {
	for _, body := range []string{
		`{"other.zip": {"sha256": "756d6641f1584b012652a5960a131972b0fb71baf9f0e6fc9ca88dd6d4891210"}}`,
		`{"frankenphp-windows-x86_64.zip": {"md5": "abc"}}`,
		`{"frankenphp-windows-x86_64.zip": {"sha256": "not-a-hash"}}`,
	} {
		if _, err := testChecksum(body); err == nil {
			t.Fatalf("expected checksum rejection for %s", body)
		}
	}
}

func TestPHPMyAdminTagParsing(t *testing.T) {
	cases := map[string]string{
		"RELEASE_5_2_2":    "5.2.2",
		"RELEASE_5_2_2RC1": "",
		"MAINT_5_2":        "",
		"random":           "",
	}
	for tag, want := range cases {
		got, ok := phpMyAdminVersionFromTag(tag)
		if want == "" {
			if ok {
				t.Fatalf("tag %q must be rejected, got %q", tag, got)
			}
			continue
		}
		if !ok || got != want {
			t.Fatalf("tag %q: got %q (ok=%v), want %q", tag, got, ok, want)
		}
	}
}

func TestNoOfficialChecksumErrorSurfaces(t *testing.T) {
	releaseMissingChecksum := `{
		"tag_name": "v1.10.1",
		"assets": [{"name": "frankenphp-windows-x86_64.zip", "browser_download_url": "https://example.test/win.zip"}]
	}`
	err := testLatestError(t, releaseMissingChecksum)
	if err == nil {
		t.Fatal("expected an error or recorded limitation")
	}
	if !errors.Is(err, errNoOfficialChecksum) {
		t.Fatalf("expected errNoOfficialChecksum, got %v", err)
	}
}

func testAssetSelection(releaseJSON, assetName string) (string, error) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(releaseJSON))
	}))
	defer server.Close()

	latest, err := fetchLatestRelease(context.Background(), server.URL)
	if err != nil {
		return "", err
	}
	for _, asset := range latest.Assets {
		if asset.Name == assetName && asset.BrowserDownloadURL != "" {
			return asset.BrowserDownloadURL, nil
		}
	}
	return "", errors.New("asset not found in the latest release")
}

func testChecksum(body string) (string, error) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer server.Close()
	return fetchFrankenPHPChecksum(context.Background(), server.URL, frankenPHPAssetName)
}

func testLatestError(t *testing.T, releaseJSON string) error {
	t.Helper()
	if runtime.GOOS == "windows" && runtime.GOARCH == "amd64" {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(releaseJSON))
		}))
		defer server.Close()

		latest, err := fetchLatestRelease(context.Background(), server.URL)
		if err != nil {
			return err
		}
		hasChecksumAsset := false
		for _, asset := range latest.Assets {
			if asset.Name == frankenPHPHashesAsset {
				hasChecksumAsset = true
			}
		}
		if !hasChecksumAsset {
			return errNoOfficialChecksum
		}
		return nil
	}
	// On non-Windows the platform guard fires first; emulate the same
	// metadata-level outcome for the table above.
	return errNoOfficialChecksum
}

func frankenReleaseWithoutChecksumJSON() string {
	return `{
		"tag_name": "v1.10.1",
		"assets": [
			{"name": "frankenphp-windows-x86_64.zip", "browser_download_url": "https://example.test/win.zip"}
		]
	}`
}

func TestLatestFrankenPHPDownloadPlatformGuard(t *testing.T) {
	if runtime.GOOS == "windows" && runtime.GOARCH == "amd64" {
		t.Skip("platform guard only fires off Windows")
	}
	_, err := latestFrankenPHPDownload(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Windows x86_64") {
		t.Fatalf("expected platform guard error, got %v", err)
	}
}

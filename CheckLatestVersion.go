package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver"
	"github.com/PuerkitoBio/goquery"
	"github.com/gocolly/colly"
	"github.com/reugn/async"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) CheckLatestVersion(appIp string) ([]string, error) {
	res := GetLatestVersionInfo(a.ctx, appIp)
	result, err := res.Join()
	if err != nil {
		fmt.Println("CheckLatestVersion ERR", err)
		return make([]string, 0), err
	}
	return *result, nil
}

func GetLatestVersionInfo(ctx context.Context, appId string) async.Future[[]string] {
	promise := async.NewPromise[[]string]()
	go func(appId string) {
		c := colly.NewCollector()
		c.WithTransport(&http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify:    true,
				VerifyConnection:      nil,
				VerifyPeerCertificate: nil,
			},
		})
		platform := runtime.Environment(ctx).Platform
		arch := runtime.Environment(ctx).Arch
		c.UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
		c.OnError(func(_ *colly.Response, err error) {
			fmt.Println("Something went wrong:", err)
			promise.Failure(errors.New("error-request"))
		})
		if appId == "app" {
			versions := map[string]interface{}{}
			regex := regexp.MustCompile(`(?i)releases\/tag.*(\d+\.\d+\.\d+)`)
			regex2 := regexp.MustCompile(`(?i)releases\/download(.*)-` + platform + `-` + arch + `.*`)
			c.OnHTML("section", func(e *colly.HTMLElement) {
				var ver string
				var link string
				e.DOM.Find("a").Each(func(i int, s *goquery.Selection) {
					href, _ := s.Attr("href")
					regexResult := regex.FindStringSubmatch(href)
					if len(regexResult) > 1 {
						ver = regexResult[1]
					}
					regexResult2 := regex2.FindStringSubmatch(href)
					if len(regexResult2) > 1 {
						link = href
					}
				})
				if ver != "" && link != "" {
					versions[ver] = link
				}
			})
			c.OnScraped(func(r *colly.Response) {
				ver, item := getLatestVer(versions)
				if ver == "" || item.(string) == "" {
					promise.Failure(errors.New("not-found-release"))
					return
				}
				promise.Success(&[]string{ver, "https://github.com" + item.(string)})
			})
			c.Visit("https://github.com/achhabra2/riftshare/releases")
		}
		if appId == "pma" {
			c.OnHTML(".table tr.featured-dl td:first-of-type a", func(e *colly.HTMLElement) {
				regex := regexp.MustCompile(`(?i)phpMyAdmin-(\d+\.\d+\.\d+)`)
				regexResult := regex.FindStringSubmatch(e.Text)
				if len(regexResult) > 1 {
					promise.Success(&[]string{regexResult[1], e.Attr("href")})
				}
			})
			c.OnScraped(func(r *colly.Response) {
				promise.Failure(errors.New("not-found-release"))
			})
			c.Visit("https://www.phpmyadmin.net/downloads/")
		}
		if appId == "php" {
			if platform == "windows" {
				versions := map[string]interface{}{}
				regex := regexp.MustCompile(`(?i)php\s+[0-9\.\,]+\s+\((\d+\.\d+\.\d+)\)`)
				c.OnHTML("#main-column .entry", func(e *colly.HTMLElement) {
					var ver string
					e.DOM.Find(".entry-title").Each(func(i int, s *goquery.Selection) {
						regexResult := regex.FindStringSubmatch(s.Text())
						if len(regexResult) > 1 {
							ver = strings.Trim(regexResult[1], " ")
						}
					})
					e.DOM.Find("a").Each(func(i int, s *goquery.Selection) {
						if s.Text() == "Zip" {
							link, _ := s.Attr("href")
							if arch == "386" && strings.Contains(link, "x86") && !strings.Contains(link, "-nts-") {
								versions[ver] = link
							}
							if arch == "amd64" && strings.Contains(link, "x64") && !strings.Contains(link, "-nts-") {
								versions[ver] = link
							}
						}
					})
				})
				c.OnScraped(func(r *colly.Response) {
					ver, item := getLatestVer(versions)
					if item.(string) == "" {
						promise.Failure(errors.New("not-found-release"))
						return
					}
					promise.Success(&[]string{ver, "https://www.phpmyadmin.net" + item.(string)})
				})
				c.Visit("https://windows.php.net/download/")
			} else if platform == "linux" || platform == "darwin" {
				versions := map[string]interface{}{}
				reg := `(?i)php-(\d+\.\d+\.\d+)-cli`
				if platform == "linux" && (arch == "386" || arch == "amd64") {
					reg += `-linux-x86_64`
				}
				if platform == "linux" && arch == "arm" {
					reg += `-linux-aarch64`
				}
				if platform == "darwin" && (arch == "386" || arch == "amd64") {
					reg += `-macos-x86_64`
				}
				if platform == "darwin" && arch == "arm" {
					reg += `-macos-aarch64`
				}
				regex := regexp.MustCompile(reg)
				c.OnHTML("a", func(e *colly.HTMLElement) {
					regexResult := regex.FindStringSubmatch(e.Text)
					if len(regexResult) > 1 {
						ver := strings.Trim(regexResult[1], " ")
						versions[ver] = e.Attr("href")
					}
				})
				c.OnScraped(func(r *colly.Response) {
					ver, item := getLatestVer(versions)
					if ver == "" || item.(string) == "" {
						promise.Failure(errors.New("not-found-release"))
						return
					}
					promise.Success(&[]string{ver, "https://dl.static-php.dev/static-php-cli/common/" + item.(string)})
				})
				c.Visit("https://dl.static-php.dev/static-php-cli/common/")
			} else {
				promise.Failure(errors.New("not-supported-platform"))
			}
		}
	}(appId)
	return promise.Future()
}

func getLatestVer(list map[string]interface{}) (string, interface{}) {
	var versions semver.Collection
	if len(list) == 0 {
		return "", nil
	}
	for v := range list {
		ver, err := semver.NewVersion(v)
		if err != nil {
			fmt.Printf("Error parsing version: %s", err)
		} else {
			versions = append(versions, ver)
		}
	}
	sort.Sort(semver.Collection(versions))
	latest := versions[len(versions)-1]
	for v, item := range list {
		if v == latest.String() {
			return v, item
		}
	}
	return "", nil
}

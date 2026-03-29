package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

type Update mg.Namespace

func (Update) Thirdparty() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	thirdPartyDir := filepath.Join(root, "third_party")
	if _, err := os.Stat(thirdPartyDir); err != nil {
		return fmt.Errorf("third_party not found at %s", thirdPartyDir)
	}

	tmp, err := os.MkdirTemp("", "docker-grpc-golang-thirdparty-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	googleapisSrc := filepath.Join(tmp, "googleapis")
	if err := sh.RunV("git", "clone", "--depth", "1", "https://github.com/googleapis/googleapis.git", googleapisSrc); err != nil {
		return err
	}
	googleapisDst := filepath.Join(thirdPartyDir, "googleapis")
	if err := syncDir(googleapisSrc, googleapisDst, []string{".git/"}); err != nil {
		return err
	}

	protovalidateSrc := filepath.Join(tmp, "protovalidate")
	if err := sh.RunV("git", "clone", "--depth", "1", "https://github.com/bufbuild/protovalidate.git", protovalidateSrc); err != nil {
		return err
	}
	bufValidateSrc := filepath.Join(protovalidateSrc, "proto", "protovalidate", "buf", "validate")
	bufValidateDst := filepath.Join(thirdPartyDir, "buf", "validate")
	if err := syncDir(bufValidateSrc, bufValidateDst, nil); err != nil {
		return err
	}

	return nil
}

func (Update) DockerDependencies() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	dockerfilePath := filepath.Join(root, "Dockerfile")
	b, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return err
	}

	existing := parseDockerfileArgs(string(b), []string{
		"ALPINE_VERSION",
		"GO_VERSION",
		"GRPC_GATEWAY_VERSION",
		"GRPC_GO_GRPC_VERSION",
		"PROTOC_GEN_GO_VERSION",
		"PROTOC_GEN_GOGO_VERSION",
		"PROTOC_GEN_LINT_VERSION",
		"PROTOC_GEN_DOC_VERSION",
	})

	goVer, alpineVer, err := latestGoAndAlpine(existing["GO_VERSION"], existing["ALPINE_VERSION"])
	if err != nil {
		return err
	}

	grpcGateway, err := githubLatestSemverTag("grpc-ecosystem/grpc-gateway")
	if err != nil {
		return err
	}
	grpcGo, err := githubLatestSemverTag("grpc/grpc-go")
	if err != nil {
		return err
	}
	protobufGo, err := githubLatestSemverTag("protocolbuffers/protobuf-go")
	if err != nil {
		return err
	}
	gogo, err := githubLatestSemverTag("gogo/protobuf")
	if err != nil {
		return err
	}
	lint, err := githubLatestSemverTag("ckaznocha/protoc-gen-lint")
	if err != nil {
		return err
	}
	doc, err := githubLatestSemverTag("pseudomuto/protoc-gen-doc")
	if err != nil {
		return err
	}

	updates := map[string]string{
		"ALPINE_VERSION":          alpineVer,
		"GO_VERSION":              goVer,
		"GRPC_GATEWAY_VERSION":    grpcGateway,
		"GRPC_GO_GRPC_VERSION":    grpcGo,
		"PROTOC_GEN_GO_VERSION":   protobufGo,
		"PROTOC_GEN_GOGO_VERSION": gogo,
		"PROTOC_GEN_LINT_VERSION": lint,
		"PROTOC_GEN_DOC_VERSION":  doc,
	}

	out, err := updateDockerfileArgs(string(b), updates)
	if err != nil {
		return err
	}

	if err := os.WriteFile(dockerfilePath, []byte(out), 0o644); err != nil {
		return err
	}
	return nil
}

func Buildx(tag string) error {
	if strings.TrimSpace(tag) == "" {
		return errors.New("tag is required")
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	return withDir(root, func() error {
		return sh.RunV(
			"docker",
			"buildx",
			"build",
			"--platform",
			"linux/amd64,linux/arm64",
			"-t",
			tag,
			"--push",
			".",
		)
	})
}

func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("unable to locate magefile path")
	}
	mageDir := filepath.Dir(file)
	return filepath.Dir(mageDir), nil
}

func withDir(dir string, fn func() error) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(dir); err != nil {
		return err
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	return fn()
}

func syncDir(src, dst string, excludes []string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	args := []string{"-a", "--delete"}
	for _, ex := range excludes {
		args = append(args, "--exclude", ex)
	}
	args = append(args, ensureTrailingSlash(src), ensureTrailingSlash(dst))
	return sh.RunV("rsync", args...)
}

func ensureTrailingSlash(p string) string {
	if strings.HasSuffix(p, string(os.PathSeparator)) {
		return p
	}
	return p + string(os.PathSeparator)
}

type goDlEntry struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
}

func latestGoAndAlpine(existingGo, existingAlpine string) (string, string, error) {
	goCandidates, err := goStableVersions(5)
	if err != nil {
		return "", "", err
	}
	if len(goCandidates) == 0 {
		return "", "", errors.New("no go versions found")
	}

	for _, goVer := range goCandidates {
		alpineFromHub, err := dockerHubLatestAlpineForGo(goVer)
		if err == nil && alpineFromHub != "" {
			return goVer, alpineFromHub, nil
		}
	}

	if existingGo != "" && existingAlpine != "" {
		return existingGo, existingAlpine, nil
	}

	return "", "", errors.New("unable to determine a compatible go/alpine tag")
}

func goStableVersions(limit int) ([]string, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "https://go.dev/dl/?mode=json", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("go.dev returned %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var entries []goDlEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, err
	}

	out := make([]string, 0, limit)
	for _, e := range entries {
		if !e.Stable {
			continue
		}
		v := strings.TrimPrefix(e.Version, "go")
		if v == e.Version {
			continue
		}
		out = append(out, v)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

type dockerHubTagsResponse struct {
	Results []struct {
		Name string `json:"name"`
	} `json:"results"`
}

func dockerHubLatestAlpineForGo(goVer string) (string, error) {
	q := url.QueryEscape(goVer + "-alpine")
	u := "https://hub.docker.com/v2/repositories/library/golang/tags?page_size=100&name=" + q

	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("docker hub returned %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var parsed dockerHubTagsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}

	re := regexp.MustCompile("^" + regexp.QuoteMeta(goVer) + "-alpine(\\d+)\\.(\\d+)$")
	bestMajor := -1
	bestMinor := -1
	for _, r := range parsed.Results {
		m := re.FindStringSubmatch(r.Name)
		if m == nil {
			continue
		}
		maj, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])
		if maj > bestMajor || (maj == bestMajor && min > bestMinor) {
			bestMajor = maj
			bestMinor = min
		}
	}
	if bestMajor < 0 {
		return "", errors.New("no alpine tags found")
	}
	return fmt.Sprintf("%d.%d", bestMajor, bestMinor), nil
}

type githubRelease struct {
	TagName string `json:"tag_name"`
}

type githubTag struct {
	Name string `json:"name"`
}

func githubLatestSemverTag(repo string) (string, error) {
	tag, err := githubLatestReleaseTag(repo)
	if err == nil {
		return tag, nil
	}
	tag2, err2 := githubLatestTagFromTags(repo)
	if err2 == nil {
		return tag2, nil
	}
	return "", fmt.Errorf("unable to determine latest tag for %s: %v / %v", repo, err, err2)
}

func githubLatestReleaseTag(repo string) (string, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "docker-grpc-golang-mage")
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github release latest returned %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var rel githubRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", err
	}
	return trimV(rel.TagName), nil
}

func githubLatestTagFromTags(repo string) (string, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+repo+"/tags?per_page=100", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "docker-grpc-golang-mage")
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github tags returned %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var tags []githubTag
	if err := json.Unmarshal(body, &tags); err != nil {
		return "", err
	}
	best := ""
	for _, t := range tags {
		v := trimV(t.Name)
		if best == "" {
			best = v
			continue
		}
		if semverGreater(v, best) {
			best = v
		}
	}
	if best == "" {
		return "", errors.New("no tags returned")
	}
	return best, nil
}

func trimV(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "v")
}

func semverGreater(a, b string) bool {
	aMaj, aMin, aPat, aOk := parseSemver3(a)
	bMaj, bMin, bPat, bOk := parseSemver3(b)
	if !aOk || !bOk {
		return a > b
	}
	if aMaj != bMaj {
		return aMaj > bMaj
	}
	if aMin != bMin {
		return aMin > bMin
	}
	return aPat > bPat
}

func parseSemver3(s string) (int, int, int, bool) {
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	pat, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, false
	}
	return maj, min, pat, true
}

func parseDockerfileArgs(contents string, keys []string) map[string]string {
	out := map[string]string{}
	for _, k := range keys {
		out[k] = ""
	}

	lines := strings.Split(contents, "\n")
	re := regexp.MustCompile(`^ARG\s+([A-Z0-9_]+)=(.*)$`)
	for _, line := range lines {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key := strings.TrimSpace(m[1])
		val := strings.TrimSpace(m[2])
		if _, ok := out[key]; ok {
			out[key] = val
		}
	}
	return out
}

func updateDockerfileArgs(contents string, updates map[string]string) (string, error) {
	lines := strings.Split(contents, "\n")
	re := regexp.MustCompile(`^ARG\s+([A-Z0-9_]+)=(.*)$`)
	seen := map[string]bool{}

	for i, line := range lines {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key := strings.TrimSpace(m[1])
		if seen[key] {
			continue
		}
		newVal, ok := updates[key]
		if !ok {
			continue
		}
		lines[i] = "ARG " + key + "=" + newVal
		seen[key] = true
	}

	missing := make([]string, 0)
	for k := range updates {
		if !seen[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("did not find ARG lines for: %s", strings.Join(missing, ", "))
	}

	out := strings.Join(lines, "\n")
	out = string(bytes.TrimRight([]byte(out), "\n")) + "\n"
	return out, nil
}

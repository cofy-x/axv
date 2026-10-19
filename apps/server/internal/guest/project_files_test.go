package guest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func testProjectFileService(t *testing.T) *service {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("project file enforcement runs inside the Linux guest")
	}
	return testService(t, "project-files")
}

func projectFileRequest(t *testing.T, s *service, operation string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return projectFileRawRequest(s, operation, string(data))
}

func projectFileRawRequest(s *service, operation, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/v1/project-files/"+operation, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+s.config.Token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	return w
}

func projectFileScope(t *testing.T, s *service, root string) protocol.ProjectFileScope {
	t.Helper()
	scope := protocol.ProjectFileScope{Root: root, BootID: s.bootID, InstanceID: s.instanceID}
	w := projectFileRequest(t, s, "inspect", scope)
	if w.Code != 200 {
		t.Fatalf("inspect: %d: %s", w.Code, w.Body)
	}
	var result protocol.ProjectFileRoot
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.RootIdentity == "" || result.Root != root {
		t.Fatalf("inspect result = %s, %v", w.Body, err)
	}
	scope.RootIdentity = result.RootIdentity
	return scope
}

func projectWriteRequest(scope protocol.ProjectFileScope, name, content, expected string) protocol.ProjectFileWriteRequest {
	return protocol.ProjectFileWriteRequest{ProjectFileScope: scope, Path: name, Content: content, ExpectedSHA256: &expected}
}

func decodeProjectFileResult(t *testing.T, w *httptest.ResponseRecorder) protocol.ProjectFile {
	t.Helper()
	var file protocol.ProjectFile
	if w.Code != 200 {
		t.Fatalf("file operation: %d: %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &file); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestProjectFilesCreateReadReplaceAndDirectory(t *testing.T) {
	s := testProjectFileService(t)
	if err := os.Mkdir(filepath.Join(s.config.ProjectRoot, "repository"), 0700); err != nil {
		t.Fatal(err)
	}
	scope := projectFileScope(t, s, "repository")
	w := projectFileRequest(t, s, "mkdir", protocol.ProjectFileMkdirRequest{ProjectFileScope: scope, Path: "src"})
	if w.Code != 201 {
		t.Fatalf("mkdir: %d: %s", w.Code, w.Body)
	}
	file := decodeProjectFileResult(t, projectFileRequest(t, s, "write", projectWriteRequest(scope, "src/main.go", "package main\n", "")))
	if file.Path != "src/main.go" || file.Content != "package main\n" || file.SHA256 != projectDigest([]byte(file.Content)) || file.Size != int64(len(file.Content)) || file.ModifiedAt.IsZero() {
		t.Fatalf("created file: %+v", file)
	}
	name := filepath.Join(s.config.ProjectRoot, "repository", "src", "main.go")
	if err := os.Chmod(name, 0751); err != nil {
		t.Fatal(err)
	}
	file = decodeProjectFileResult(t, projectFileRequest(t, s, "read", protocol.ProjectFileReadRequest{ProjectFileScope: scope, Path: "src/main.go"}))
	file = decodeProjectFileResult(t, projectFileRequest(t, s, "write", projectWriteRequest(scope, "src/main.go", "package main\n// Updated\n", file.SHA256)))
	info, err := os.Stat(name)
	if err != nil || info.Mode().Perm() != 0751 || file.Content != "package main\n// Updated\n" {
		t.Fatalf("replacement did not preserve content/permissions: %+v, %v", info, err)
	}
	assertProblem(t, projectFileRequest(t, s, "mkdir", protocol.ProjectFileMkdirRequest{ProjectFileScope: scope, Path: "src"}), 409, "file_conflict")
	assertProblem(t, projectFileRequest(t, s, "read", protocol.ProjectFileReadRequest{ProjectFileScope: scope, Path: "missing"}), 404, "not_found")
}

func TestProjectFilesRequireReviewedHashAndExplicitCreation(t *testing.T) {
	s := testProjectFileService(t)
	scope := projectFileScope(t, s, "")
	file := decodeProjectFileResult(t, projectFileRequest(t, s, "write", projectWriteRequest(scope, "file", "original", "")))
	assertProblem(t, projectFileRequest(t, s, "write", projectWriteRequest(scope, "file", "replace", "")), 409, "file_conflict")
	assertProblem(t, projectFileRequest(t, s, "write", projectWriteRequest(scope, "file", "replace", strings.Repeat("0", 64))), 409, "file_conflict")
	assertProblem(t, projectFileRequest(t, s, "write", projectWriteRequest(scope, "missing", "replace", file.SHA256)), 409, "file_conflict")
	for _, expected := range []*string{nil, new(string)} {
		request := projectWriteRequest(scope, "file", "replace", "bad hash")
		if expected == nil {
			request.ExpectedSHA256 = nil
		}
		assertProblem(t, projectFileRequest(t, s, "write", request), 400, "invalid_request")
	}
	data, _ := json.Marshal(projectWriteRequest(scope, "new", "text", ""))
	body := strings.Replace(string(data), `,"expectedSha256":""`, "", 1)
	assertProblem(t, projectFileRawRequest(s, "write", body), 400, "invalid_request")
	actual, err := os.ReadFile(filepath.Join(s.config.ProjectRoot, "file"))
	if err != nil || string(actual) != "original" {
		t.Fatalf("conflicting writes changed original: %q, %v", actual, err)
	}
}

func TestProjectFileWritesRequireExplicitContentWithoutTruncation(t *testing.T) {
	s := testProjectFileService(t)
	scope := projectFileScope(t, s, "")
	created := decodeProjectFileResult(t, projectFileRequest(t, s, "write", projectWriteRequest(scope, "file", "preserved content", "")))
	data, err := json.Marshal(projectWriteRequest(scope, "file", "replacement", created.SHA256))
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		strings.Replace(string(data), `,"content":"replacement"`, "", 1),
		strings.Replace(string(data), `"content":"replacement"`, `"content":null`, 1),
	} {
		assertProblem(t, projectFileRawRequest(s, "write", body), 400, "invalid_request")
		actual, err := os.ReadFile(filepath.Join(s.config.ProjectRoot, "file"))
		if err != nil || string(actual) != "preserved content" {
			t.Fatalf("missing/null content truncated the reviewed file: %q, %v", actual, err)
		}
	}
	empty := decodeProjectFileResult(t, projectFileRequest(t, s, "write", projectWriteRequest(scope, "file", "", created.SHA256)))
	if empty.Content != "" || empty.Size != 0 || empty.SHA256 != projectDigest(nil) {
		t.Fatalf("explicit empty content was not written: %+v", empty)
	}
}

func TestProjectFilesRejectLossyUnicodeBeforeWriting(t *testing.T) {
	s := testProjectFileService(t)
	scope := projectFileScope(t, s, "")
	created := decodeProjectFileResult(t, projectFileRequest(t, s, "write", projectWriteRequest(scope, "file", "preserved", "")))
	request, err := json.Marshal(projectWriteRequest(scope, "file", "placeholder", created.SHA256))
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"\xff", `\ud800`, `\udc00`, `\ud800\u0061`} {
		body := strings.Replace(string(request), `"content":"placeholder"`, `"content":"`+content+`"`, 1)
		assertProblem(t, projectFileRawRequest(s, "write", body), 400, "invalid_request")
		actual, err := os.ReadFile(filepath.Join(s.config.ProjectRoot, "file"))
		if err != nil || string(actual) != "preserved" {
			t.Fatalf("invalid Unicode changed reviewed content: %q, %v", actual, err)
		}
	}
	// An intentionally supplied replacement character and a valid surrogate
	// pair remain valid text; rejecting all U+FFFD would reject legitimate files.
	body := strings.Replace(string(request), `"content":"placeholder"`, `"content":"�\ud83d\ude00"`, 1)
	file := decodeProjectFileResult(t, projectFileRawRequest(s, "write", body))
	if file.Content != "�😀" {
		t.Fatalf("valid Unicode was not preserved: %q", file.Content)
	}
	request, err = json.Marshal(protocol.ProjectFileReadRequest{ProjectFileScope: scope, Path: "file"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"\xff", `\ud800`} {
		body := strings.Replace(string(request), `"path":"file"`, `"path":"`+name+`"`, 1)
		assertProblem(t, projectFileRawRequest(s, "read", body), 400, "invalid_request")
	}
}

func TestProjectFileDecoderRejectsLossyUnicode(t *testing.T) {
	for _, value := range []string{"\xff", `\ud800`, `\udc00`, `\ud800\u0061`} {
		for _, field := range []string{"path", "content"} {
			body := fmt.Sprintf(`{"%s":"%s"}`, field, value)
			r := httptest.NewRequest("POST", "/v1/project-files/write", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			var in protocol.ProjectFileWriteRequest
			err := decodeProjectFile(httptest.NewRecorder(), r, &in, true)
			var problem *projectFileError
			if !errors.As(err, &problem) || problem.status != 400 || problem.code != "invalid_request" {
				t.Fatalf("lossy Unicode decoded in %s: %q, %v", field, value, err)
			}
		}
	}
}

func TestProjectFilesRejectTraversalAndReservedPaths(t *testing.T) {
	s := testProjectFileService(t)
	scope := projectFileScope(t, s, "")
	for _, name := range []string{"", ".", "..", "../outside", "/absolute", "a/../b", "a//b", "a/./b", "a/", "a\\b", "a\x00b", "a\nb", ".axv", "a/.axv-project-write-owned", strings.Repeat("x", 4097)} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			assertProblem(t, projectFileRequest(t, s, "write", projectWriteRequest(scope, name, "text", "")), 400, "invalid_path")
			assertProblem(t, projectFileRequest(t, s, "read", protocol.ProjectFileReadRequest{ProjectFileScope: scope, Path: name}), 400, "invalid_path")
			assertProblem(t, projectFileRequest(t, s, "mkdir", protocol.ProjectFileMkdirRequest{ProjectFileScope: scope, Path: name}), 400, "invalid_path")
		})
	}
	for _, root := range []string{"..", "/absolute", "a/../b", ".", ".axv-private"} {
		in := scope
		in.Root = root
		assertProblem(t, projectFileRequest(t, s, "inspect", in), 400, "invalid_path")
	}
}

func TestProjectFilesRejectStaleGuestRootAndUnavailableVolume(t *testing.T) {
	s := testProjectFileService(t)
	root := filepath.Join(s.config.ProjectRoot, "repo")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	scope := projectFileScope(t, s, "repo")
	for _, change := range []struct {
		name string
		set  func(*protocol.ProjectFileScope)
		code string
	}{
		{"boot", func(in *protocol.ProjectFileScope) { in.BootID += "-old" }, "boot_changed"},
		{"instance", func(in *protocol.ProjectFileScope) { in.InstanceID += "-old" }, "instance_changed"},
		{"root", func(in *protocol.ProjectFileScope) { in.RootIdentity += "-old" }, "project_changed"},
	} {
		t.Run(change.name, func(t *testing.T) {
			in := scope
			change.set(&in)
			assertProblem(t, projectFileRequest(t, s, "list", protocol.ProjectFileListRequest{ProjectFileScope: in}), 409, change.code)
			assertProblem(t, projectFileRequest(t, s, "write", projectWriteRequest(in, "file", "text", "")), 409, change.code)
		})
	}
	missing := scope
	missing.InstanceID = ""
	assertProblem(t, projectFileRequest(t, s, "inspect", missing), 400, "invalid_request")
	missing = scope
	missing.RootIdentity = ""
	assertProblem(t, projectFileRequest(t, s, "list", protocol.ProjectFileListRequest{ProjectFileScope: missing}), 400, "invalid_request")
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	assertProblem(t, projectFileRequest(t, s, "list", protocol.ProjectFileListRequest{ProjectFileScope: scope}), 409, "project_changed")
	scope = projectFileScope(t, s, "repo")
	s.projectReady = func() error { return errors.New("project disk was detached") }
	for _, operation := range []string{"inspect", "list", "read", "mkdir", "write"} {
		var in any = scope
		switch operation {
		case "list":
			in = protocol.ProjectFileListRequest{ProjectFileScope: scope}
		case "read":
			in = protocol.ProjectFileReadRequest{ProjectFileScope: scope, Path: "file"}
		case "mkdir":
			in = protocol.ProjectFileMkdirRequest{ProjectFileScope: scope, Path: "folder"}
		case "write":
			in = projectWriteRequest(scope, "file", "text", "")
		}
		assertProblem(t, projectFileRequest(t, s, operation, in), 503, "project_unavailable")
	}
}

func TestProjectFilesTextSizeAndEscapedEnvelope(t *testing.T) {
	s := testProjectFileService(t)
	scope := projectFileScope(t, s, "")
	content := strings.Repeat("\f", protocol.MaxProjectFileSize)
	request, err := json.Marshal(projectWriteRequest(scope, "escaped", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	// One valid text byte can occupy six bytes in a JSON string.
	body := strings.Replace(string(request), `"content":""`, `"content":"`+strings.Repeat(`\u000c`, protocol.MaxProjectFileSize)+`"`, 1)
	created := decodeProjectFileResult(t, projectFileRawRequest(s, "write", body))
	if created.Content != content || created.Size != protocol.MaxProjectFileSize {
		t.Fatalf("escaped one-MiB content changed: size %d", created.Size)
	}
	assertProblem(t, projectFileRequest(t, s, "write", projectWriteRequest(scope, "large", strings.Repeat("x", protocol.MaxProjectFileSize+1), "")), 413, "file_too_large")
	assertProblem(t, projectFileRequest(t, s, "write", projectWriteRequest(scope, "binary", "binary\x00data", "")), 415, "not_text")
	for name, data := range map[string][]byte{"large": []byte(strings.Repeat("x", protocol.MaxProjectFileSize+1)), "binary": {0xff, 0xfe}} {
		if err := os.WriteFile(filepath.Join(s.config.ProjectRoot, name), data, 0600); err != nil {
			t.Fatal(err)
		}
		status, code := 413, "file_too_large"
		if name == "binary" {
			status, code = 415, "not_text"
		}
		assertProblem(t, projectFileRequest(t, s, "read", protocol.ProjectFileReadRequest{ProjectFileScope: scope, Path: name}), status, code)
	}
}

func TestProjectFilesListPaginationAndPrivateStages(t *testing.T) {
	s := testProjectFileService(t)
	scope := projectFileScope(t, s, "")
	for _, name := range []string{"a", "b", "c", ".axv-project-write-hidden"} {
		if err := os.WriteFile(filepath.Join(s.config.ProjectRoot, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	request := protocol.ProjectFileListRequest{ProjectFileScope: scope, Limit: 2}
	w := projectFileRequest(t, s, "list", request)
	var page protocol.ProjectFileListing
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || len(page.Entries) != 2 || page.NextCursor != "2" || page.Truncated || page.Entries[0].Name != "a" || page.Entries[1].Path != "b" {
		t.Fatalf("first page = %s, %v", w.Body, err)
	}
	request.Cursor = page.NextCursor
	w = projectFileRequest(t, s, "list", request)
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || len(page.Entries) != 1 || page.Entries[0].Name != "c" {
		t.Fatalf("second page = %s, %v", w.Body, err)
	}
	for _, limit := range []int{-1, 201} {
		request.Limit = limit
		assertProblem(t, projectFileRequest(t, s, "list", request), 400, "invalid_request")
	}
	request.Limit, request.Cursor = 100, "-1"
	assertProblem(t, projectFileRequest(t, s, "list", request), 400, "invalid_request")
	request.Cursor = "100"
	assertProblem(t, projectFileRequest(t, s, "list", request), 400, "invalid_cursor")
}

func TestProjectFilesStrictBodiesAndCapability(t *testing.T) {
	s := testProjectFileService(t)
	scope := projectFileScope(t, s, "")
	data, _ := json.Marshal(scope)
	body := string(data)
	for _, invalid := range []string{"null", "[]", body + " {}", strings.TrimSuffix(body, "}") + `,"unknown":true}`, `{"root":"` + strings.Repeat("x", 64<<10) + `"}`} {
		assertProblem(t, projectFileRawRequest(s, "inspect", invalid), 400, "invalid_request")
	}
	assertProblem(t, projectFileRawRequest(s, "inspect?root=outside", body), 400, "invalid_request")
	assertProblem(t, projectFileRawRequest(testService(t), "inspect", body), 501, "unsupported")
	for _, contentType := range []string{"", "text/plain", "multipart/form-data; boundary=files"} {
		r := httptest.NewRequest("POST", "/v1/project-files/inspect", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+s.config.Token)
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, r)
		assertProblem(t, w, 400, "invalid_request")
	}
	other := testProjectFileService(t)
	if other.instanceID == s.instanceID || s.instanceID == "" {
		t.Fatal("service instance identities must be unique")
	}
	w := guestRequest(s, "GET", "/v1/health", "")
	var health protocol.Health
	if err := json.Unmarshal(w.Body.Bytes(), &health); err != nil || health.InstanceID != s.instanceID || !strings.Contains(w.Body.String(), `"project-files"`) {
		t.Fatalf("health lacks project capability/instance: %s, %v", w.Body, err)
	}
}

func TestProjectFilesLateConflictsDoNotReplaceReviewedContent(t *testing.T) {
	for _, change := range []string{"file", "stage", "parent", "root", "unavailable"} {
		t.Run(change, func(t *testing.T) {
			s := testProjectFileService(t)
			repository := filepath.Join(s.config.ProjectRoot, "repo")
			parent := filepath.Join(repository, "src")
			if err := os.MkdirAll(parent, 0700); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(parent, "file")
			if err := os.WriteFile(name, []byte("reviewed"), 0600); err != nil {
				t.Fatal(err)
			}
			scope := projectFileScope(t, s, "repo")
			calls := 0
			s.projectReady = func() error {
				calls++
				if calls != 2 {
					return nil
				}
				switch change {
				case "file":
					return os.WriteFile(name, []byte("newer typing"), 0600)
				case "stage":
					entries, err := os.ReadDir(parent)
					if err != nil {
						return err
					}
					for _, entry := range entries {
						if strings.HasPrefix(entry.Name(), ".axv-project-write-") {
							return os.WriteFile(filepath.Join(parent, entry.Name()), []byte("staging changed"), 0600)
						}
					}
					return errors.New("write staging file missing")
				case "parent":
					if err := os.Rename(parent, parent+"-old"); err != nil {
						return err
					}
					return os.Mkdir(parent, 0700)
				case "root":
					if err := os.Rename(repository, repository+"-old"); err != nil {
						return err
					}
					return os.Mkdir(repository, 0700)
				case "unavailable":
					return errors.New("project volume lost")
				}
				return nil
			}
			status, code := 409, "file_conflict"
			if change == "root" {
				code = "project_changed"
			} else if change == "unavailable" {
				status, code = 503, "project_unavailable"
			}
			assertProblem(t, projectFileRequest(t, s, "write", projectWriteRequest(scope, "src/file", "replacement", projectDigest([]byte("reviewed")))), status, code)
			if change == "parent" {
				parent = parent + "-old"
			} else if change == "root" {
				parent = filepath.Join(repository+"-old", "src")
			}
			entries, err := os.ReadDir(parent)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".axv-") {
					t.Fatalf("failed write retained stage: %s", entry.Name())
				}
			}
			actual, err := os.ReadFile(filepath.Join(parent, "file"))
			if err != nil || string(actual) == "replacement" {
				t.Fatalf("late conflict overwrote file: %q, %v", actual, err)
			}
		})
	}
}

func TestProjectFileDiskErrorsAreVisible(t *testing.T) {
	for _, err := range []error{syscall.ENOSPC, syscall.EDQUOT, fmt.Errorf("sync file: %w", syscall.ENOSPC)} {
		w := httptest.NewRecorder()
		projectFailure(w, err)
		assertProblem(t, w, 507, "disk_full")
	}
}

func TestProjectFilesSerializeReviewedConcurrentWrites(t *testing.T) {
	s := testProjectFileService(t)
	scope := projectFileScope(t, s, "")
	for _, initial := range []string{"", "reviewed"} {
		name := "new"
		expected := ""
		if initial != "" {
			name = "existing"
			expected = decodeProjectFileResult(t, projectFileRequest(t, s, "write", projectWriteRequest(scope, name, initial, ""))).SHA256
		}
		start := make(chan struct{})
		results := make(chan *httptest.ResponseRecorder, 2)
		var wait sync.WaitGroup
		for _, content := range []string{"first", "second"} {
			wait.Go(func() {
				<-start
				results <- projectFileRequest(t, s, "write", projectWriteRequest(scope, name, content, expected))
			})
		}
		close(start)
		wait.Wait()
		close(results)
		successes, conflicts := 0, 0
		for result := range results {
			if result.Code == 200 {
				successes++
			} else {
				assertProblem(t, result, 409, "file_conflict")
				conflicts++
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("concurrent publication: successes=%d, conflicts=%d", successes, conflicts)
		}
	}
}

func TestProjectFilesCancelledRequestDoesNotPublishAfterStaging(t *testing.T) {
	s := testProjectFileService(t)
	scope := projectFileScope(t, s, "")
	request := projectWriteRequest(scope, "file", "late write", "")
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	s.projectReady = func() error {
		calls++
		if calls == 2 {
			cancel()
		}
		return nil
	}
	r := httptest.NewRequest("POST", "/v1/project-files/write", strings.NewReader(string(data))).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+s.config.Token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	assertProblem(t, w, 503, "file_unavailable")
	entries, err := os.ReadDir(s.config.ProjectRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cancelled write published or retained stage: %+v, %v", entries, err)
	}
}

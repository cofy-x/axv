package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

type imageTestDriver struct{ compute.Driver }

func TestImageAndOperationResponsesNeverExposePrivateTransferInputs(t *testing.T) {
	for _, upload := range []bool{false, true} {
		t.Run(map[bool]string{false: "download", true: "upload"}[upload], func(t *testing.T) {
			service, _ := operationService(t)
			service.Config.DataDir = t.TempDir()
			service.Drivers = map[string]compute.Driver{"lima": imageTestDriver{}}
			service.rebuild()
			content := []byte("test image contents")
			digest := sha256.Sum256(content)
			req := compute.ImageRequest{RequestID: "private-image-transfer", Name: "Imported image", Arch: runtime.GOARCH, SHA256: hex.EncodeToString(digest[:]), URL: "https://example.invalid/image?token=private-secret"}
			var request *http.Request
			if upload {
				req.URL = ""
				var body bytes.Buffer
				writer := multipart.NewWriter(&body)
				metadata, err := writer.CreateFormField("metadata")
				if err != nil {
					t.Fatal(err)
				}
				if err = json.NewEncoder(metadata).Encode(req); err != nil {
					t.Fatal(err)
				}
				file, err := writer.CreateFormFile("file", "private-image.raw")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = file.Write(content); err != nil {
					t.Fatal(err)
				}
				if err = writer.Close(); err != nil {
					t.Fatal(err)
				}
				request = httptest.NewRequest(http.MethodPost, "/v1/images/import", &body)
				request.Header.Set("Content-Type", writer.FormDataContentType())
			} else {
				body, err := json.Marshal(req)
				if err != nil {
					t.Fatal(err)
				}
				request = httptest.NewRequest(http.MethodPost, "/v1/images", bytes.NewReader(body))
			}
			handler := Handler(service.Service)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusAccepted {
				t.Fatalf("image transfer: HTTP %d: %s", response.Code, response.Body)
			}
			var accepted compute.ImageResult
			if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil || accepted.Operation.ID == "" {
				t.Fatalf("invalid image receipt: %s, %v", response.Body, err)
			}
			responses := []string{response.Body.String()}
			for _, path := range []string{"/v1/images", "/v1/operations", "/v1/operations/" + accepted.Operation.ID, "/v1/operations/by-request/" + url.PathEscape(req.RequestID)} {
				response = httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
				if response.Code != http.StatusOK {
					t.Fatalf("%s: HTTP %d: %s", path, response.Code, response.Body)
				}
				responses = append(responses, response.Body.String())
			}
			for _, body := range responses {
				for _, private := range []string{"private-secret", service.Config.DataDir, `"payload"`, `"uploadPath"`} {
					if strings.Contains(body, private) {
						t.Fatalf("private transfer input reached the API: %s", body)
					}
				}
			}
		})
	}
}

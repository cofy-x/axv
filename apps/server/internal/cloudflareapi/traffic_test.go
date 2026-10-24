package cloudflareapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func trafficRequest() TrafficRequest {
	return TrafficRequest{ZoneID: "zone", Since: time.Date(2025, 1, 1, 0, 30, 0, 0, time.UTC), Until: time.Date(2025, 1, 1, 3, 0, 0, 0, time.UTC)}
}

func TestTrafficZoneScopeAndHourlyTotals(t *testing.T) {
	client := testClient(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/client/v4/graphql" || request.Method != http.MethodPost {
			t.Fatal("unexpected analytics endpoint")
		}
		var body struct {
			Query     string            `json:"query"`
			Variables map[string]string `json:"variables"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Variables["since"] != "2025-01-01T00:00:00Z" || body.Variables["zoneTag"] != "zone" || !strings.Contains(body.Query, "httpRequests1hGroups") || strings.Contains(body.Query, "test-secret-token") {
			t.Fatal("unbounded or incorrect zone analytics query")
		}
		return rawResponse(200, `{"errors":null,"data":{"viewer":{"zones":[{"series":[{"dimensions":{"datetime":"2025-01-01T02:00:00Z"},"sum":{"requests":5,"bytes":100,"cachedRequests":2}},{"dimensions":{"datetime":"2025-01-01T00:00:00Z"},"sum":{"requests":10,"bytes":20,"cachedRequests":3}}]}]}}}`), nil
	})
	result, err := client.Traffic(context.Background(), trafficRequest())
	if err != nil || result.Scope != "zone" || result.Requests != 15 || result.Bytes != 120 || result.CachedRequests != 5 || len(result.Series) != 2 || result.Series[0].Time.Hour() != 0 || result.Since.Minute() != 0 {
		t.Fatalf("incorrect traffic aggregation: %v err=%v", result, err)
	}
}

func TestTrafficUnavailableIsNotSuccessfulZero(t *testing.T) {
	for _, body := range []string{
		`{"errors":[{"message":"permission denied test-secret-token"}],"data":{"viewer":{"zones":[]}}}`,
		`{"data":{"viewer":{"zones":[{"series":null}]}}}`,
		`{"data":{"viewer":{"zones":[]}}}`,
		`{"data":{"viewer":{"zones":[{"series":[{"dimensions":{"datetime":"2025-01-01T00:00:00Z"},"sum":{"requests":1,"bytes":1}}]}]}}}`,
		`{"data":{"viewer":{"zones":[{"series":[{"dimensions":{"datetime":"2025-01-01T00:00:00Z"},"sum":{"requests":1,"bytes":1,"cachedRequests":2}}]}]}}}`,
		`{"data":{"viewer":{"zones":[{"series":[{"dimensions":{"datetime":"2024-12-31T00:00:00Z"},"sum":{"requests":1,"bytes":1,"cachedRequests":0}}]}]}}}`,
	} {
		client := testClient(t, func(*http.Request) (*http.Response, error) { return rawResponse(200, body), nil })
		if _, err := client.Traffic(context.Background(), trafficRequest()); err == nil || strings.Contains(err.Error(), "test-secret-token") {
			t.Fatalf("unavailable or malformed analytics treated as successful zero: %v", err)
		}
	}
	client := testClient(t, func(*http.Request) (*http.Response, error) {
		return rawResponse(200, `{"data":{"viewer":{"zones":[{"series":[]}]}}}`), nil
	})
	if result, err := client.Traffic(context.Background(), trafficRequest()); err != nil || result.Series == nil || result.Requests != 0 {
		t.Fatalf("valid empty dataset not represented as zero: %v", err)
	}
}

func TestTrafficCannotBroadenHostnameScope(t *testing.T) {
	client := testClient(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached provider")
		return nil, nil
	})
	request := trafficRequest()
	request.Hostname = "axv.example.com"
	_, err := client.Traffic(context.Background(), request)
	failureKind(t, err, "unsupported", false)
	request.Hostname = ""
	request.Since = request.Until.Add(-32 * 24 * time.Hour)
	if _, err := client.Traffic(context.Background(), request); err == nil {
		t.Fatal("unbounded analytics time range accepted")
	}
}

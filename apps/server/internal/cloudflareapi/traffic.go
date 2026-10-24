package cloudflareapi

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"time"
)

type TrafficRequest struct {
	ZoneID, Hostname string
	Since, Until     time.Time
}

type TrafficPoint struct {
	Time           time.Time `json:"time"`
	Requests       uint64    `json:"requests"`
	Bytes          uint64    `json:"bytes"`
	CachedRequests uint64    `json:"cachedRequests"`
}

type Traffic struct {
	Since          time.Time      `json:"since"`
	Until          time.Time      `json:"until"`
	Requests       uint64         `json:"requests"`
	Bytes          uint64         `json:"bytes"`
	CachedRequests uint64         `json:"cachedRequests"`
	Series         []TrafficPoint `json:"series"`
	Scope          string         `json:"scope"`
	Dataset        string         `json:"dataset"`
}

const trafficQuery = `query AXVZoneTraffic($zoneTag: string!, $since: Time!, $until: Time!) {
  viewer { zones(filter: {zoneTag: $zoneTag}) {
    series: httpRequests1hGroups(limit: 1000, orderBy: [datetime_ASC],
      filter: {datetime_geq: $since, datetime_lt: $until}) {
      dimensions { datetime }
      sum { requests bytes cachedRequests }
    }
  } }
}`

// Traffic returns zone-wide hourly rollups, not tunnel/hostname statistics or a
// connectivity check. Since is aligned down to the first included hour, so the
// returned bounds describe the actual rollup window. Permission, plan and query
// window failures are explicit errors, never a fabricated successful zero.
func (c *Client) Traffic(ctx context.Context, request TrafficRequest) (Traffic, error) {
	if !validID(request.ZoneID) || request.Since.IsZero() || !request.Until.After(request.Since) ||
		request.Until.Sub(request.Since) > 31*24*time.Hour || request.Until.After(time.Now().Add(5*time.Minute)) {
		return Traffic{}, invalidInput()
	}
	if request.Hostname != "" {
		// The rollup dataset has no hostname filter. Do not silently broaden a
		// caller's requested scope to all other applications in the zone.
		return Traffic{}, &Error{Kind: "unsupported"}
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	since, until := request.Since.UTC().Truncate(time.Hour), request.Until.UTC()
	body := map[string]any{"query": trafficQuery, "variables": map[string]string{
		"zoneTag": request.ZoneID, "since": since.Format(time.RFC3339), "until": until.Format(time.RFC3339),
	}}
	data, status, err := c.exchange(ctx, http.MethodPost, "/graphql", body, false)
	if err != nil {
		return Traffic{}, err
	}
	if status < 200 || status > 299 {
		return Traffic{}, statusError(status, false)
	}
	var response struct {
		Errors []json.RawMessage `json:"errors"`
		Data   *struct {
			Viewer *struct {
				Zones []struct {
					Series []struct {
						Dimensions struct {
							Datetime time.Time `json:"datetime"`
						} `json:"dimensions"`
						Sum *struct {
							Requests, Bytes, CachedRequests *uint64
						} `json:"sum"`
					} `json:"series"`
				} `json:"zones"`
			} `json:"viewer"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &response) != nil {
		return Traffic{}, &Error{Kind: "invalid_response"}
	}
	if len(response.Errors) != 0 {
		return Traffic{}, &Error{Kind: "unsupported"}
	}
	if response.Data == nil || response.Data.Viewer == nil || len(response.Data.Viewer.Zones) != 1 || response.Data.Viewer.Zones[0].Series == nil {
		return Traffic{}, &Error{Kind: "invalid_response"}
	}
	rows := response.Data.Viewer.Zones[0].Series
	if len(rows) >= 1000 {
		return Traffic{}, &Error{Kind: "invalid_response"}
	}
	result := Traffic{Since: since, Until: until, Scope: "zone", Dataset: "httpRequests1hGroups", Series: make([]TrafficPoint, 0, len(rows))}
	seen := make(map[time.Time]bool, len(rows))
	for _, row := range rows {
		stamp := row.Dimensions.Datetime.UTC()
		if stamp.Before(since) || !stamp.Before(until) || stamp != stamp.Truncate(time.Hour) || seen[stamp] || row.Sum == nil ||
			row.Sum.Requests == nil || row.Sum.Bytes == nil || row.Sum.CachedRequests == nil || *row.Sum.CachedRequests > *row.Sum.Requests ||
			math.MaxUint64-result.Requests < *row.Sum.Requests || math.MaxUint64-result.Bytes < *row.Sum.Bytes || math.MaxUint64-result.CachedRequests < *row.Sum.CachedRequests {
			return Traffic{}, &Error{Kind: "invalid_response"}
		}
		seen[stamp] = true
		point := TrafficPoint{Time: stamp, Requests: *row.Sum.Requests, Bytes: *row.Sum.Bytes, CachedRequests: *row.Sum.CachedRequests}
		result.Series = append(result.Series, point)
		result.Requests += point.Requests
		result.Bytes += point.Bytes
		result.CachedRequests += point.CachedRequests
	}
	sort.Slice(result.Series, func(left, right int) bool { return result.Series[left].Time.Before(result.Series[right].Time) })
	return result, nil
}

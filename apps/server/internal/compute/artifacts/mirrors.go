package artifacts

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/artifactcache"
)

// Mirror URLs are exact operator-supplied locations keyed by the original full
// digest. No public mirror is presumed to host Ubuntu, Lima or future AXV releases.
// Selection is mirror-only: failures never silently switch source or release.
func MirrorArtifact(a artifactcache.Artifact, configuration string) (artifactcache.Artifact, error) {
	mirrors, err := parseMirrors(configuration)
	if err != nil {
		return a, err
	}
	return selectMirror(a, mirrors), nil
}

func parseMirrors(configuration string) (map[string]string, error) {
	if configuration == "" {
		return nil, nil
	}
	var mirrors map[string]string
	if len(configuration) > 64<<10 || json.Unmarshal([]byte(configuration), &mirrors) != nil || mirrors == nil {
		return nil, fmt.Errorf("AXV_ARTIFACT_MIRRORS must be a JSON object mapping full SHA-256 to exact HTTPS URL")
	}
	for sum, location := range mirrors {
		probe := artifactcache.Artifact{SHA256: sum, URL: location, MaxBytes: 1}
		u, err := url.Parse(location)
		if probe.Validate() != nil || err != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(location, "\r\n\x00") {
			// Never echo an invalid value: it may contain credentials.
			return nil, fmt.Errorf("AXV_ARTIFACT_MIRRORS requires full lowercase digests and credential-free HTTPS URLs without query or fragment")
		}
	}
	return mirrors, nil
}

func selectMirror(a artifactcache.Artifact, mirrors map[string]string) artifactcache.Artifact {
	if location, ok := mirrors[a.SHA256]; ok {
		if a.OriginURL == "" {
			a.OriginURL = a.URL
		}
		a.URL = location
	}
	return a
}

func WithMirrors(c Catalog, configuration string) (Catalog, error) {
	// Also validate malformed configuration when a catalog is empty.
	mirrors, err := parseMirrors(configuration)
	if err != nil {
		return c, err
	}
	c.Lima, c.Ubuntu, c.AXV = maps.Clone(c.Lima), maps.Clone(c.Ubuntu), maps.Clone(c.AXV)
	for arch, a := range c.Lima {
		c.Lima[arch] = selectMirror(a, mirrors)
	}
	for arch, a := range c.Ubuntu {
		c.Ubuntu[arch] = selectMirror(a, mirrors)
	}
	for key, release := range c.AXV {
		release.Artifact = selectMirror(release.Artifact, mirrors)
		c.AXV[key] = release
	}
	return c, nil
}

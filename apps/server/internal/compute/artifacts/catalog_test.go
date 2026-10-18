package artifacts

import (
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/artifactcache"
	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func TestReviewedLimaArtifactsMatchHostSupport(t *testing.T) {
	// Integrity metadata belongs to this reviewed release. A host support upgrade
	// must review new pins before existing digests can label a different release.
	const reviewedVersion = "2.2.1"
	if compute.SupportedLimaVersion != reviewedVersion || LimaVersion != reviewedVersion {
		t.Fatalf("review Lima artifact pins for host support %s (catalog %s)", compute.SupportedLimaVersion, LimaVersion)
	}
	const releaseURL = "https://github.com/lima-vm/lima/releases/download/v" + reviewedVersion + "/"
	want := map[string]artifactcache.Artifact{
		"arm64": {ID: "lima", Name: "Lima " + reviewedVersion + " for Apple silicon", URL: releaseURL + "lima-" + reviewedVersion + "-Darwin-arm64.tar.gz", SHA256: "9e9eacce88f37e185c346bad73aa6136f738d8cdf8c3bb23cd42b071824bc66e", MaxBytes: 128 << 20, Bytes: 38328082},
		"amd64": {ID: "lima", Name: "Lima " + reviewedVersion + " for Intel Mac", URL: releaseURL + "lima-" + reviewedVersion + "-Darwin-x86_64.tar.gz", SHA256: "6b776e4bc41f358e6ee01fefcc78e2eb26f11635a0fcff632234af77a566c6c4", MaxBytes: 128 << 20, Bytes: 24958659},
	}
	catalog := Official()
	if len(catalog.Lima) != len(want) {
		t.Fatalf("Lima architecture count = %d, want %d reviewed pins", len(catalog.Lima), len(want))
	}
	for arch, artifact := range want {
		t.Run(arch, func(t *testing.T) {
			if got := catalog.Lima[arch]; got != artifact {
				t.Fatalf("Lima artifact differs from reviewed release: got %+v, want %+v", got, artifact)
			}
		})
	}
}

func TestReviewedArtifactsSeparateArchitecturesAndPendingReleases(t *testing.T) {
	c := Official()
	if len(c.AXV) != 0 {
		t.Fatal("update the publication assertion when an official release is actually published")
	}
	for _, set := range []map[string]artifactcache.Artifact{c.Lima, c.Ubuntu} {
		if set["arm64"].SHA256 == set["amd64"].SHA256 {
			t.Fatal("architecture pins alias")
		}
		for _, a := range set {
			if err := a.Validate(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, ok := Lookup("ubuntu-riscv64"); ok {
		t.Fatal("invented unsupported architecture")
	}
}

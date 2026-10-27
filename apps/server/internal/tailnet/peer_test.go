package tailnet

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/tailnet/access"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

func TestPeerIdentityDoesNotRequireSelfOnlyMachineAuthorizationField(t *testing.T) {
	for _, test := range []struct{ name, field string }{
		{"omitted", ""},
		{"false", `,"MachineAuthorized":false`},
		{"true", `,"MachineAuthorized":true`},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := `{"Node":{"User":123,"StableID":"node-client"` + test.field + `},"UserProfile":{"ID":123}}`
			var who apitype.WhoIsResponse
			if err := json.Unmarshal([]byte(data), &who); err != nil {
				t.Fatal(err)
			}
			want := access.Identity{UserID: "123", NodeID: "node-client"}
			if got := peerIdentity(&who); got != want {
				t.Fatalf("valid WhoIs peer rejected for an optional machine field: %+v", got)
			}
		})
	}
}

func TestPeerIdentityRejectsRestrictedEntriesRetainedByWhoIs(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*apitype.WhoIsResponse)
	}{
		{"expired", func(who *apitype.WhoIsResponse) { who.Node.Expired = true }},
		{"jailed", func(who *apitype.WhoIsResponse) { who.Node.IsJailed = true }},
		{"peer API only", func(who *apitype.WhoIsResponse) { who.Node.UnsignedPeerAPIOnly = true }},
		{"missing node", func(who *apitype.WhoIsResponse) { who.Node = nil }},
		{"missing user", func(who *apitype.WhoIsResponse) { who.UserProfile = nil }},
		{"invalid user", func(who *apitype.WhoIsResponse) { who.Node.User = 0; who.UserProfile.ID = 0 }},
		{"inconsistent user", func(who *apitype.WhoIsResponse) { who.UserProfile.ID = 456 }},
		{"missing stable node", func(who *apitype.WhoIsResponse) { who.Node.StableID = "" }},
		{"invalid stable node", func(who *apitype.WhoIsResponse) { who.Node.StableID = "0" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			who := &apitype.WhoIsResponse{Node: &tailcfg.Node{User: 123, StableID: "node-client"}, UserProfile: &tailcfg.UserProfile{ID: 123}}
			test.change(who)
			if got := peerIdentity(who); got != (access.Identity{}) {
				t.Fatalf("restricted WhoIs entry authorized a peer: %+v", got)
			}
		})
	}
	if got := peerIdentity(nil); got != (access.Identity{}) {
		t.Fatalf("missing WhoIs entry authorized a peer: %+v", got)
	}
}

func TestPeerIdentityPreservesProviderExpiryAndTagSemantics(t *testing.T) {
	for _, expiry := range []time.Time{{}, time.Now().Add(time.Hour), time.Now().Add(-time.Hour)} {
		who := &apitype.WhoIsResponse{Node: &tailcfg.Node{User: 123, StableID: "node-client", MachineAuthorized: true, KeyExpiry: expiry}, UserProfile: &tailcfg.UserProfile{ID: 123}}
		want := access.Identity{UserID: "123", NodeID: "node-client"}
		if got := peerIdentity(who); got != want {
			t.Fatalf("valid peer rejected despite authoritative unexpired state: %+v", got)
		}
		who.Node.Tags = []string{"tag:client"}
		want.Tagged = true
		if got := peerIdentity(who); got != want {
			t.Fatalf("tagged peer lost its authorization restriction: %+v", got)
		}
	}
}

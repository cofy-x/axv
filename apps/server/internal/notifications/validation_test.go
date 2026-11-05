package notifications

import (
	"strings"
	"testing"
)

func TestSchemaKeysCannotAliasThroughUnicodeCaseFolding(t *testing.T) {
	for _, data := range []string{
		"{\"storeId\":\"original\",\"ſtoreId\":\"replacement\"}",
		"{\"storeId\":\"original\",\"\\u017ftoreId\":\"replacement\"}",
		"{\"ſtoreId\":\"replacement\"}",
	} {
		var input Update
		if uniqueJSON([]byte(data)) || decodeStrict(strings.NewReader(data), &input) == nil {
			t.Fatal("ambiguous non-ASCII schema key was accepted")
		}
	}
	var value struct {
		Token string `json:"token"`
	}
	if decodeStrict(strings.NewReader("{\"token\":\"α\"}"), &value) != nil || value.Token != "α" {
		t.Fatal("valid Unicode field content was changed")
	}
}

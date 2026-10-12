package session

import (
	"encoding/json"
	"log"
	"os"
)

const unknownAccount = "unknown"

// ReadAccountID returns lastKnownAccountUuid from the Claude app config at
// path. It returns "unknown", and warns, when the file is absent, unparsable
// or lacks the key.
func ReadAccountID(path string) string {
	var data struct {
		LastKnownAccountUUID *string `json:"lastKnownAccountUuid"`
	}
	raw, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(raw, &data)
	}
	if err != nil || data.LastKnownAccountUUID == nil || *data.LastKnownAccountUUID == unknownAccount {
		log.Println("clued: account ID unavailable — isolation is degraded")
		return unknownAccount
	}
	return *data.LastKnownAccountUUID
}

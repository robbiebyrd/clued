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
	return readAccountID(path, true)
}

// AccountIDFor is ReadAccountID for cfg's Claude app config, but it warns
// only in local mode: in remote mode the server stamps every write with the
// token's account, so a missing local id does not degrade isolation.
func AccountIDFor(cfg Config) string {
	return readAccountID(cfg.ClaudeAppConfigPath, !cfg.Remote())
}

func readAccountID(path string, warn bool) string {
	var data struct {
		LastKnownAccountUUID *string `json:"lastKnownAccountUuid"`
	}
	raw, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(raw, &data)
	}
	if err != nil || data.LastKnownAccountUUID == nil || *data.LastKnownAccountUUID == unknownAccount {
		if warn {
			log.Println("clued: account ID unavailable — isolation is degraded")
		}
		return unknownAccount
	}
	return *data.LastKnownAccountUUID
}

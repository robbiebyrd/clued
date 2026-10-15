package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/wal"
)

// daemonPostTimeout bounds one post to the daemon.
const daemonPostTimeout = 2 * time.Second

// postToDaemon delivers one event document to the local daemon's /event,
// falling back to the write-ahead log the daemon replays when it is not
// reachable (the path the hooks use). It returns an error only when
// neither accepted it.
func postToDaemon(ctx context.Context, port int, walPath string, doc session.Doc) error {
	err := sendToDaemon(ctx, port, doc)
	if err == nil {
		return nil
	}
	if walPath == "" {
		return err
	}
	if werr := wal.Append(walPath, doc); werr != nil {
		return fmt.Errorf("daemon: %v; wal: %w", err, werr)
	}
	return nil
}

func sendToDaemon(ctx context.Context, port int, doc session.Doc) error {
	if port <= 0 {
		return fmt.Errorf("no daemon port configured")
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, daemonPostTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(port)+"/event", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("daemon answered %s", resp.Status)
	}
	return nil
}

package session

import "time"

// HostInfo describes the machine a session ran on. IP and MAC are null when unknown.
type HostInfo struct {
	Hostname  string  `json:"hostname" bson:"hostname"`
	IP        *string `json:"ip" bson:"ip"`
	MAC       *string `json:"mac" bson:"mac"`
	Username  string  `json:"username" bson:"username"`
	UID       int     `json:"uid" bson:"uid"`
	Platform  string  `json:"platform" bson:"platform"`
	Arch      string  `json:"arch" bson:"arch"`
	OSRelease string  `json:"os_release" bson:"os_release"`
	OSType    string  `json:"os_type" bson:"os_type"`
}

// Session is the metadata of one Claude Code session. Empty strings are not written.
type Session struct {
	SessionID      string    `json:"session_id" bson:"session_id"`
	TranscriptPath string    `json:"transcript_path,omitempty" bson:"transcript_path,omitempty"`
	Cwd            string    `json:"cwd,omitempty" bson:"cwd,omitempty"`
	ProjectPath    string    `json:"project_path,omitempty" bson:"project_path,omitempty"`
	GitOrigin      string    `json:"git_origin,omitempty" bson:"git_origin,omitempty"`
	GitBranch      string    `json:"git_branch,omitempty" bson:"git_branch,omitempty"`
	AccountID      string    `json:"account_id,omitempty" bson:"account_id,omitempty"`
	Host           *HostInfo `json:"host,omitempty" bson:"host,omitempty"`
	LastSeen       time.Time `json:"last_seen" bson:"last_seen"`
}

// TranscriptLine is one line of a session transcript, keyed by session and sequence number.
type TranscriptLine struct {
	SessionID string    `json:"session_id" bson:"session_id"`
	Seq       int       `json:"seq" bson:"seq"`
	Line      any       `json:"line" bson:"line"`
	AccountID string    `json:"account_id,omitempty" bson:"account_id,omitempty"`
	Host      *HostInfo `json:"host,omitempty" bson:"host,omitempty"`
}

// SubagentLine is one line of a subagent transcript.
type SubagentLine struct {
	SessionID  string    `json:"session_id" bson:"session_id"`
	SubagentID string    `json:"subagent_id" bson:"subagent_id"`
	Seq        int       `json:"seq" bson:"seq"`
	Line       any       `json:"line" bson:"line"`
	AccountID  string    `json:"account_id,omitempty" bson:"account_id,omitempty"`
	Host       *HostInfo `json:"host,omitempty" bson:"host,omitempty"`
}

// Blob is a stored side file of a session. BlobType is subagent-meta,
// tool-result or file-history; Encoding is utf8 or base64.
type Blob struct {
	SessionID string `json:"session_id" bson:"session_id"`
	BlobType  string `json:"blob_type" bson:"blob_type"`
	Name      string `json:"name" bson:"name"`
	Content   string `json:"content" bson:"content"`
	Encoding  string `json:"encoding" bson:"encoding"`
	AccountID string `json:"account_id,omitempty" bson:"account_id,omitempty"`
}

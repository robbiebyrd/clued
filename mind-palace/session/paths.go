package session

import "strings"

// DecodeProjectPath turns a Claude projects directory name ("-Users-x-y")
// into a path ("/Users/x/y"). Hyphens in the original path are
// indistinguishable from separators, so the result is best-effort metadata.
func DecodeProjectPath(dirName string) string {
	if dirName == "" {
		return "/"
	}
	return "/" + strings.ReplaceAll(dirName[1:], "-", "/")
}

// EncodeProjectPath is the inverse of DecodeProjectPath: "/Users/x/y"
// becomes "-Users-x-y".
func EncodeProjectPath(path string) string {
	return "-" + strings.ReplaceAll(strings.TrimPrefix(path, "/"), "/", "-")
}

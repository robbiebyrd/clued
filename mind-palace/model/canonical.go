package model

import (
	"regexp"
	"strings"
)

// TemplateIDRe matches a template id: one path segment of letters, digits,
// dots, underscores and hyphens, never "." or "..".
var TemplateIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`)

const (
	idAlphabet         = "abcdefghijklmnopqrstuvwxyz0123456789-"
	templateIDAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-"
)

// CanonicalID reports whether id has the document id shape (IDRe) and, when
// it does, returns a copy rebuilt character by character from the id
// alphabet. Stores use the copy wherever an id becomes a file name or a
// query filter, so the value at that point can only hold those characters
// whatever the request carried.
func CanonicalID(id string) (string, bool) {
	if !IDRe.MatchString(id) {
		return "", false
	}
	return rebuild(id, idAlphabet)
}

// CanonicalTemplateID is CanonicalID for template ids (TemplateIDRe).
func CanonicalTemplateID(id string) (string, bool) {
	if !TemplateIDRe.MatchString(id) {
		return "", false
	}
	return rebuild(id, templateIDAlphabet)
}

// rebuild returns s spelled from alphabet, or false when s holds any other
// byte.
func rebuild(s, alphabet string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		j := strings.IndexByte(alphabet, s[i])
		if j < 0 {
			return "", false
		}
		b.WriteByte(alphabet[j])
	}
	return b.String(), true
}

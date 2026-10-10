package enrichers

import (
	"context"
	"unicode/utf16"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// AttachmentExtraction is the stored result of the attachment-extractor enricher.
type AttachmentExtraction struct {
	FilePath  *string `json:"file_path" bson:"file_path"`
	Language  *string `json:"language" bson:"language"`
	SizeChars *int    `json:"size_chars" bson:"size_chars"`
}

// AttachmentExtractor records the file path, language and size of the file an
// attachment line carries.
var AttachmentExtractor = session.Enricher{
	Name:       "attachment-extractor",
	Collection: "transcript_lines",
	Enabled:    true,
	Matches: func(doc session.Doc) bool {
		lineType, _ := doc.Line().String("type")
		return lineType == "attachment"
	},
	Enrich: func(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
		att := doc.Attachment()
		result := AttachmentExtraction{}
		if filePath, ok := att.String("file_path"); ok {
			result.FilePath = &filePath
			result.Language = languageOrNil(filePath)
		}
		if content, ok := att.String("content"); ok {
			// JavaScript measures string length in UTF-16 code units.
			size := len(utf16.Encode([]rune(content)))
			result.SizeChars = &size
		}
		return result, nil
	},
}

// languageOrNil is the language for a path, or nil when its extension is unknown.
func languageOrNil(path string) *string {
	lang := session.ExtToLang(path)
	if lang == "" {
		return nil
	}
	return &lang
}

func init() { session.Register(AttachmentExtractor) }

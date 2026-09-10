// Package artifact defines the bounded document formats supported by the
// conversation product. It never evaluates scripts, HTML or spreadsheet code.
package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

const MaxBytes = 1024 * 1024
const InlineBytes = 32 * 1024

var columnKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
var decimal = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)

func failure(class, code string) error {
	return &agentsdk.Error{Class: class, Code: "agent.conversation." + code}
}
func text(value string, max int) bool {
	return len(value) <= max && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}
func ValidTitle(value string) bool {
	return text(value, 128) && strings.TrimSpace(value) != "" && !strings.ContainsAny(value, "\r\n")
}
func Hash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func Validate(content agentsdk.ConversationArtifactContent) error {
	invalid := func() error { return failure("bad_request", "artifact_content_invalid") }
	if content.Kind == "markdown" {
		if !text(content.Markdown, MaxBytes) || content.Table != nil || content.Chart != nil {
			return invalid()
		}
		return nil
	}
	if content.Kind != "table" && content.Kind != "chart" || content.Markdown != "" || content.Table == nil || content.Kind == "table" && content.Chart != nil {
		return invalid()
	}
	table := content.Table
	if len(table.Columns) == 0 || len(table.Columns) > 64 || len(table.Rows) > 10000 {
		return invalid()
	}
	columns := map[string]string{}
	for _, column := range table.Columns {
		if !columnKey.MatchString(column.Key) || columns[column.Key] != "" || !text(column.Label, 128) || strings.TrimSpace(column.Label) == "" || column.Type != "text" && column.Type != "number" && column.Type != "date" {
			return invalid()
		}
		columns[column.Key] = column.Type
	}
	for _, row := range table.Rows {
		if len(row) != len(table.Columns) {
			return invalid()
		}
		for index, value := range row {
			if value == nil {
				continue
			}
			if !text(*value, 16384) {
				return invalid()
			}
			switch table.Columns[index].Type {
			case "number":
				if len(*value) > 128 || !decimal.MatchString(*value) {
					return invalid()
				}
			case "date":
				date, err := time.Parse("2006-01-02", *value)
				if err != nil || date.Format("2006-01-02") != *value {
					return invalid()
				}
			}
		}
	}
	if content.Kind == "chart" {
		chart := content.Chart
		if chart == nil || chart.Type != "bar" && chart.Type != "line" || columns[chart.XColumn] == "" || len(chart.YColumns) < 1 || len(chart.YColumns) > 8 || len(table.Rows) > 500 {
			return invalid()
		}
		seen := map[string]bool{}
		for _, key := range chart.YColumns {
			if columns[key] != "number" || seen[key] || key == chart.XColumn {
				return invalid()
			}
			seen[key] = true
		}
	}
	return nil
}

func Encode(content agentsdk.ConversationArtifactContent) ([]byte, string, error) {
	if err := Validate(content); err != nil {
		return nil, "", err
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return nil, "", err
	}
	if len(raw) > MaxBytes {
		return nil, "", failure("bad_request", "artifact_too_large")
	}
	return raw, Hash(raw), nil
}
func Decode(raw []byte) (agentsdk.ConversationArtifactContent, error) {
	var content agentsdk.ConversationArtifactContent
	if len(raw) == 0 || len(raw) > MaxBytes {
		return content, failure("bad_request", "artifact_too_large")
	}
	if !utf8.Valid(raw) {
		return content, failure("bad_request", "artifact_content_invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&content); err != nil {
		return content, failure("bad_request", "artifact_content_invalid")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return content, failure("bad_request", "artifact_content_invalid")
	}
	return content, Validate(content)
}

// Text replacements must each match exactly once, in order. Ambiguous text
// requires reading the version and choosing a more specific section. This
// function copies the old content so a rejected patch cannot mutate it.
func Edit(original agentsdk.ConversationArtifactContent, patch agentsdk.ConversationArtifactPatch) (agentsdk.ConversationArtifactContent, error) {
	invalid := func() (agentsdk.ConversationArtifactContent, error) {
		return agentsdk.ConversationArtifactContent{}, failure("bad_request", "artifact_patch_invalid")
	}
	if patch.Title != nil && !ValidTitle(*patch.Title) || len(patch.Text) > 32 || len(patch.Cells) > 256 {
		return invalid()
	}
	modes := 0
	if patch.Content != nil {
		modes++
	}
	if len(patch.Text) > 0 {
		modes++
	}
	if len(patch.Cells) > 0 {
		modes++
	}
	if modes > 1 || modes == 0 && patch.Title == nil {
		return invalid()
	}
	copyFrom := original
	if patch.Content != nil {
		copyFrom = *patch.Content
	}
	raw, _, err := Encode(copyFrom)
	if err != nil {
		return agentsdk.ConversationArtifactContent{}, err
	}
	content, err := Decode(raw)
	if err != nil {
		return content, err
	}
	if len(patch.Text) > 0 && content.Kind != "markdown" || len(patch.Cells) > 0 && content.Table == nil {
		return invalid()
	}
	for _, edit := range patch.Text {
		if edit.Find == "" || !text(edit.Find, MaxBytes) || !text(edit.Replace, MaxBytes) {
			return invalid()
		}
		if strings.Count(content.Markdown, edit.Find) != 1 {
			return content, failure("conflict", "artifact_text_ambiguous")
		}
		content.Markdown = strings.Replace(content.Markdown, edit.Find, edit.Replace, 1)
	}
	seen := map[struct {
		row    int
		column string
	}]bool{}
	for _, edit := range patch.Cells {
		key := struct {
			row    int
			column string
		}{edit.Row, edit.Column}
		if seen[key] || edit.Row < 0 || edit.Row >= len(content.Table.Rows) {
			return invalid()
		}
		seen[key] = true
		column := -1
		for index, candidate := range content.Table.Columns {
			if candidate.Key == edit.Column {
				column = index
			}
		}
		if column < 0 {
			return invalid()
		}
		if edit.Value == nil {
			content.Table.Rows[edit.Row][column] = nil
		} else {
			value := *edit.Value
			content.Table.Rows[edit.Row][column] = &value
		}
	}
	_, _, err = Encode(content)
	return content, err
}

type Exported struct {
	Data                   []byte
	ContentType, Extension string
	FormulaGuarded         bool
}

func Export(content agentsdk.ConversationArtifactContent, format string) (Exported, error) {
	if _, _, err := Encode(content); err != nil {
		return Exported{}, err
	}
	if format == "markdown" && content.Kind == "markdown" {
		return Exported{Data: []byte(content.Markdown), ContentType: "text/markdown; charset=utf-8", Extension: ".md"}, nil
	}
	if format != "csv" || content.Table == nil {
		return Exported{}, failure("bad_request", "artifact_export_format_invalid")
	}
	out := Exported{ContentType: "text/csv; charset=utf-8", Extension: ".csv"}
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	writer.UseCRLF = true
	guard := func(value string, numeric bool) string {
		trimmed := strings.TrimLeftFunc(value, unicode.IsSpace)
		if !numeric && (len(value) > 0 && strings.ContainsRune("\t\r\n", rune(value[0])) || len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0]))) {
			out.FormulaGuarded = true
			return "'" + value
		}
		return value
	}
	header := make([]string, len(content.Table.Columns))
	for index, column := range content.Table.Columns {
		header[index] = guard(column.Label, false)
	}
	if err := writer.Write(header); err != nil {
		return out, err
	}
	for _, row := range content.Table.Rows {
		values := make([]string, len(row))
		for index, value := range row {
			if value != nil {
				values[index] = guard(*value, content.Table.Columns[index].Type == "number")
			}
		}
		if err := writer.Write(values); err != nil {
			return out, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return out, err
	}
	out.Data = buffer.Bytes()
	return out, nil
}

// Package schema is the content type registry: the runtime-editable
// definition of what fields a collection or single type has, and the
// validation rules entries are checked against before every write.
//
// This is a deliberately small subset of the full field-type catalogue
// (see docs/ROADMAP.md) covering the types needed for the first vertical
// slice plus relations: short_text, long_text, rich_text, integer,
// decimal, boolean, email, url, date, datetime, enumeration, json,
// relation. Components, dynamic zones and media fields are tracked as
// NOT_STARTED in PROGRESS.md.
package schema

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"time"

	"github.com/google/uuid"
)

type FieldType string

const (
	FieldShortText   FieldType = "short_text"
	FieldLongText    FieldType = "long_text"
	FieldRichText    FieldType = "rich_text"
	FieldInteger     FieldType = "integer"
	FieldDecimal     FieldType = "decimal"
	FieldBoolean     FieldType = "boolean"
	FieldEmail       FieldType = "email"
	FieldURL         FieldType = "url"
	FieldDate        FieldType = "date"
	FieldDateTime    FieldType = "datetime"
	FieldEnumeration FieldType = "enumeration"
	FieldJSON        FieldType = "json"
	FieldRelation    FieldType = "relation"
)

var validFieldTypes = map[FieldType]bool{
	FieldShortText: true, FieldLongText: true, FieldRichText: true,
	FieldInteger: true, FieldDecimal: true, FieldBoolean: true,
	FieldEmail: true, FieldURL: true, FieldDate: true, FieldDateTime: true,
	FieldEnumeration: true, FieldJSON: true, FieldRelation: true,
}

type RelationType string

const (
	RelationOneToOne   RelationType = "one_to_one"
	RelationOneToMany  RelationType = "one_to_many"
	RelationManyToOne  RelationType = "many_to_one"
	RelationManyToMany RelationType = "many_to_many"
)

var validRelationTypes = map[RelationType]bool{
	RelationOneToOne: true, RelationOneToMany: true,
	RelationManyToOne: true, RelationManyToMany: true,
}

// RelationConfig describes what a "relation" field points at. The
// target is always a content type API id within the *same* project —
// cross-project references are rejected (internal/content enforces
// this; schema.Validate can't, since it has no database access).
type RelationConfig struct {
	TargetContentType string       `json:"targetContentType"`
	RelationType      RelationType `json:"relationType"`
}

// IsToMany reports whether a relation field's value is a list (true for
// one_to_many/many_to_many) or a single related document (false).
func (rc RelationConfig) IsToMany() bool {
	return rc.RelationType == RelationOneToMany || rc.RelationType == RelationManyToMany
}

// reservedFieldNames collide with infrastructure columns/metadata and
// can never be used as a content field's API name.
var reservedFieldNames = map[string]bool{
	"id": true, "documentId": true, "locale": true, "status": true,
	"version": true, "createdAt": true, "updatedAt": true, "createdBy": true,
}

var apiNamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)

type Field struct {
	Name        string    `json:"name"`
	Type        FieldType `json:"type"`
	Required    bool      `json:"required"`
	Unique      bool      `json:"unique"`
	Default     any       `json:"default,omitempty"`
	MinLength   *int      `json:"minLength,omitempty"`
	MaxLength   *int      `json:"maxLength,omitempty"`
	Min         *float64  `json:"min,omitempty"`
	Max         *float64  `json:"max,omitempty"`
	Pattern     string    `json:"pattern,omitempty"`
	Enum        []string  `json:"enum,omitempty"`
	Private     bool      `json:"private"`
	HelpText    string    `json:"helpText,omitempty"`
	Placeholder string    `json:"placeholder,omitempty"`
	DisplayName string    `json:"displayName,omitempty"`

	// Relation is required when Type == FieldRelation, and ignored
	// otherwise.
	Relation *RelationConfig `json:"relation,omitempty"`
}

type Definition struct {
	Fields []Field `json:"fields"`
}

// Validate checks the schema definition itself for structural problems:
// duplicate names, reserved names, invalid types, invalid enum config.
// It does not look at any content.
func (d Definition) Validate() error {
	seen := map[string]bool{}
	for _, f := range d.Fields {
		if !apiNamePattern.MatchString(f.Name) {
			return fmt.Errorf("field %q: name must start with a letter and contain only letters, digits, underscores", f.Name)
		}
		if reservedFieldNames[f.Name] {
			return fmt.Errorf("field %q: name is reserved", f.Name)
		}
		if seen[f.Name] {
			return fmt.Errorf("field %q: duplicate field name", f.Name)
		}
		seen[f.Name] = true

		if !validFieldTypes[f.Type] {
			return fmt.Errorf("field %q: unknown field type %q", f.Name, f.Type)
		}
		if f.Type == FieldEnumeration && len(f.Enum) == 0 {
			return fmt.Errorf("field %q: enumeration fields require at least one value", f.Name)
		}
		if f.Type == FieldRelation {
			if f.Relation == nil || f.Relation.TargetContentType == "" {
				return fmt.Errorf("field %q: relation fields require a targetContentType", f.Name)
			}
			if !validRelationTypes[f.Relation.RelationType] {
				return fmt.Errorf("field %q: unknown relationType %q", f.Name, f.Relation.RelationType)
			}
		}
	}
	return nil
}

// ValidateEntry checks a content payload against the schema. It returns
// an error on the first problem: unexpected fields are rejected outright
// rather than silently dropped.
func (d Definition) ValidateEntry(data map[string]any) error {
	allowed := map[string]Field{}
	for _, f := range d.Fields {
		allowed[f.Name] = f
	}
	for key := range data {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("unexpected field %q", key)
		}
	}

	for _, f := range d.Fields {
		value, present := data[f.Name]
		if !present || value == nil {
			if f.Required {
				return fmt.Errorf("field %q is required", f.Name)
			}
			continue
		}
		if err := validateField(f, value); err != nil {
			return fmt.Errorf("field %q: %w", f.Name, err)
		}
	}
	return nil
}

func validateField(f Field, value any) error {
	switch f.Type {
	case FieldShortText, FieldLongText, FieldRichText:
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("expected a string")
		}
		if f.MinLength != nil && len(s) < *f.MinLength {
			return fmt.Errorf("must be at least %d characters", *f.MinLength)
		}
		if f.MaxLength != nil && len(s) > *f.MaxLength {
			return fmt.Errorf("must be at most %d characters", *f.MaxLength)
		}
		if f.Pattern != "" {
			re, err := regexp.Compile(f.Pattern)
			if err != nil {
				return fmt.Errorf("invalid pattern configured on field")
			}
			if !re.MatchString(s) {
				return fmt.Errorf("does not match required pattern")
			}
		}
	case FieldInteger:
		n, ok := asFloat(value)
		if !ok || n != float64(int64(n)) {
			return fmt.Errorf("expected an integer")
		}
		return checkRange(f, n)
	case FieldDecimal:
		n, ok := asFloat(value)
		if !ok {
			return fmt.Errorf("expected a number")
		}
		return checkRange(f, n)
	case FieldBoolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected a boolean")
		}
	case FieldEmail:
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("expected a string")
		}
		if _, err := mail.ParseAddress(s); err != nil {
			return fmt.Errorf("not a valid email address")
		}
	case FieldURL:
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("expected a string")
		}
		u, err := url.ParseRequestURI(s)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("not a valid URL")
		}
	case FieldDate:
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("expected a date string")
		}
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return fmt.Errorf("expected date format YYYY-MM-DD")
		}
	case FieldDateTime:
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("expected a datetime string")
		}
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			return fmt.Errorf("expected RFC3339 datetime")
		}
	case FieldEnumeration:
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("expected a string")
		}
		found := false
		for _, e := range f.Enum {
			if e == s {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("value %q is not one of the allowed options", s)
		}
	case FieldJSON:
		// Any JSON-decodable value is acceptable; nothing further to check.
		if _, err := json.Marshal(value); err != nil {
			return fmt.Errorf("not valid JSON")
		}
	case FieldRelation:
		return validateRelationValue(f, value)
	}
	return nil
}

// validateRelationValue only checks shape (a document id, or a list of
// them, matching the field's cardinality) — whether those documents
// actually exist, belong to the same project, and match the target
// content type is checked by internal/content, which has the database
// access schema.Definition deliberately doesn't.
func validateRelationValue(f Field, value any) error {
	if f.Relation == nil {
		return fmt.Errorf("field has no relation configuration")
	}
	if f.Relation.IsToMany() {
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("expected a list of document ids")
		}
		for _, item := range items {
			if err := validateDocumentIDString(item); err != nil {
				return err
			}
		}
		return nil
	}
	return validateDocumentIDString(value)
}

func validateDocumentIDString(value any) error {
	s, ok := value.(string)
	if !ok {
		return fmt.Errorf("expected a document id string")
	}
	if _, err := uuid.Parse(s); err != nil {
		return fmt.Errorf("%q is not a valid document id", s)
	}
	return nil
}

// RelationFields returns the fields of this definition that are
// relations, for callers (internal/content) that need to handle them
// separately from ordinary JSONB-stored fields.
func (d Definition) RelationFields() []Field {
	var out []Field
	for _, f := range d.Fields {
		if f.Type == FieldRelation {
			out = append(out, f)
		}
	}
	return out
}

func checkRange(f Field, n float64) error {
	if f.Min != nil && n < *f.Min {
		return fmt.Errorf("must be >= %v", *f.Min)
	}
	if f.Max != nil && n > *f.Max {
		return fmt.Errorf("must be <= %v", *f.Max)
	}
	return nil
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

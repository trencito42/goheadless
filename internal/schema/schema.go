// Package schema is the content type registry: the runtime-editable
// definition of what fields a collection or single type has, and the
// validation rules entries are checked against before every write.
//
// This is a deliberately small subset of the full field-type catalogue
// (see docs/ROADMAP.md): short_text, long_text, rich_text, integer,
// decimal, boolean, email, url, date, datetime, enumeration, json,
// relation, component, dynamic_zone. Media fields are tracked as
// NOT_STARTED in PROGRESS.md.
//
// Components and dynamic zones are nested field groups, stored inline
// in the owning entry's JSONB data (no separate table — see
// docs/adr/0005-components.md) and so, deliberately, may not contain
// relation fields in this first pass: a relation needs a from_entry_id
// to key entry_relations by, which a value nested inside JSONB doesn't
// have one of. That restriction is enforced by Definition.Validate and
// documented as a known gap in PROGRESS.md, not silently dropped.
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
	FieldComponent   FieldType = "component"
	FieldDynamicZone FieldType = "dynamic_zone"
)

var validFieldTypes = map[FieldType]bool{
	FieldShortText: true, FieldLongText: true, FieldRichText: true,
	FieldInteger: true, FieldDecimal: true, FieldBoolean: true,
	FieldEmail: true, FieldURL: true, FieldDate: true, FieldDateTime: true,
	FieldEnumeration: true, FieldJSON: true, FieldRelation: true,
	FieldComponent: true, FieldDynamicZone: true,
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

// DeletePolicy governs what happens to a relation field's value when
// its target document is deleted (internal/content.Service.DeleteDocument).
type DeletePolicy string

const (
	// DeleteRestrict rejects deleting a document that is still
	// referenced through this field — the safest default, and what an
	// unset OnDelete means.
	DeleteRestrict DeletePolicy = "restrict"
	// DeleteSetNull lets the deletion proceed and clears the relation
	// (the referencing document's value for this field becomes
	// null/empty; the referencing document itself is untouched).
	DeleteSetNull DeletePolicy = "set_null"
	// DeleteCascade lets the deletion proceed and also deletes every
	// document that references the target through this field.
	DeleteCascade DeletePolicy = "cascade"
)

var validDeletePolicies = map[DeletePolicy]bool{
	DeleteRestrict: true, DeleteSetNull: true, DeleteCascade: true,
}

// RelationConfig describes what a "relation" field points at. The
// target is always a content type API id within the *same* project —
// cross-project references are rejected (internal/content enforces
// this; schema.Validate can't, since it has no database access).
type RelationConfig struct {
	TargetContentType string       `json:"targetContentType"`
	RelationType      RelationType `json:"relationType"`
	// OnDelete governs what happens to this field's relation when its
	// target document is deleted. Empty means DeleteRestrict.
	OnDelete DeletePolicy `json:"onDelete,omitempty"`
}

// DeletePolicyOrDefault returns OnDelete, or DeleteRestrict if unset.
func (rc RelationConfig) DeletePolicyOrDefault() DeletePolicy {
	if rc.OnDelete == "" {
		return DeleteRestrict
	}
	return rc.OnDelete
}

// IsToMany reports whether a relation field's value is a list (true for
// one_to_many/many_to_many) or a single related document (false).
func (rc RelationConfig) IsToMany() bool {
	return rc.RelationType == RelationOneToMany || rc.RelationType == RelationManyToMany
}

// ComponentConfig describes a "component" field's nested shape: its own
// set of fields, validated the same way a top-level Definition is.
// Repeatable means the field's value is a list of component instances
// rather than a single one.
//
// Components may not contain relation, component, or dynamic_zone
// fields themselves: relations need a from_entry_id to key
// entry_relations by, which a value nested inside JSONB never has one
// of (see the package doc comment); nesting components/dynamic zones
// inside components is deferred rather than designed, to keep the
// first pass's recursion bounded to one level.
type ComponentConfig struct {
	Fields     []Field `json:"fields"`
	Repeatable bool    `json:"repeatable"`
}

// DynamicZoneConfig describes a "dynamic_zone" field: an ordered list
// of component instances, each tagged with which named component type
// it is (Components keyed by that name). A dynamic zone is always a
// list, never a single instance.
type DynamicZoneConfig struct {
	Components map[string]ComponentConfig `json:"components"`
}

// dynamicZoneComponentKey is the JSON key a dynamic zone's component
// instances are tagged with to say which component in Components they
// are an instance of.
const dynamicZoneComponentKey = "__component"

// reservedFieldNames collide with infrastructure columns/metadata and
// can never be used as a content field's API name.
var reservedFieldNames = map[string]bool{
	"id": true, "documentId": true, "locale": true, "status": true,
	"version": true, "createdAt": true, "updatedAt": true, "createdBy": true,
}

var apiNamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)

type Field struct {
	// ID is the field's stable identity: assigned once when the field
	// is first created (Registry.Create fills in a zero ID) and never
	// changed afterward, even if Name is later renamed (once schema
	// migrations support that). internal/content keys stored relations
	// by this, not by Name, so a rename can't silently orphan them —
	// see docs/adr/0003-document-identity.md.
	ID          uuid.UUID `json:"id"`
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

	// Component is required when Type == FieldComponent, and ignored
	// otherwise.
	Component *ComponentConfig `json:"component,omitempty"`

	// DynamicZone is required when Type == FieldDynamicZone, and
	// ignored otherwise.
	DynamicZone *DynamicZoneConfig `json:"dynamicZone,omitempty"`
}

type Definition struct {
	Fields []Field `json:"fields"`
}

// Validate checks the schema definition itself for structural problems:
// duplicate names, reserved names, invalid types, invalid enum config.
// It does not look at any content.
func (d Definition) Validate() error {
	seenIDs := map[uuid.UUID]bool{}
	return validateFieldList(d.Fields, seenIDs)
}

// validateFieldList validates one set of fields — either a top-level
// Definition's own Fields, or the nested Fields of a component/dynamic
// zone's component. Field *names* only need to be unique within the
// list being validated (a component's fields live in their own nested
// JSON object, so they can't collide with the parent's); field *ids*
// are tracked in the one seenIDs map passed all the way down, so an id
// collision is caught no matter how deep the two colliding fields are
// nested relative to each other.
func validateFieldList(fields []Field, seenIDs map[uuid.UUID]bool) error {
	seenNames := map[string]bool{}
	for _, f := range fields {
		if !apiNamePattern.MatchString(f.Name) {
			return fmt.Errorf("field %q: name must start with a letter and contain only letters, digits, underscores", f.Name)
		}
		if reservedFieldNames[f.Name] {
			return fmt.Errorf("field %q: name is reserved", f.Name)
		}
		if seenNames[f.Name] {
			return fmt.Errorf("field %q: duplicate field name", f.Name)
		}
		seenNames[f.Name] = true

		// A client may supply a field id explicitly (e.g. re-submitting
		// a previously assigned one); what it may never do is supply
		// the same id for two different fields — that would make
		// stored-relation lookups (keyed by field id) ambiguous between
		// them. A zero id is fine: Registry.Create assigns a fresh one.
		if f.ID != uuid.Nil {
			if seenIDs[f.ID] {
				return fmt.Errorf("field %q: duplicate field id %s", f.Name, f.ID)
			}
			seenIDs[f.ID] = true
		}

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
			if f.Relation.OnDelete != "" && !validDeletePolicies[f.Relation.OnDelete] {
				return fmt.Errorf("field %q: unknown onDelete policy %q", f.Name, f.Relation.OnDelete)
			}
		}
		if f.Type == FieldComponent {
			if f.Component == nil {
				return fmt.Errorf("field %q: component fields require a component configuration", f.Name)
			}
			if err := rejectNestedContainers(f.Component.Fields); err != nil {
				return fmt.Errorf("field %q: %w", f.Name, err)
			}
			if err := validateFieldList(f.Component.Fields, seenIDs); err != nil {
				return fmt.Errorf("field %q: %w", f.Name, err)
			}
		}
		if f.Type == FieldDynamicZone {
			if f.DynamicZone == nil || len(f.DynamicZone.Components) == 0 {
				return fmt.Errorf("field %q: dynamic_zone fields require at least one named component", f.Name)
			}
			for compName, cc := range f.DynamicZone.Components {
				if !apiNamePattern.MatchString(compName) {
					return fmt.Errorf("field %q: component name %q must start with a letter and contain only letters, digits, underscores", f.Name, compName)
				}
				if err := rejectNestedContainers(cc.Fields); err != nil {
					return fmt.Errorf("field %q: component %q: %w", f.Name, compName, err)
				}
				if err := validateFieldList(cc.Fields, seenIDs); err != nil {
					return fmt.Errorf("field %q: component %q: %w", f.Name, compName, err)
				}
			}
		}
	}
	return nil
}

// rejectNestedContainers enforces the one-level-deep limit on
// components/dynamic zones (see the package doc comment): none of a
// component's own fields may themselves be a relation, a component, or
// a dynamic zone.
func rejectNestedContainers(fields []Field) error {
	for _, f := range fields {
		switch f.Type {
		case FieldRelation:
			return fmt.Errorf("nested field %q: relation fields are not allowed inside a component or dynamic zone", f.Name)
		case FieldComponent, FieldDynamicZone:
			return fmt.Errorf("nested field %q: components/dynamic zones may not themselves contain a component or dynamic zone", f.Name)
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
	case FieldComponent:
		return validateComponentValue(*f.Component, value)
	case FieldDynamicZone:
		return validateDynamicZoneValue(*f.DynamicZone, value)
	}
	return nil
}

// validateComponentValue checks a component field's value: a single
// JSON object (validated against cc.Fields) or, if Repeatable, a list
// of them.
func validateComponentValue(cc ComponentConfig, value any) error {
	if cc.Repeatable {
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("expected a list of component instances")
		}
		for i, item := range items {
			obj, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("item %d: expected a component instance object", i)
			}
			if err := (Definition{Fields: cc.Fields}).ValidateEntry(obj); err != nil {
				return fmt.Errorf("item %d: %w", i, err)
			}
		}
		return nil
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("expected a component instance object")
	}
	return (Definition{Fields: cc.Fields}).ValidateEntry(obj)
}

// validateDynamicZoneValue checks a dynamic zone's value: a list of
// JSON objects, each tagged with "__component" naming which of
// dz.Components it's an instance of, and validated against that
// component's own fields.
func validateDynamicZoneValue(dz DynamicZoneConfig, value any) error {
	items, ok := value.([]any)
	if !ok {
		return fmt.Errorf("expected a list of component instances")
	}
	for i, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("item %d: expected a component instance object", i)
		}
		name, ok := obj[dynamicZoneComponentKey].(string)
		if !ok || name == "" {
			return fmt.Errorf("item %d: missing or invalid %q", i, dynamicZoneComponentKey)
		}
		cc, ok := dz.Components[name]
		if !ok {
			return fmt.Errorf("item %d: %q is not one of this field's allowed components", i, name)
		}
		rest := make(map[string]any, len(obj)-1)
		for k, v := range obj {
			if k != dynamicZoneComponentKey {
				rest[k] = v
			}
		}
		if err := (Definition{Fields: cc.Fields}).ValidateEntry(rest); err != nil {
			return fmt.Errorf("item %d (%s): %w", i, name, err)
		}
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

package schema

import (
	"testing"

	"github.com/google/uuid"
)

func ptrInt(v int) *int           { return &v }
func ptrFloat(v float64) *float64 { return &v }

var fixedFieldID = uuid.New()

func TestDefinitionValidate(t *testing.T) {
	cases := []struct {
		name    string
		def     Definition
		wantErr bool
	}{
		{
			name: "valid simple schema",
			def:  Definition{Fields: []Field{{Name: "title", Type: FieldShortText, Required: true}}},
		},
		{
			name:    "duplicate field name",
			def:     Definition{Fields: []Field{{Name: "title", Type: FieldShortText}, {Name: "title", Type: FieldLongText}}},
			wantErr: true,
		},
		{
			name:    "reserved field name",
			def:     Definition{Fields: []Field{{Name: "id", Type: FieldShortText}}},
			wantErr: true,
		},
		{
			name:    "unknown field type",
			def:     Definition{Fields: []Field{{Name: "foo", Type: "not_a_type"}}},
			wantErr: true,
		},
		{
			name:    "invalid field name characters",
			def:     Definition{Fields: []Field{{Name: "2bad", Type: FieldShortText}}},
			wantErr: true,
		},
		{
			name:    "enumeration without values",
			def:     Definition{Fields: []Field{{Name: "state", Type: FieldEnumeration}}},
			wantErr: true,
		},
		{
			name: "enumeration with values",
			def:  Definition{Fields: []Field{{Name: "state", Type: FieldEnumeration, Enum: []string{"a", "b"}}}},
		},
		{
			// Client-supplied field ids are allowed (re-submitting a
			// previously assigned one), but two different fields can
			// never share one — stored relations are keyed by field id,
			// so a collision would make lookups ambiguous between them.
			name: "duplicate client-supplied field id",
			def: Definition{Fields: []Field{
				{ID: fixedFieldID, Name: "a", Type: FieldShortText},
				{ID: fixedFieldID, Name: "b", Type: FieldShortText},
			}},
			wantErr: true,
		},
		{
			name: "distinct field ids are fine",
			def: Definition{Fields: []Field{
				{ID: fixedFieldID, Name: "a", Type: FieldShortText},
				{ID: uuid.New(), Name: "b", Type: FieldShortText},
			}},
		},
		{
			name: "unknown onDelete policy",
			def: Definition{Fields: []Field{
				{Name: "author", Type: FieldRelation, Relation: &RelationConfig{
					TargetContentType: "author", RelationType: RelationOneToOne, OnDelete: "explode",
				}},
			}},
			wantErr: true,
		},
		{
			name: "valid onDelete policy",
			def: Definition{Fields: []Field{
				{Name: "author", Type: FieldRelation, Relation: &RelationConfig{
					TargetContentType: "author", RelationType: RelationOneToOne, OnDelete: DeleteCascade,
				}},
			}},
		},
		{
			name: "valid component field",
			def: Definition{Fields: []Field{
				{Name: "address", Type: FieldComponent, Component: &ComponentConfig{
					Fields: []Field{{Name: "street", Type: FieldShortText}},
				}},
			}},
		},
		{
			name:    "component field missing configuration",
			def:     Definition{Fields: []Field{{Name: "address", Type: FieldComponent}}},
			wantErr: true,
		},
		{
			name: "component may not nest a relation field",
			def: Definition{Fields: []Field{
				{Name: "address", Type: FieldComponent, Component: &ComponentConfig{
					Fields: []Field{{Name: "owner", Type: FieldRelation, Relation: &RelationConfig{
						TargetContentType: "author", RelationType: RelationManyToOne,
					}}},
				}},
			}},
			wantErr: true,
		},
		{
			name: "component may not nest another component",
			def: Definition{Fields: []Field{
				{Name: "outer", Type: FieldComponent, Component: &ComponentConfig{
					Fields: []Field{{Name: "inner", Type: FieldComponent, Component: &ComponentConfig{
						Fields: []Field{{Name: "x", Type: FieldShortText}},
					}}},
				}},
			}},
			wantErr: true,
		},
		{
			name: "duplicate field id across a component boundary is still rejected",
			def: Definition{Fields: []Field{
				{ID: fixedFieldID, Name: "top", Type: FieldShortText},
				{Name: "box", Type: FieldComponent, Component: &ComponentConfig{
					Fields: []Field{{ID: fixedFieldID, Name: "inner", Type: FieldShortText}},
				}},
			}},
			wantErr: true,
		},
		{
			name: "valid dynamic zone field",
			def: Definition{Fields: []Field{
				{Name: "sections", Type: FieldDynamicZone, DynamicZone: &DynamicZoneConfig{
					Components: map[string]ComponentConfig{
						"quote": {Fields: []Field{{Name: "text", Type: FieldShortText}}},
					},
				}},
			}},
		},
		{
			name:    "dynamic zone field with no components",
			def:     Definition{Fields: []Field{{Name: "sections", Type: FieldDynamicZone, DynamicZone: &DynamicZoneConfig{}}}},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.def.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateEntry(t *testing.T) {
	def := Definition{Fields: []Field{
		{Name: "title", Type: FieldShortText, Required: true, MaxLength: ptrInt(10)},
		{Name: "views", Type: FieldInteger, Min: ptrFloat(0)},
		{Name: "price", Type: FieldDecimal},
		{Name: "published", Type: FieldBoolean},
		{Name: "contact", Type: FieldEmail},
		{Name: "homepage", Type: FieldURL},
		{Name: "releaseDate", Type: FieldDate},
		{Name: "publishedAt", Type: FieldDateTime},
		{Name: "state", Type: FieldEnumeration, Enum: []string{"draft", "live"}},
	}}

	cases := []struct {
		name    string
		data    map[string]any
		wantErr bool
	}{
		{name: "missing required field", data: map[string]any{}, wantErr: true},
		{name: "valid minimal", data: map[string]any{"title": "hello"}},
		{name: "title too long", data: map[string]any{"title": "this is way too long"}, wantErr: true},
		{name: "unexpected field rejected", data: map[string]any{"title": "x", "nope": "y"}, wantErr: true},
		{name: "integer below min", data: map[string]any{"title": "x", "views": -1.0}, wantErr: true},
		{name: "integer not whole", data: map[string]any{"title": "x", "views": 1.5}, wantErr: true},
		{name: "valid integer", data: map[string]any{"title": "x", "views": 5.0}},
		{name: "bad email", data: map[string]any{"title": "x", "contact": "not-an-email"}, wantErr: true},
		{name: "good email", data: map[string]any{"title": "x", "contact": "a@b.com"}},
		{name: "bad url", data: map[string]any{"title": "x", "homepage": "not a url"}, wantErr: true},
		{name: "good url", data: map[string]any{"title": "x", "homepage": "https://example.com"}},
		{name: "bad date", data: map[string]any{"title": "x", "releaseDate": "not-a-date"}, wantErr: true},
		{name: "good date", data: map[string]any{"title": "x", "releaseDate": "2026-01-01"}},
		{name: "bad enum value", data: map[string]any{"title": "x", "state": "archived"}, wantErr: true},
		{name: "good enum value", data: map[string]any{"title": "x", "state": "live"}},
		{name: "wrong type for boolean", data: map[string]any{"title": "x", "published": "yes"}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := def.ValidateEntry(tc.data)
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateEntryComponentsAndDynamicZones(t *testing.T) {
	def := Definition{Fields: []Field{
		{Name: "title", Type: FieldShortText, Required: true},
		{Name: "address", Type: FieldComponent, Component: &ComponentConfig{
			Fields: []Field{
				{Name: "street", Type: FieldShortText, Required: true},
				{Name: "zip", Type: FieldShortText},
			},
		}},
		{Name: "links", Type: FieldComponent, Component: &ComponentConfig{
			Repeatable: true,
			Fields:     []Field{{Name: "url", Type: FieldURL, Required: true}},
		}},
		{Name: "sections", Type: FieldDynamicZone, DynamicZone: &DynamicZoneConfig{
			Components: map[string]ComponentConfig{
				"quote": {Fields: []Field{{Name: "text", Type: FieldShortText, Required: true}}},
				"image": {Fields: []Field{{Name: "url", Type: FieldURL, Required: true}}},
			},
		}},
	}}

	cases := []struct {
		name    string
		data    map[string]any
		wantErr bool
	}{
		{
			name: "valid component, repeatable component, and dynamic zone",
			data: map[string]any{
				"title":   "Hello",
				"address": map[string]any{"street": "1 Main St"},
				"links":   []any{map[string]any{"url": "https://example.com"}},
				"sections": []any{
					map[string]any{"__component": "quote", "text": "hi"},
					map[string]any{"__component": "image", "url": "https://example.com/a.png"},
				},
			},
		},
		{
			name: "component missing a required nested field",
			data: map[string]any{
				"title":   "Hello",
				"address": map[string]any{},
			},
			wantErr: true,
		},
		{
			name: "repeatable component given a single object instead of a list",
			data: map[string]any{
				"title": "Hello",
				"links": map[string]any{"url": "https://example.com"},
			},
			wantErr: true,
		},
		{
			name: "dynamic zone item naming an unknown component",
			data: map[string]any{
				"title":    "Hello",
				"sections": []any{map[string]any{"__component": "nope", "text": "hi"}},
			},
			wantErr: true,
		},
		{
			name: "dynamic zone item missing __component",
			data: map[string]any{
				"title":    "Hello",
				"sections": []any{map[string]any{"text": "hi"}},
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := def.ValidateEntry(tc.data)
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

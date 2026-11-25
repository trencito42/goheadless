package schema

import "testing"

func ptrInt(v int) *int           { return &v }
func ptrFloat(v float64) *float64 { return &v }

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

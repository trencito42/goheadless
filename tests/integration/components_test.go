package integration

import (
	"net/http"
	"testing"
)

// TestComponentsAndDynamicZonesEndToEnd exercises the component and
// dynamic_zone field types through the real HTTP API end to end:
// creating a content type with a single component, a repeatable
// component, and a dynamic zone; writing valid data; and confirming
// both the round trip and schema-level validation of nested fields.
func TestComponentsAndDynamicZonesEndToEnd(t *testing.T) {
	env := newTestEnv(t)
	owner := env.bootstrapOwner(t, "owner@example.com", "owner-password-1")

	_, project := owner.do(http.MethodPost, "/api/v1/manage/projects/", map[string]any{"name": "Docs", "slug": "docs"})
	projectID := project["id"].(string)

	resp, body := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content-types/", map[string]any{
		"apiId": "page", "displayName": "Page", "kind": "collection",
		"schema": map[string]any{"fields": []map[string]any{
			{"name": "title", "type": "short_text", "required": true},
			{"name": "seo", "type": "component", "component": map[string]any{
				"fields": []map[string]any{
					{"name": "metaTitle", "type": "short_text", "required": true},
					{"name": "metaDescription", "type": "short_text"},
				},
			}},
			{"name": "links", "type": "component", "component": map[string]any{
				"repeatable": true,
				"fields":     []map[string]any{{"name": "url", "type": "url", "required": true}},
			}},
			{"name": "sections", "type": "dynamic_zone", "dynamicZone": map[string]any{
				"components": map[string]any{
					"quote": map[string]any{"fields": []map[string]any{{"name": "text", "type": "short_text", "required": true}}},
					"image": map[string]any{"fields": []map[string]any{{"name": "url", "type": "url", "required": true}}},
				},
			}},
		}},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create content type: %d %v", resp.StatusCode, body)
	}

	resp, entry := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/page/", map[string]any{
		"data": map[string]any{
			"title": "Home",
			"seo":   map[string]any{"metaTitle": "Welcome"},
			"links": []map[string]any{{"url": "https://example.com/a"}, {"url": "https://example.com/b"}},
			"sections": []map[string]any{
				{"__component": "quote", "text": "Hello world"},
				{"__component": "image", "url": "https://example.com/hero.png"},
			},
		},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create entry: %d %v", resp.StatusCode, entry)
	}
	data := entry["data"].(map[string]any)
	if data["seo"].(map[string]any)["metaTitle"] != "Welcome" {
		t.Fatalf("expected component data to round-trip, got %v", data["seo"])
	}
	links := data["links"].([]any)
	if len(links) != 2 {
		t.Fatalf("expected 2 repeatable component instances, got %v", links)
	}
	sections := data["sections"].([]any)
	if len(sections) != 2 {
		t.Fatalf("expected 2 dynamic zone instances, got %v", sections)
	}
	if sections[0].(map[string]any)["__component"] != "quote" {
		t.Fatalf("expected first section to be a quote, got %v", sections[0])
	}

	documentID := entry["documentId"].(string)
	resp, got := owner.do(http.MethodGet, "/api/v1/manage/projects/"+projectID+"/content/page/"+documentID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get entry: %d %v", resp.StatusCode, got)
	}

	// Required nested fields are enforced: omitting seo.metaTitle fails.
	resp, body = owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/page/", map[string]any{
		"data": map[string]any{"title": "Missing", "seo": map[string]any{}},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing required nested component field, got %d %v", resp.StatusCode, body)
	}

	// An unknown dynamic zone component is rejected.
	resp, body = owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/page/", map[string]any{
		"data": map[string]any{"title": "Bad section", "sections": []map[string]any{{"__component": "video", "text": "nope"}}},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown dynamic zone component, got %d %v", resp.StatusCode, body)
	}
}

// TestComponentRejectsNestedRelationField confirms the content-type
// creation endpoint rejects a component whose own fields try to
// include a relation field — components have no from_entry_id to key
// entry_relations by (see internal/schema's package doc comment).
func TestComponentRejectsNestedRelationField(t *testing.T) {
	env := newTestEnv(t)
	owner := env.bootstrapOwner(t, "owner@example.com", "owner-password-1")

	_, project := owner.do(http.MethodPost, "/api/v1/manage/projects/", map[string]any{"name": "Docs", "slug": "docs"})
	projectID := project["id"].(string)

	resp, body := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content-types/", map[string]any{
		"apiId": "page", "displayName": "Page", "kind": "collection",
		"schema": map[string]any{"fields": []map[string]any{
			{"name": "box", "type": "component", "component": map[string]any{
				"fields": []map[string]any{
					{"name": "owner", "type": "relation", "relation": map[string]any{
						"targetContentType": "page", "relationType": "many_to_one",
					}},
				},
			}},
		}},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 rejecting a relation field nested inside a component, got %d %v", resp.StatusCode, body)
	}
}

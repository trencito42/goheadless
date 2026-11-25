package integration

import (
	"net/http"
	"testing"
)

func TestFullVerticalSlice(t *testing.T) {
	env := newTestEnv(t)
	owner := env.bootstrapOwner(t, "owner@example.com", "owner-password-1")

	resp, project := owner.do(http.MethodPost, "/api/v1/manage/projects/", map[string]any{
		"name": "Blog", "slug": "blog",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: %d %v", resp.StatusCode, project)
	}
	projectID := project["id"].(string)

	resp, ct := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content-types/", map[string]any{
		"apiId":       "article",
		"displayName": "Article",
		"kind":        "collection",
		"schema": map[string]any{
			"fields": []map[string]any{
				{"name": "title", "type": "short_text", "required": true},
			},
		},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create content type: %d %v", resp.StatusCode, ct)
	}

	resp, entry := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/article/", map[string]any{
		"data": map[string]any{"title": "Hello world"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create entry: %d %v", resp.StatusCode, entry)
	}
	documentID := entry["documentId"].(string)
	if entry["status"] != "draft" {
		t.Fatalf("expected new entry to be a draft, got %v", entry["status"])
	}

	// Not published yet: delivery API must not see it.
	resp, delivery := owner.do(http.MethodGet, "/api/v1/projects/"+projectID+"/content/article/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list published: %d %v", resp.StatusCode, delivery)
	}
	if data, _ := delivery["data"].([]any); len(data) != 0 {
		t.Fatalf("expected no published entries before publish, got %v", data)
	}

	resp, published := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/article/"+documentID+"/publish", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish: %d %v", resp.StatusCode, published)
	}
	if published["status"] != "published" {
		t.Fatalf("expected published status, got %v", published["status"])
	}

	resp, delivery = owner.do(http.MethodGet, "/api/v1/projects/"+projectID+"/content/article/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list published after publish: %d %v", resp.StatusCode, delivery)
	}
	data, _ := delivery["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("expected exactly one published entry, got %v", data)
	}
}

func TestSchemaValidationRejectsUnexpectedAndMissingFields(t *testing.T) {
	env := newTestEnv(t)
	owner := env.bootstrapOwner(t, "owner@example.com", "owner-password-1")

	_, project := owner.do(http.MethodPost, "/api/v1/manage/projects/", map[string]any{"name": "Blog", "slug": "blog"})
	projectID := project["id"].(string)
	owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content-types/", map[string]any{
		"apiId": "article", "displayName": "Article", "kind": "collection",
		"schema": map[string]any{"fields": []map[string]any{{"name": "title", "type": "short_text", "required": true}}},
	})

	resp, body := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/article/", map[string]any{
		"data": map[string]any{}, // missing required "title"
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing required field, got %d %v", resp.StatusCode, body)
	}

	resp, body = owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/article/", map[string]any{
		"data": map[string]any{"title": "ok", "notAField": "x"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unexpected field, got %d %v", resp.StatusCode, body)
	}
}

func TestOwnerOnlyRoutesRejectNonOwner(t *testing.T) {
	env := newTestEnv(t)
	owner := env.bootstrapOwner(t, "owner@example.com", "owner-password-1")

	resp, user := owner.do(http.MethodPost, "/api/v1/manage/users/", map[string]any{
		"email": "member@example.com", "password": "member-password-1",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("owner create user: %d %v", resp.StatusCode, user)
	}

	member := newClient(t, env.srv)
	resp, _ = member.do(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"email": "member@example.com", "password": "member-password-1",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("member login failed: %d", resp.StatusCode)
	}

	resp, body := member.do(http.MethodPost, "/api/v1/manage/users/", map[string]any{"email": "x@example.com", "password": "x"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owner creating a user, got %d %v", resp.StatusCode, body)
	}

	resp, body = member.do(http.MethodPost, "/api/v1/manage/projects/", map[string]any{"name": "X", "slug": "x"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owner creating a project, got %d %v", resp.StatusCode, body)
	}
}

func TestCrossProjectIsolationAndRoleFloors(t *testing.T) {
	env := newTestEnv(t)
	owner := env.bootstrapOwner(t, "owner@example.com", "owner-password-1")

	_, projectA := owner.do(http.MethodPost, "/api/v1/manage/projects/", map[string]any{"name": "A", "slug": "project-a"})
	projectAID := projectA["id"].(string)
	owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectAID+"/content-types/", map[string]any{
		"apiId": "note", "displayName": "Note", "kind": "collection",
		"schema": map[string]any{"fields": []map[string]any{{"name": "body", "type": "short_text"}}},
	})

	_, user := owner.do(http.MethodPost, "/api/v1/manage/users/", map[string]any{
		"email": "member@example.com", "password": "member-password-1",
	})
	userID := user["id"].(string)

	member := newClient(t, env.srv)
	member.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "member@example.com", "password": "member-password-1"})

	// Not a member of project A yet: every project-scoped route 403s,
	// and project A doesn't show up in their project list.
	resp, body := member.do(http.MethodGet, "/api/v1/manage/projects/"+projectAID+"/content-types/", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for non-member reading content types, got %d %v", resp.StatusCode, body)
	}
	resp, listBody := member.do(http.MethodGet, "/api/v1/manage/projects/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list projects: %d", resp.StatusCode)
	}
	// listBody is a map because our client always decodes into
	// map[string]any; a JSON array response decodes to nil here, which
	// is itself the signal that the list came back empty (an object
	// response would have decoded into a populated map).
	if listBody != nil {
		t.Fatalf("expected an empty project list for a non-member, got %v", listBody)
	}

	// Owner adds them as viewer: can read, cannot write.
	resp, _ = owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectAID+"/members/", map[string]any{
		"userId": userID, "role": "viewer",
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("add member: %d", resp.StatusCode)
	}

	resp, body = member.do(http.MethodGet, "/api/v1/manage/projects/"+projectAID+"/content-types/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("viewer should be able to read content types: %d %v", resp.StatusCode, body)
	}
	resp, body = member.do(http.MethodPost, "/api/v1/manage/projects/"+projectAID+"/content/note/", map[string]any{
		"data": map[string]any{"body": "nope"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer creating an entry, got %d %v", resp.StatusCode, body)
	}

	// Upgrade to editor: can now create, and created_by is attributed
	// correctly to the member, not the owner who added them.
	resp, _ = owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectAID+"/members/", map[string]any{
		"userId": userID, "role": "editor",
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("upgrade member: %d", resp.StatusCode)
	}
	resp, entry := member.do(http.MethodPost, "/api/v1/manage/projects/"+projectAID+"/content/note/", map[string]any{
		"data": map[string]any{"body": "from editor"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("editor create entry: %d %v", resp.StatusCode, entry)
	}
	if entry["createdBy"] != userID {
		t.Fatalf("expected createdBy to be the editor %q, got %v", userID, entry["createdBy"])
	}
}

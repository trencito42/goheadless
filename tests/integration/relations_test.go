package integration

import (
	"net/http"
	"testing"
)

// setupBlogWithAuthorAndComment creates a project with two related
// content types: "author" (short_text name, a "private" internal note
// field) and "post" (short_text title, many_to_one relation "author" ->
// author, one_to_many relation "tags" -> tag... kept simple: a single
// many_to_one relation to "author"). Returns the owner client, project
// id, and both content type api ids.
func setupBlogWithAuthorAndComment(t *testing.T, env *testEnv) (owner *client, projectID string) {
	t.Helper()
	owner = env.bootstrapOwner(t, "owner@example.com", "owner-password-1")

	_, project := owner.do(http.MethodPost, "/api/v1/manage/projects/", map[string]any{"name": "Blog", "slug": "blog"})
	projectID = project["id"].(string)

	resp, body := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content-types/", map[string]any{
		"apiId": "author", "displayName": "Author", "kind": "collection",
		"schema": map[string]any{"fields": []map[string]any{
			{"name": "name", "type": "short_text", "required": true},
			{"name": "secretNote", "type": "short_text", "private": true},
		}},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create author content type: %d %v", resp.StatusCode, body)
	}

	resp, body = owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content-types/", map[string]any{
		"apiId": "post", "displayName": "Post", "kind": "collection",
		"schema": map[string]any{"fields": []map[string]any{
			{"name": "title", "type": "short_text", "required": true},
			{"name": "author", "type": "relation", "relation": map[string]any{
				"targetContentType": "author", "relationType": "many_to_one",
			}},
			{"name": "related", "type": "relation", "relation": map[string]any{
				"targetContentType": "post", "relationType": "one_to_many",
			}},
		}},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create post content type: %d %v", resp.StatusCode, body)
	}
	return owner, projectID
}

func TestRelationCreateAndFetch(t *testing.T) {
	env := newTestEnv(t)
	owner, projectID := setupBlogWithAuthorAndComment(t, env)

	resp, authorEntry := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/author/", map[string]any{
		"data": map[string]any{"name": "Ada", "secretNote": "internal only"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create author: %d %v", resp.StatusCode, authorEntry)
	}
	authorDocID := authorEntry["documentId"].(string)

	resp, postEntry := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/", map[string]any{
		"data": map[string]any{"title": "Hello", "author": authorDocID},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create post: %d %v", resp.StatusCode, postEntry)
	}
	if postEntry["data"].(map[string]any)["author"] != authorDocID {
		t.Fatalf("expected author relation stub %q in response, got %v", authorDocID, postEntry["data"])
	}
	postDocID := postEntry["documentId"].(string)

	// Management read (draft) sees the private field on the related
	// author when expanded; redaction is a delivery-only concern at the
	// top level, but expanded embeds are always redacted regardless.
	resp, got := owner.do(http.MethodGet, "/api/v1/manage/projects/"+projectID+"/content/post/"+postDocID+"?include=author", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get draft post with include: %d %v", resp.StatusCode, got)
	}
	authorExpanded, ok := got["data"].(map[string]any)["author"].(map[string]any)
	if !ok {
		t.Fatalf("expected expanded author object, got %v", got["data"].(map[string]any)["author"])
	}
	expandedData := authorExpanded["data"].(map[string]any)
	if _, leaked := expandedData["secretNote"]; leaked {
		t.Fatalf("expanded relation leaked a private field: %v", expandedData)
	}
	if expandedData["name"] != "Ada" {
		t.Fatalf("expected expanded author name %q, got %v", "Ada", expandedData)
	}

	// The author's own private field IS visible on its own draft read
	// (editors need to see and edit it) but hidden from delivery reads.
	resp, authorDraft := owner.do(http.MethodGet, "/api/v1/manage/projects/"+projectID+"/content/author/"+authorDocID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get author draft: %d", resp.StatusCode)
	}
	if authorDraft["data"].(map[string]any)["secretNote"] != "internal only" {
		t.Fatalf("expected draft read to show private field, got %v", authorDraft["data"])
	}
}

func TestRelationCrossProjectReferenceRejected(t *testing.T) {
	env := newTestEnv(t)
	owner, projectID := setupBlogWithAuthorAndComment(t, env)

	_, projectB := owner.do(http.MethodPost, "/api/v1/manage/projects/", map[string]any{"name": "Other", "slug": "other"})
	projectBID := projectB["id"].(string)
	owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectBID+"/content-types/", map[string]any{
		"apiId": "author", "displayName": "Author", "kind": "collection",
		"schema": map[string]any{"fields": []map[string]any{{"name": "name", "type": "short_text"}}},
	})
	_, foreignAuthor := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectBID+"/content/author/", map[string]any{
		"data": map[string]any{"name": "Grace"},
	})
	foreignAuthorDocID := foreignAuthor["documentId"].(string)

	resp, body := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/", map[string]any{
		"data": map[string]any{"title": "Hello", "author": foreignAuthorDocID},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 rejecting a cross-project relation target, got %d %v", resp.StatusCode, body)
	}
}

func TestRelationSelfReferencingAndToMany(t *testing.T) {
	env := newTestEnv(t)
	owner, projectID := setupBlogWithAuthorAndComment(t, env)

	_, post1 := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/", map[string]any{
		"data": map[string]any{"title": "First"},
	})
	post1ID := post1["documentId"].(string)
	_, post2 := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/", map[string]any{
		"data": map[string]any{"title": "Second"},
	})
	post2ID := post2["documentId"].(string)

	resp, post3 := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/", map[string]any{
		"data": map[string]any{"title": "Third", "related": []string{post1ID, post2ID}},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create self-referencing to-many relation: %d %v", resp.StatusCode, post3)
	}
	related, ok := post3["data"].(map[string]any)["related"].([]any)
	if !ok || len(related) != 2 {
		t.Fatalf("expected 2 related document ids, got %v", post3["data"].(map[string]any)["related"])
	}
}

func TestRelationDraftAndPublishedAreIndependentSnapshots(t *testing.T) {
	env := newTestEnv(t)
	owner, projectID := setupBlogWithAuthorAndComment(t, env)

	_, authorA := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/author/", map[string]any{
		"data": map[string]any{"name": "Ada"},
	})
	authorAID := authorA["documentId"].(string)
	_, authorB := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/author/", map[string]any{
		"data": map[string]any{"name": "Grace"},
	})
	authorBID := authorB["documentId"].(string)

	resp, post := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/", map[string]any{
		"data": map[string]any{"title": "Hello", "author": authorAID},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create post: %d %v", resp.StatusCode, post)
	}
	postID := post["documentId"].(string)

	resp, published := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/"+postID+"/publish", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish: %d %v", resp.StatusCode, published)
	}
	if published["data"].(map[string]any)["author"] != authorAID {
		t.Fatalf("expected published author %q, got %v", authorAID, published["data"])
	}

	// Change the draft's relation to author B, but do NOT republish.
	resp, updated := owner.do(http.MethodPatch, "/api/v1/manage/projects/"+projectID+"/content/post/"+postID, map[string]any{
		"expectedVersion": int(post["version"].(float64)),
		"data":            map[string]any{"title": "Hello", "author": authorBID},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update draft relation: %d %v", resp.StatusCode, updated)
	}
	if updated["data"].(map[string]any)["author"] != authorBID {
		t.Fatalf("expected updated draft author %q, got %v", authorBID, updated["data"])
	}

	// Published variant must still show author A: draft and published
	// relation snapshots are independent until the next publish.
	resp, publishedAgain := owner.do(http.MethodGet, "/api/v1/projects/"+projectID+"/content/post/"+postID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get published: %d %v", resp.StatusCode, publishedAgain)
	}
	if publishedAgain["data"].(map[string]any)["author"] != authorAID {
		t.Fatalf("expected published snapshot to still show author A %q, got %v", authorAID, publishedAgain["data"])
	}

	// Republishing copies the draft's current relation onto published.
	resp, republished := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/"+postID+"/publish", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("republish: %d %v", resp.StatusCode, republished)
	}
	if republished["data"].(map[string]any)["author"] != authorBID {
		t.Fatalf("expected republished author %q, got %v", authorBID, republished["data"])
	}
}

func TestAuthorCannotEditAnotherAuthorsDraftButEditorCan(t *testing.T) {
	env := newTestEnv(t)
	owner := env.bootstrapOwner(t, "owner@example.com", "owner-password-1")

	_, project := owner.do(http.MethodPost, "/api/v1/manage/projects/", map[string]any{"name": "Blog", "slug": "blog"})
	projectID := project["id"].(string)
	owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content-types/", map[string]any{
		"apiId": "post", "displayName": "Post", "kind": "collection",
		"schema": map[string]any{"fields": []map[string]any{{"name": "title", "type": "short_text", "required": true}}},
	})

	_, userA := owner.do(http.MethodPost, "/api/v1/manage/users/", map[string]any{"email": "a@example.com", "password": "password-a1"})
	userAID := userA["id"].(string)
	_, userB := owner.do(http.MethodPost, "/api/v1/manage/users/", map[string]any{"email": "b@example.com", "password": "password-b1"})
	userBID := userB["id"].(string)

	owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/members/", map[string]any{"userId": userAID, "role": "author"})
	owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/members/", map[string]any{"userId": userBID, "role": "author"})

	authorA := newClient(t, env.srv)
	authorA.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "a@example.com", "password": "password-a1"})
	authorB := newClient(t, env.srv)
	authorB.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "b@example.com", "password": "password-b1"})

	resp, entry := authorA.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/", map[string]any{
		"data": map[string]any{"title": "A's draft"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("author A create: %d %v", resp.StatusCode, entry)
	}
	documentID := entry["documentId"].(string)
	version := int(entry["version"].(float64))

	// Author B (not the creator, not editor+) must be rejected.
	resp, body := authorB.do(http.MethodPatch, "/api/v1/manage/projects/"+projectID+"/content/post/"+documentID, map[string]any{
		"expectedVersion": version,
		"data":            map[string]any{"title": "hijacked"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for author B editing author A's draft, got %d %v", resp.StatusCode, body)
	}

	// The creator themself can still edit their own draft.
	resp, body = authorA.do(http.MethodPatch, "/api/v1/manage/projects/"+projectID+"/content/post/"+documentID, map[string]any{
		"expectedVersion": version,
		"data":            map[string]any{"title": "A's edit"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected author A to edit their own draft, got %d %v", resp.StatusCode, body)
	}

	// Promote B to editor: editors can edit anyone's draft.
	owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/members/", map[string]any{"userId": userBID, "role": "editor"})
	resp, body = authorB.do(http.MethodPatch, "/api/v1/manage/projects/"+projectID+"/content/post/"+documentID, map[string]any{
		"expectedVersion": int(body["version"].(float64)),
		"data":            map[string]any{"title": "editor override"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected editor to be able to edit another author's draft, got %d %v", resp.StatusCode, body)
	}
}

func TestDeliveryAPIRedactsPrivateFields(t *testing.T) {
	env := newTestEnv(t)
	owner, projectID := setupBlogWithAuthorAndComment(t, env)

	_, author := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/author/", map[string]any{
		"data": map[string]any{"name": "Ada", "secretNote": "hidden"},
	})
	authorID := author["documentId"].(string)
	owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/author/"+authorID+"/publish", nil)

	resp, published := owner.do(http.MethodGet, "/api/v1/projects/"+projectID+"/content/author/"+authorID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get published author: %d %v", resp.StatusCode, published)
	}
	if _, leaked := published["data"].(map[string]any)["secretNote"]; leaked {
		t.Fatalf("delivery API leaked a private field: %v", published["data"])
	}
	if published["data"].(map[string]any)["name"] != "Ada" {
		t.Fatalf("expected public field to still be present, got %v", published["data"])
	}
}

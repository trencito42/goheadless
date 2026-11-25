package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
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
			{"name": "featuredAuthor", "type": "relation", "relation": map[string]any{
				"targetContentType": "author", "relationType": "one_to_one",
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
	// Both targets need their own published variant, or the delivery API's
	// unpublished-target filtering (see relations.go filterUndeliverableTargets)
	// would correctly null out the stub regardless of snapshot independence,
	// and this test wants to isolate the snapshot-independence behavior.
	owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/author/"+authorAID+"/publish", nil)
	owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/author/"+authorBID+"/publish", nil)

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

func TestManyToOneAllowsMultipleSourcesToSameTarget(t *testing.T) {
	env := newTestEnv(t)
	owner, projectID := setupBlogWithAuthorAndComment(t, env)

	_, author := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/author/", map[string]any{
		"data": map[string]any{"name": "Ada"},
	})
	authorID := author["documentId"].(string)
	owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/author/"+authorID+"/publish", nil)

	for i, title := range []string{"First post", "Second post"} {
		resp, post := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/", map[string]any{
			"data": map[string]any{"title": title, "author": authorID},
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create post %d: %d %v", i, resp.StatusCode, post)
		}
		postID := post["documentId"].(string)
		resp, published := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/"+postID+"/publish", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("many_to_one must allow multiple published sources to reference the same target, post %d: %d %v", i, resp.StatusCode, published)
		}
		if published["data"].(map[string]any)["author"] != authorID {
			t.Fatalf("post %d: expected author %q, got %v", i, authorID, published["data"])
		}
	}
}

func TestOneToOneRejectsSecondPublishedReferenceButAllowsConcurrentDrafts(t *testing.T) {
	env := newTestEnv(t)
	owner, projectID := setupBlogWithAuthorAndComment(t, env)

	_, author := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/author/", map[string]any{
		"data": map[string]any{"name": "Ada"},
	})
	authorID := author["documentId"].(string)
	owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/author/"+authorID+"/publish", nil)

	_, post1 := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/", map[string]any{
		"data": map[string]any{"title": "Spotlight 1", "featuredAuthor": authorID},
	})
	post1ID := post1["documentId"].(string)

	// Two different documents' DRAFTS are allowed to transiently both
	// point at the same one_to_one target — the conflict is only real
	// once one of them actually publishes (see docs/adr/0003).
	resp, post2 := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/", map[string]any{
		"data": map[string]any{"title": "Spotlight 2", "featuredAuthor": authorID},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected two drafts to both be allowed to target the same one_to_one target: %d %v", resp.StatusCode, post2)
	}
	post2ID := post2["documentId"].(string)

	resp, published1 := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/"+post1ID+"/publish", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish post1: %d %v", resp.StatusCode, published1)
	}

	resp, body := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/"+post2ID+"/publish", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 publishing a second one_to_one reference to an already-published target, got %d %v", resp.StatusCode, body)
	}
}

func TestDeliveryFiltersUnpublishedRelationTargets(t *testing.T) {
	env := newTestEnv(t)
	owner, projectID := setupBlogWithAuthorAndComment(t, env)

	// Author is created but never published.
	_, author := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/author/", map[string]any{
		"data": map[string]any{"name": "Ada"},
	})
	authorID := author["documentId"].(string)

	_, post := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/", map[string]any{
		"data": map[string]any{"title": "Hello", "author": authorID},
	})
	postID := post["documentId"].(string)
	resp, published := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/"+postID+"/publish", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish: %d %v", resp.StatusCode, published)
	}

	// Draft/management read still shows the stub — editors need to see
	// it to notice and fix the dangling reference.
	resp, draft := owner.do(http.MethodGet, "/api/v1/manage/projects/"+projectID+"/content/post/"+postID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get draft: %d %v", resp.StatusCode, draft)
	}
	if draft["data"].(map[string]any)["author"] != authorID {
		t.Fatalf("expected draft read to still show the unpublished-target stub, got %v", draft["data"])
	}

	// Delivery read must NOT deliver an id that doesn't resolve to a
	// published document — the author was never published.
	resp, deliveryPost := owner.do(http.MethodGet, "/api/v1/projects/"+projectID+"/content/post/"+postID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get published post: %d %v", resp.StatusCode, deliveryPost)
	}
	if got := deliveryPost["data"].(map[string]any)["author"]; got != nil {
		t.Fatalf("delivery API delivered an unpublished target's id %v instead of null", got)
	}
}

func TestLocaleAwareRelationExpansion(t *testing.T) {
	env := newTestEnv(t)
	owner, projectID := setupBlogWithAuthorAndComment(t, env)

	resp, author := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/author/", map[string]any{
		"data": map[string]any{"name": "Ada"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create author: %d %v", resp.StatusCode, author)
	}
	authorID := author["documentId"].(string)
	contentTypeID := author["contentTypeId"].(string)
	resp, publishedAuthor := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/author/"+authorID+"/publish", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish author: %d %v", resp.StatusCode, publishedAuthor)
	}

	// Simulate a second locale variant of the SAME document by inserting
	// directly (there is no API to add a locale to an existing document
	// yet — see PROGRESS.md localization gaps). Without locale filtering,
	// expandIncludes' target lookup would match either row nondeterministically.
	frData, err := json.Marshal(map[string]any{"name": "Adalisa"})
	if err != nil {
		t.Fatalf("marshal fr data: %v", err)
	}
	_, err = env.pool.Exec(context.Background(), `
		INSERT INTO entries (project_id, content_type_id, document_id, locale, status, data)
		VALUES ($1, $2, $3, 'fr', 'published', $4)`,
		mustParseUUID(t, projectID), mustParseUUID(t, contentTypeID), mustParseUUID(t, authorID), frData)
	if err != nil {
		t.Fatalf("insert fr variant: %v", err)
	}

	_, post := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/", map[string]any{
		"data": map[string]any{"title": "Hello", "author": authorID},
	})
	postID := post["documentId"].(string)
	resp, publishedPost := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/"+postID+"/publish", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish post: %d %v", resp.StatusCode, publishedPost)
	}

	resp, got := owner.do(http.MethodGet, "/api/v1/projects/"+projectID+"/content/post/"+postID+"?locale=en&include=author", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get published post with include: %d %v", resp.StatusCode, got)
	}
	authorExpanded, ok := got["data"].(map[string]any)["author"].(map[string]any)
	if !ok {
		t.Fatalf("expected expanded author object, got %v", got["data"].(map[string]any)["author"])
	}
	name := authorExpanded["data"].(map[string]any)["name"]
	if name != "Ada" {
		t.Fatalf("expected locale=en expansion to resolve the EN author row (name %q), got %q — locale filtering regressed", "Ada", name)
	}
}

func TestDocumentDeletionCascadesToEntriesAndRelations(t *testing.T) {
	env := newTestEnv(t)
	owner, projectID := setupBlogWithAuthorAndComment(t, env)

	_, author := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/author/", map[string]any{
		"data": map[string]any{"name": "Ada"},
	})
	authorID := author["documentId"].(string)
	owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/author/"+authorID+"/publish", nil)

	_, post := owner.do(http.MethodPost, "/api/v1/manage/projects/"+projectID+"/content/post/", map[string]any{
		"data": map[string]any{"title": "Hello", "author": authorID},
	})
	postID := post["documentId"].(string)

	// There is no "delete a document" API yet (see PROGRESS.md), so this
	// exercises the FK cascade (documents -> entries, documents ->
	// entry_relations.to_document_id) directly against the database —
	// it's the mechanism a future delete endpoint would rely on.
	if _, err := env.pool.Exec(context.Background(), `DELETE FROM documents WHERE id = $1`, mustParseUUID(t, authorID)); err != nil {
		t.Fatalf("delete document: %v", err)
	}

	resp, body := owner.do(http.MethodGet, "/api/v1/manage/projects/"+projectID+"/content/author/"+authorID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected the author's entries rows to be gone after its document was deleted, got %d %v", resp.StatusCode, body)
	}

	resp, draft := owner.do(http.MethodGet, "/api/v1/manage/projects/"+projectID+"/content/post/"+postID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get post after target deletion: %d %v", resp.StatusCode, draft)
	}
	if got := draft["data"].(map[string]any)["author"]; got != nil {
		t.Fatalf("expected the relation to the deleted document to be gone (cascaded), got %v", got)
	}
}

func mustParseUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return id
}

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Badge, Button, Card, Checkbox, DataTable, EmptyState, PageHeader, Select, Tabs, TextField } from "@b4/react";

import { api, type ContentType, type FieldType, type Project, type SchemaField } from "../api/client";

const FIELD_TYPE_OPTIONS: { value: FieldType; label: string }[] = [
  { value: "short_text", label: "Short text" },
  { value: "long_text", label: "Long text" },
  { value: "rich_text", label: "Rich text" },
  { value: "integer", label: "Integer" },
  { value: "decimal", label: "Decimal" },
  { value: "boolean", label: "Boolean" },
  { value: "email", label: "Email" },
  { value: "url", label: "URL" },
  { value: "date", label: "Date" },
  { value: "datetime", label: "DateTime" },
  { value: "enumeration", label: "Enumeration" },
  { value: "json", label: "JSON" },
];

export function ProjectDetail({ project, onBack }: { project: Project; onBack: () => void }) {
  const [selected, setSelected] = useState<ContentType | null>(null);

  return (
    <div className="page-content">
      <Button variant="ghost" size="sm" onClick={onBack} style={{ alignSelf: "flex-start" }}>
        ← All projects
      </Button>
      <PageHeader title={project.name} description={project.slug} />

      <Tabs
        aria-label="Project sections"
        defaultValue="content-types"
        items={[
          {
            value: "content-types",
            label: "Content Type Builder",
            content: <ContentTypeBuilder project={project} onSelect={setSelected} />,
          },
          {
            value: "content-manager",
            label: "Content Manager",
            content: selected ? (
              <ContentManager project={project} contentType={selected} />
            ) : (
              <EmptyState title="Select a content type" description="Pick one from the Content Type Builder tab first." />
            ),
          },
        ]}
      />
    </div>
  );
}

function ContentTypeBuilder({
  project,
  onSelect,
}: {
  project: Project;
  onSelect: (ct: ContentType) => void;
}) {
  const queryClient = useQueryClient();
  const contentTypesQuery = useQuery({
    queryKey: ["content-types", project.id],
    queryFn: () => api.listContentTypes(project.id),
  });

  const [apiId, setApiId] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [fields, setFields] = useState<SchemaField[]>([{ name: "title", type: "short_text", required: true }]);

  const createMutation = useMutation({
    mutationFn: () =>
      api.createContentType(project.id, {
        apiId,
        displayName,
        kind: "collection",
        schema: { fields },
      }),
    onSuccess: () => {
      setApiId("");
      setDisplayName("");
      setFields([{ name: "title", type: "short_text", required: true }]);
      queryClient.invalidateQueries({ queryKey: ["content-types", project.id] });
    },
  });

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 20, marginTop: 16 }}>
      <Card style={{ padding: 20, maxWidth: 640 }}>
        <h2 style={{ fontSize: 15, marginTop: 0 }}>New collection type</h2>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            createMutation.mutate();
          }}
          style={{ display: "flex", flexDirection: "column", gap: 12 }}
        >
          <TextField label="Display name" value={displayName} onValueChange={setDisplayName} required />
          <TextField label="API id" hint="camelCase, used in the URL" value={apiId} onValueChange={setApiId} required />

          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <strong style={{ fontSize: 13 }}>Fields</strong>
            {fields.map((f, i) => (
              <div key={i} style={{ display: "flex", gap: 8, alignItems: "flex-end" }}>
                <TextField
                  label="Name"
                  hideLabel
                  value={f.name}
                  onValueChange={(v) =>
                    setFields((prev) => prev.map((pf, idx) => (idx === i ? { ...pf, name: v } : pf)))
                  }
                />
                <Select
                  label="Type"
                  hideLabel
                  items={FIELD_TYPE_OPTIONS}
                  value={f.type}
                  onValueChange={(v) =>
                    setFields((prev) =>
                      prev.map((pf, idx) => (idx === i ? { ...pf, type: (v ?? "short_text") as FieldType } : pf)),
                    )
                  }
                />
                <Checkbox
                  label="Required"
                  checked={!!f.required}
                  onCheckedChange={(checked) =>
                    setFields((prev) => prev.map((pf, idx) => (idx === i ? { ...pf, required: !!checked } : pf)))
                  }
                />
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => setFields((prev) => prev.filter((_, idx) => idx !== i))}
                >
                  Remove
                </Button>
              </div>
            ))}
            <Button
              type="button"
              variant="secondary"
              onClick={() => setFields((prev) => [...prev, { name: "", type: "short_text" }])}
            >
              Add field
            </Button>
          </div>

          <Button type="submit" loading={createMutation.isPending}>
            Create content type
          </Button>
          {createMutation.isError && (
            <p role="alert" style={{ color: "crimson", fontSize: 13 }}>
              {(createMutation.error as Error).message}
            </p>
          )}
        </form>
      </Card>

      <div>
        <h2 style={{ fontSize: 15 }}>Existing content types</h2>
        <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
          {contentTypesQuery.data?.map((ct) => (
            <Button key={ct.id} type="button" variant="ghost" size="sm" onClick={() => onSelect(ct)}>
              <Badge>
                {ct.displayName} ({ct.apiId})
              </Badge>
            </Button>
          ))}
        </div>
      </div>
    </div>
  );
}

function ContentManager({ project, contentType }: { project: Project; contentType: ContentType }) {
  const queryClient = useQueryClient();
  const entriesQuery = useQuery({
    queryKey: ["entries", project.id, contentType.apiId],
    queryFn: () => api.listEntries(project.id, contentType.apiId),
  });

  const [draft, setDraft] = useState<Record<string, unknown>>({});
  const createMutation = useMutation({
    mutationFn: () => api.createEntry(project.id, contentType.apiId, draft),
    onSuccess: () => {
      setDraft({});
      queryClient.invalidateQueries({ queryKey: ["entries", project.id, contentType.apiId] });
    },
  });
  const publishMutation = useMutation({
    mutationFn: (documentId: string) => api.publishEntry(project.id, contentType.apiId, documentId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["entries", project.id, contentType.apiId] }),
  });

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 20, marginTop: 16 }}>
      <Card style={{ padding: 20, maxWidth: 640 }}>
        <h2 style={{ fontSize: 15, marginTop: 0 }}>New {contentType.displayName}</h2>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            createMutation.mutate();
          }}
          style={{ display: "flex", flexDirection: "column", gap: 12 }}
        >
          {contentType.schema.fields.map((f) => (
            <TextField
              key={f.name}
              label={f.name}
              required={f.required}
              value={(draft[f.name] as string) ?? ""}
              onValueChange={(v) => setDraft((prev) => ({ ...prev, [f.name]: v }))}
            />
          ))}
          <Button type="submit" loading={createMutation.isPending}>
            Save draft
          </Button>
        </form>
      </Card>

      {entriesQuery.data?.data.length === 0 ? (
        <EmptyState title="No entries yet" description="Create the first one above." />
      ) : (
        <DataTable
          caption={`${contentType.displayName} entries`}
          getRowId={(entry) => entry.id}
          rows={entriesQuery.data?.data ?? []}
          columns={[
            { key: "data", header: "Data", render: (entry) => JSON.stringify(entry.data) },
            { key: "status", header: "Status", render: (entry) => <Badge>{entry.status}</Badge> },
            {
              key: "actions",
              header: "",
              align: "end",
              render: (entry) => (
                <Button
                  size="sm"
                  variant="secondary"
                  loading={publishMutation.isPending}
                  onClick={() => publishMutation.mutate(entry.documentId)}
                >
                  Publish
                </Button>
              ),
            },
          ]}
        />
      )}
    </div>
  );
}

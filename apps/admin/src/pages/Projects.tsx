import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Card, EmptyState, InteractiveCard, PageHeader, TextField } from "@b4/react";

import { api, type Project } from "../api/client";

export function Projects({ onOpenProject }: { onOpenProject: (p: Project) => void }) {
  const queryClient = useQueryClient();
  const projectsQuery = useQuery({ queryKey: ["projects"], queryFn: api.listProjects });

  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const createMutation = useMutation({
    mutationFn: () => api.createProject(name, slug),
    onSuccess: () => {
      setName("");
      setSlug("");
      queryClient.invalidateQueries({ queryKey: ["projects"] });
    },
  });

  return (
    <div className="page-content">
      <PageHeader title="Projects" description="Every project you own or have been added to." />

      <Card style={{ padding: 20, maxWidth: 480 }}>
        <h2 style={{ fontSize: 15, marginTop: 0 }}>New project</h2>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            createMutation.mutate();
          }}
          style={{ display: "flex", flexDirection: "column", gap: 12 }}
        >
          <TextField label="Name" value={name} onValueChange={setName} required />
          <TextField
            label="Slug"
            hint="lowercase, hyphen-separated"
            value={slug}
            onValueChange={setSlug}
            required
          />
          <Button type="submit" loading={createMutation.isPending}>
            Create project
          </Button>
          {createMutation.isError && (
            <p role="alert" style={{ color: "var(--b4-danger)", fontSize: 13 }}>
              {(createMutation.error as Error).message}
            </p>
          )}
        </form>
      </Card>

      {projectsQuery.data?.length === 0 && (
        <EmptyState title="No projects yet" description="Create your first project above." />
      )}

      <div style={{ display: "grid", gap: 12, gridTemplateColumns: "repeat(auto-fill, minmax(240px, 1fr))" }}>
        {projectsQuery.data?.map((p) => (
          <InteractiveCard key={p.id} onClick={() => onOpenProject(p)}>
            <strong>{p.name}</strong>
            <div style={{ fontSize: 12, opacity: 0.7 }}>{p.slug}</div>
          </InteractiveCard>
        ))}
      </div>
    </div>
  );
}

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AppShell, Button } from "@b4/react";

import { api, type Project } from "./api/client";
import { Login } from "./pages/Login";
import { Projects } from "./pages/Projects";
import { ProjectDetail } from "./pages/ProjectDetail";

export function App() {
  const queryClient = useQueryClient();
  const meQuery = useQuery({ queryKey: ["me"], queryFn: api.me, retry: false });
  const [activeProject, setActiveProject] = useState<Project | null>(null);

  if (meQuery.isLoading) {
    return null;
  }

  if (meQuery.isError) {
    return <Login />;
  }

  return (
    <AppShell
      header={
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", width: "100%" }}>
          <strong>CMS</strong>
          <Button
            variant="ghost"
            size="sm"
            onClick={async () => {
              await api.logout();
              queryClient.setQueryData(["me"], undefined);
              queryClient.invalidateQueries({ queryKey: ["me"] });
            }}
          >
            Sign out ({meQuery.data?.email})
          </Button>
        </div>
      }
      sidebar={
        <nav style={{ padding: 16, display: "flex", flexDirection: "column", gap: 8 }}>
          <button
            onClick={() => setActiveProject(null)}
            style={{ textAlign: "left", background: "none", border: "none", cursor: "pointer" }}
          >
            Projects
          </button>
        </nav>
      }
    >
      {activeProject ? (
        <ProjectDetail project={activeProject} onBack={() => setActiveProject(null)} />
      ) : (
        <Projects onOpenProject={setActiveProject} />
      )}
    </AppShell>
  );
}

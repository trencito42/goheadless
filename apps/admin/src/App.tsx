import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AppShell, Button, Sidebar } from "@b4/react";

import { api, type Project } from "./api/client";
import { Login } from "./pages/Login";
import { Projects } from "./pages/Projects";
import { ProjectDetail } from "./pages/ProjectDetail";

const FolderIcon = (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} aria-hidden="true">
    <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z" strokeLinecap="round" strokeLinejoin="round" />
  </svg>
);

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
        <Sidebar
          activeItemId="projects"
          sections={[
            {
              id: "main",
              items: [
                { id: "projects", label: "Projects", icon: FolderIcon, onClick: () => setActiveProject(null) },
              ],
            },
          ]}
        />
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

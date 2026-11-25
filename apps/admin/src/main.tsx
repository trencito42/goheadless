import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import "@b4/tokens/css";
import "@b4/tokens/css/graphite";
import "@b4/tokens/css/porcelain";
import "@b4/react/styles.css";

import { App } from "./App";

document.documentElement.setAttribute("data-b4-theme", "graphite");

const queryClient = new QueryClient();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);

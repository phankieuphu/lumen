import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { BackendContext, loadBackend } from "./api";
import { App } from "./App";
import "./styles.css";

const backend = await loadBackend();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BackendContext value={backend}>
      <App />
    </BackendContext>
  </StrictMode>,
);

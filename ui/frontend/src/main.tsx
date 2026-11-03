import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { BackendContext, loadBackend } from "./api";
import { App } from "./App";
// Bundled rather than loaded from Google Fonts: the desktop app must work offline.
import "@fontsource/ibm-plex-sans/400.css";
import "@fontsource/ibm-plex-sans/500.css";
import "@fontsource/ibm-plex-sans/600.css";
import "@fontsource/ibm-plex-mono/400.css";
import "@fontsource/ibm-plex-mono/500.css";
import "./styles.css";

const backend = await loadBackend();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BackendContext value={backend}>
      <App />
    </BackendContext>
  </StrictMode>,
);

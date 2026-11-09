import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Gallery } from "./Gallery";
import "../../styles/index.css";

// Dev-only entry for the UI-kit gallery (served at /gallery.html by `vite dev`).
// NOT added to the production rollup input, so it never ships in the SPA bundle.
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Gallery />
  </StrictMode>,
);

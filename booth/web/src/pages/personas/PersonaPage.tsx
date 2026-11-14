import { Routes, Route, Navigate } from "react-router-dom";
import { PersonaOverview } from "./PersonaOverview";
import { PersonaSourcesPanel } from "./PersonaSourcesPanel";
import { PersonaBioPanel } from "./PersonaBioPanel";
import { PersonaTestPanel } from "./PersonaTestPanel";

// Persona-management surface (ISI-7362 D7, plan §6): the DevRel dashboard for
// returning members of the team — P1 overview at the index, P2 sources /
// re-ingest, P3 extra-context/bio, and P4 the standalone content test.
// Mounted at /personas/* by the app router; server-side §6.3 authz is
// authoritative, so every underlying call is gated regardless of this routing.
export function PersonaPage() {
  return (
    <Routes>
      <Route index element={<PersonaOverview />} />
      <Route path=":name/sources" element={<PersonaSourcesPanel />} />
      <Route path=":name/bio" element={<PersonaBioPanel />} />
      <Route path=":name/test" element={<PersonaTestPanel />} />
      <Route path="*" element={<Navigate to="/personas" replace />} />
    </Routes>
  );
}

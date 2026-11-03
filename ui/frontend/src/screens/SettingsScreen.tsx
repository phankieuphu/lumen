import { useState } from "react";

import { type AIProvider, type AISettings, type AITask, errorMessage, useBackend } from "../api";
import { Icon } from "../components/icons";
import { Banner, Segmented, SwitchRow } from "../components/ui";
import { useAsync } from "../hooks/useAsync";

const tasks: { id: AITask; label: string; help: string; large: boolean }[] = [
  { id: "inline", label: "Inline completion", help: "Grey text while you type", large: false },
  { id: "textToSql", label: "Text to SQL", help: "Write a query from a sentence", large: false },
  { id: "fixError", label: "Fix error", help: "Explain a database error and propose a fix", large: true },
  { id: "advise", label: "Slow-query advice", help: "Read the plan and suggest an index or a rewrite", large: true },
];

type PreviewTask = "advise" | "inline";
const previewTasks: { value: PreviewTask; label: string }[] = [
  { value: "advise", label: "Slow-query advice" },
  { value: "inline", label: "Inline completion" },
];

/** defaultModels picks a small model for fast tasks and a larger one for the rest. */
function defaultModels(p: AIProvider): AISettings["models"] {
  const small = p.models[0] ?? "";
  const large = p.models[1] ?? small;
  return Object.fromEntries(tasks.map((t) => [t.id, t.large ? large : small])) as AISettings["models"];
}

/** SettingsScreen chooses the AI provider, models, and what may be sent. */
export function SettingsScreen() {
  const backend = useBackend();
  const loaded = useAsync(() => Promise.all([backend.aiProviders(), backend.aiSettings()]), [backend]);

  if (loaded.error) {
    return (
      <section className="page" aria-label="AI settings">
        <Header />
        <Banner tone="warn">{loaded.error}</Banner>
      </section>
    );
  }
  if (!loaded.data) return null;
  const [providers, settings] = loaded.data;
  return <SettingsForm providers={providers} initial={settings} />;
}

const Header = () => (
  <header className="page-head">
    <div>
      <h1 className="title">AI settings</h1>
      <p className="muted">The AI only writes suggestions. It never runs a query.</p>
    </div>
  </header>
);

function SettingsForm({ providers, initial }: { providers: AIProvider[]; initial: AISettings }) {
  const backend = useBackend();
  const [settings, setSettings] = useState(initial);
  const [previewTask, setPreviewTask] = useState<PreviewTask>("advise");
  const [saveError, setSaveError] = useState<string>();
  const preview = useAsync(() => backend.aiPreview(previewTask, settings), [backend, previewTask, settings]);
  const provider = providers.find((p) => p.id === settings.provider) ?? providers[0]!;

  // Settings save as they change; the last write wins.
  const update = (next: AISettings) => {
    setSettings(next);
    setSaveError(undefined);
    backend.saveAISettings(next).catch((err: unknown) => setSaveError(errorMessage(err)));
  };

  const pickProvider = (p: AIProvider) => update({ ...settings, provider: p.id, models: defaultModels(p) });

  const p = preview.data;
  return (
    <section className="page" aria-label="AI settings">
      <Header />
      {saveError && <Banner tone="danger">{saveError}</Banner>}
      <div className="two-col top">
        <div className="stack">
          <section className="card" aria-label="Provider">
            <header className="card-head small plain">
              <h2>Provider</h2>
            </header>
            <div className="providers">
              {providers.map((pr) => (
                <button key={pr.id} type="button" className="provider" aria-pressed={pr.id === settings.provider} onClick={() => pickProvider(pr)}>
                  <span className="provider-head">
                    <strong>{pr.name}</strong>
                    <span className={pr.local ? "badge badge-ok" : "badge badge-info"}>{pr.local ? "local" : "cloud"}</span>
                  </span>
                  <span className="muted small-text">{pr.note}</span>
                  <span className={pr.ready ? "ok-text small-text" : "warn-text small-text"}>{pr.keyNote}</span>
                </button>
              ))}
            </div>
          </section>
          <section className="card" aria-label="Model for each task">
            <header className="card-head small plain column">
              <h2>Model for each task</h2>
              <p className="muted small-text">A small model keeps typing fast. A larger model reads query plans.</p>
            </header>
            <div className="switches flush">
              {tasks.map((t) => (
                <div key={t.id} className="switch-row">
                  <div className="switch-text">
                    <div className="switch-label">{t.label}</div>
                    <div className="muted small-text">{t.help}</div>
                  </div>
                  <select
                    className="mono model-select"
                    aria-label={`Model for ${t.label}`}
                    value={settings.models[t.id]}
                    onChange={(e) => update({ ...settings, models: { ...settings.models, [t.id]: e.target.value } })}
                  >
                    {provider.models.map((m) => (
                      <option key={m} value={m}>
                        {m}
                      </option>
                    ))}
                  </select>
                </div>
              ))}
            </div>
          </section>
          <section className="card" aria-label="Privacy">
            <header className="card-head small plain">
              <h2>Privacy</h2>
            </header>
            <div className="switches flush">
              <SwitchRow
                label="Include sample rows"
                help="Adds a few real rows from each table to the request. Off by default."
                checked={settings.sampleRows}
                onChange={(v) => update({ ...settings, sampleRows: v })}
              />
              <SwitchRow
                label="Hide literal values"
                help="Numbers and strings in your query are replaced with a question mark."
                checked={settings.maskLiterals}
                onChange={(v) => update({ ...settings, maskLiterals: v })}
              />
              <SwitchRow
                label="Inline completion"
                help="Send a request after a short pause in typing."
                checked={settings.inlineCompletion}
                onChange={(v) => update({ ...settings, inlineCompletion: v })}
              />
            </div>
          </section>
        </div>
        <section className="card preview" aria-label="What is sent">
          <header className="card-head small">
            <h2 className="with-icon">
              <span className="ai-text">
                <Icon name="sparkle" size={15} strokeWidth={2} />
              </span>
              What is sent
            </h2>
            <span className="spacer" />
            <Segmented label="Request to preview" value={previewTask} options={previewTasks} onChange={setPreviewTask} />
          </header>
          {preview.error && <Banner tone="danger">{preview.error}</Banner>}
          {p && (
            <>
              <div className="chip-row pad-x">
                <span className={p.local ? "pill ok" : "pill info"}>{p.destination}</span>
                <span className={settings.sampleRows ? "pill warn" : "pill ok"}>
                  {settings.sampleRows ? "Row data: sample rows included" : "Row data: not included"}
                </span>
                <span className={settings.maskLiterals ? "pill ok" : "pill warn"}>
                  {settings.maskLiterals ? "Literal values: hidden" : "Literal values: visible"}
                </span>
              </div>
              <pre className="code-block boxed preview-text">
                {p.lines.map((l, i) => (
                  <div key={i} className={l.tone ? `tone-${l.tone}` : undefined}>
                    {l.text || " "}
                  </div>
                ))}
              </pre>
            </>
          )}
          <p className="muted small-text card-note">This is the exact text of the request. Change a setting on the left and the preview updates.</p>
        </section>
      </div>
    </section>
  );
}

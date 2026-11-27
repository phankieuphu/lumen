import { type FormEvent, useState } from "react";

import { type AIInput, type AISettings, type AITask, errorMessage, type ProviderId, useBackend } from "../api";
import { Banner, SwitchRow } from "../components/ui";
import { useAsync } from "../hooks/useAsync";

const providers: Record<ProviderId, string> = {
  ollama: "Ollama",
  anthropic: "Anthropic",
  openai: "OpenAI-compatible",
};

const tasks: Record<AITask, string> = {
  inline: "Inline completion",
  textToSql: "Text to SQL",
  fixError: "Fix error",
};

export function SettingsScreen() {
  const backend = useBackend();
  const loaded = useAsync(() => backend.aiSettings(), [backend]);
  return (
    <section className="page" aria-label="AI settings">
      <header className="page-head"><h1 className="title">AI settings</h1></header>
      {loaded.error && <Banner tone="danger">{loaded.error}</Banner>}
      {loaded.data && <SettingsForm initial={loaded.data} />}
    </section>
  );
}

function SettingsForm({ initial }: { initial: AISettings }) {
  const backend = useBackend();
  const [settings, setSettings] = useState(initial);
  const [previewTask, setPreviewTask] = useState<AITask>("textToSql");
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState<{ error: boolean; text: string }>();
  const preview = useAsync(() => {
    const editor = previewTask === "inline" ? "SELECT * FROM orders WHERE " : "SELECT * FROM orders WHERE status = 'paid'";
    const input: AIInput = {
      connId: "", task: previewTask, editor, cursor: new TextEncoder().encode(editor).length,
      request: previewTask === "textToSql" ? "List paid orders" : undefined,
      error: previewTask === "fixError" ? "no such column: 'status'" : undefined,
    };
    return backend.aiPreview(input, settings);
  }, [backend, previewTask, settings.maskLiterals]);

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    try {
      await backend.saveAISettings(settings);
      if (key && settings.provider !== "ollama") await backend.setAIKey(settings.provider, key);
      setKey("");
      setStatus({ error: false, text: "AI settings saved." });
    } catch (err) {
      setStatus({ error: true, text: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  }

  async function removeKey() {
    setBusy(true);
    try {
      await backend.setAIKey(settings.provider, "");
      setKey("");
      setStatus({ error: false, text: "API key removed." });
    } catch (err) {
      setStatus({ error: true, text: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="two-col top">
      <form className="card" onSubmit={(event) => void save(event)}>
        <fieldset disabled={busy} className="ai-settings-fields stack pad-x">
          <label>Provider
            <select value={settings.provider} onChange={(e) => {
              setSettings({ ...settings, provider: e.target.value as ProviderId, baseUrl: "", models: {} });
              setKey("");
            }}>
              {Object.entries(providers).map(([id, name]) => <option key={id} value={id}>{name}</option>)}
            </select>
          </label>
          {settings.provider !== "anthropic" && <label>Server URL
            <input type="url" placeholder={settings.provider === "ollama" ? "http://localhost:11434" : "https://example.com/v1"}
              value={settings.baseUrl} onChange={(e) => setSettings({ ...settings, baseUrl: e.target.value })} />
          </label>}
          {settings.provider !== "ollama" && <>
            <label>API key
              <input type="password" autoComplete="off" placeholder="Enter a key to replace the saved key"
                value={key} onChange={(e) => setKey(e.target.value)} />
            </label>
            <button type="button" onClick={() => void removeKey()}>Remove saved key</button>
          </>}
          {(Object.entries(tasks) as [AITask, string][]).map(([task, label]) => (
            <label key={task}>{label} model
              <input value={settings.models[task] ?? ""}
                onChange={(e) => setSettings({ ...settings, models: { ...settings.models, [task]: e.target.value } })} />
            </label>
          ))}
          <SwitchRow label="Hide quoted literal values" help="Masks SQL string literals and quoted values in error messages."
            checked={settings.maskLiterals} onChange={(maskLiterals) => setSettings({ ...settings, maskLiterals })} />
          <SwitchRow label="Inline completion" help="Sends editor context after a pause in typing. Press Tab to accept a suggestion."
            checked={settings.inlineCompletion} onChange={(inlineCompletion) => setSettings({ ...settings, inlineCompletion })} />
          <button type="submit">Save AI settings</button>
        </fieldset>
        {status && (status.error ? <Banner tone="danger">{status.text}</Banner> : <p role="status" className="pad-x">{status.text}</p>)}
      </form>
      <section className="card preview" aria-label="What is sent">
        <header className="card-head">
          <h2>What is sent</h2>
          <select aria-label="Task to preview" value={previewTask} onChange={(e) => setPreviewTask(e.target.value as AITask)}>
            {Object.entries(tasks).map(([task, label]) => <option key={task} value={task}>{label}</option>)}
          </select>
        </header>
        <p className="pad-x muted">Example request using the selected privacy settings. Live requests include relevant schema metadata. Query result rows are excluded.</p>
        {preview.error && <Banner tone="danger">{preview.error}</Banner>}
        {preview.data && <pre className="code-block boxed preview-text">{preview.data}</pre>}
      </section>
    </div>
  );
}

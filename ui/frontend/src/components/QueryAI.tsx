import { type FormEvent, type RefObject, useEffect, useRef, useState } from "react";

import { type AIInput, errorMessage, useBackend } from "../api";
import { Banner } from "./ui";
import type { EditorHandle } from "./SqlEditor";

export const byteOffset = (text: string, cursor: number) => new TextEncoder().encode(text.slice(0, cursor)).length;

interface Props {
  connId: string;
  editorRef: RefObject<EditorHandle | null>;
  failure?: { sql: string; message: string; range?: { text: string; from: number; to: number } };
  onClose: () => void;
}

export function QueryAI({ connId, editorRef, failure, onClose }: Props) {
  const backend = useBackend();
  const pending = useRef<AbortController>(null);
  const [request, setRequest] = useState("");
  const [preview, setPreview] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [diff, setDiff] = useState<{ before: string; after: string; snapshot: string; from: number; to: number }>();
  useEffect(() => () => pending.current?.abort(), []);

  async function suggest(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending.current) return;
    const target = editorRef.current?.target();
    if (!target) return;
    const controller = new AbortController();
    const submit = (event.nativeEvent as SubmitEvent).submitter;
    const previewOnly = submit?.getAttribute("value") === "preview";
    const recorded = failure?.range;
    const canReplace = recorded && recorded.text === target.text &&
      target.text.slice(recorded.from, recorded.to) === failure?.sql;
    const from = failure ? (canReplace ? recorded.from : target.text.length) : target.selectionFrom ?? target.text.length;
    const before = failure ? failure.sql : target.selection ?? "";
    const to = failure ? (canReplace ? recorded.to : from) : from + before.length;
    const input: AIInput = {
      connId, task: failure ? "fixError" : "textToSql",
      editor: failure?.sql ?? target.text,
      cursor: failure ? 0 : byteOffset(target.text, target.cursor),
      error: failure?.message, request,
    };
    pending.current = controller;
    setBusy(true);
    setError(undefined);
    setDiff(undefined);
    setPreview(undefined);
    try {
      if (previewOnly) {
        const settings = await backend.aiSettings();
        const payload = await backend.aiPreview(input, settings);
        if (!controller.signal.aborted) setPreview(payload);
      } else {
        const sql = await backend.aiSuggest(input, controller.signal);
        if (!controller.signal.aborted) {
          const appending = from === target.text.length && to === from && target.text.trim();
          const separator = appending ? (/;\s*$/.test(target.text) ? "\n\n" : "\n;\n\n") : "";
          const after = separator + sql;
          setDiff({ before, after, snapshot: target.text, from, to });
        }
      }
    } catch (err) {
      if (!controller.signal.aborted) setError(errorMessage(err));
    } finally {
      if (pending.current === controller) {
        pending.current = null;
        setBusy(false);
      }
    }
  }

  return (
    <form className="stack ai-query-form" onSubmit={(event) => void suggest(event)} aria-label={failure ? "Fix query error" : "Text to SQL"}>
      <label>{failure ? "Guidance for the fix (optional)" : "Describe the query"}
        <input value={request} maxLength={2000} onChange={(event) => setRequest(event.target.value)} required={!failure} />
      </label>
      <div className="chip-row">
        <button type="submit" disabled={busy}>{failure ? "Suggest fix" : "Generate SQL"}</button>
        <button type="submit" value="preview" disabled={busy}>Preview request</button>
        <button type="button" onClick={onClose}>{busy ? "Cancel request" : "Close"}</button>
      </div>
      {busy && <p role="status">Preparing response…</p>}
      {error && <Banner tone="danger">{error}</Banner>}
      {preview && <pre className="code-block boxed preview-text">{preview}</pre>}
      {diff && <>
        <div className="two-col">
          <div><strong>Before</strong><pre className="code-block boxed">{diff.before || "(insertion)"}</pre></div>
          <div><strong>Suggested SQL</strong><pre className="code-block boxed">{diff.after}</pre></div>
        </div>
        <button type="button" onClick={() => {
          const editor = editorRef.current;
          if (!editor || editor.target().text !== diff.snapshot) {
            setError("The editor changed. Generate a new suggestion to apply it.");
            return;
          }
          editor.replace(diff.from, diff.to, diff.after);
          editor.focus();
          onClose();
        }}>Apply to editor</button>
      </>}
    </form>
  );
}

import { createRef, type Ref, type RefObject, useCallback, useEffect, useImperativeHandle, useRef, useState } from "react";

import { type Connection, useBackend } from "../api";
import { Icon } from "../components/icons";
import type { EditorHandle } from "../components/SqlEditor";
import { Confirm } from "../components/ui";
import { type FileState, MAC, QueryScreen } from "./QueryScreen";

export const NEW_TAB_KEYS = MAC ? "⌘N" : "Ctrl+N";

export const NEW_WINDOW_KEYS = MAC ? "⇧⌘N" : "Ctrl+Shift+N";

/** isNewTabKey reports whether e is Mod-N: ⌘N on macOS, Ctrl+N elsewhere. */
export function isNewTabKey(e: KeyboardEvent): boolean {
  return isModN(e) && !e.shiftKey;
}

/** isNewWindowKey reports whether e is Shift-Mod-N. */
export function isNewWindowKey(e: KeyboardEvent): boolean {
  return isModN(e) && e.shiftKey;
}

function isModN(e: KeyboardEvent): boolean {
  const mod = MAC ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey;
  return mod && !e.altKey && e.key.toLowerCase() === "n";
}

export interface QueryTabsHandle {
  /** newTab opens an empty tab, shows it, and focuses its editor. */
  newTab(): void;
  /** editor is the shown tab's editor. */
  editor(): EditorHandle | null;
}

interface Tab {
  id: number;
  editor: RefObject<EditorHandle | null>;
  file: FileState;
}

interface Props {
  conn: Connection;
  onRan: () => void;
  ref?: Ref<QueryTabsHandle>;
}

/**
 * QueryTabs holds several query editors on the current connection. Hidden
 * tabs stay mounted, so their text, results, and files survive switching.
 */
export function QueryTabs({ conn, onRan, ref }: Props) {
  const backend = useBackend();
  const blank = (id: number): Tab => ({ id, editor: createRef(), file: { dirty: false } });
  // The initializer stays pure (StrictMode runs it twice), so tab 1 is made
  // here and later ids come from the counter.
  const [tabs, setTabs] = useState<Tab[]>(() => [blank(1)]);
  const nextId = useRef(2);
  const [activeId, setActiveId] = useState(1);
  const [confirmingClose, setConfirmingClose] = useState<Tab>();
  // There is always at least one tab: the last one cannot be closed.
  const active = tabs.find((t) => t.id === activeId) ?? tabs[0]!;

  const show = useCallback((tab: Tab) => {
    setActiveId(tab.id);
    // The editor is hidden until this render commits.
    requestAnimationFrame(() => tab.editor.current?.focus());
  }, []);

  const newTab = () => {
    const tab = blank(nextId.current++);
    setTabs((ts) => [...ts, tab]);
    show(tab);
  };

  useImperativeHandle(ref, () => ({ newTab, editor: () => active.editor.current }));

  const close = (id: number) => {
    setConfirmingClose(undefined);
    const i = tabs.findIndex((t) => t.id === id);
    const rest = tabs.filter((t) => t.id !== id);
    if (i < 0 || rest.length === 0) return;
    setTabs(rest);
    // The neighbour to the right takes its place, or the one to the left.
    if (id === active.id) show(rest[Math.min(i, rest.length - 1)]!);
    // The focused close button is gone; keep focus in the shown editor.
    else active.editor.current?.focus();
  };
  const askClose = (tab: Tab) => (tab.file.dirty ? setConfirmingClose(tab) : close(tab.id));

  // Any tab's request may begin or end the connection's transaction, which
  // the shown tab re-reads.
  const [txVersion, setTxVersion] = useState(0);
  const ran = useCallback(() => {
    setTxVersion((v) => v + 1);
    onRan();
  }, [onRan]);
  // So can another window's, which this one hears of when it gains focus.
  useEffect(() => {
    const focused = () => setTxVersion((v) => v + 1);
    window.addEventListener("focus", focused);
    return () => window.removeEventListener("focus", focused);
  }, []);

  const setFile = useCallback((id: number, file: FileState) => {
    setTabs((ts) => ts.map((t) => (t.id === id ? { ...t, file } : t)));
  }, []);

  // The window asks before closing while any tab's file has unsaved changes.
  const unsaved = tabs.some((t) => t.file.name !== undefined && t.file.dirty);
  useEffect(() => {
    backend.setUnsaved(unsaved).catch(() => {});
  }, [backend, unsaved]);

  return (
    <>
      <div className="query-tabs" role="tablist" aria-label="Query tabs">
        {tabs.map((t) => (
          <div key={t.id} className="query-tab" aria-current={t.id === active.id ? "true" : undefined}>
            <button type="button" role="tab" aria-selected={t.id === active.id} className="query-tab-name" onClick={() => show(t)}>
              {t.file.name ?? `Query ${t.id}`}
              {t.file.dirty && t.file.name !== undefined && <span className="dirty-dot" aria-label="Unsaved changes" />}
            </button>
            {tabs.length > 1 && (
              <button type="button" className="query-tab-close" aria-label="Close tab" title="Close tab" onClick={() => askClose(t)}>
                <Icon name="close" size={12} strokeWidth={2} />
              </button>
            )}
          </div>
        ))}
        <button type="button" className="query-tab-new" aria-label="New tab" title={`New tab (${NEW_TAB_KEYS})`} onClick={newTab}>
          <Icon name="plus" size={14} strokeWidth={2} />
        </button>
      </div>
      {tabs.map((t) => (
        <div key={t.id} className="fill" hidden={t.id !== active.id}>
          <QueryScreen
            conn={conn}
            editorRef={t.editor}
            onRan={ran}
            active={t.id === active.id}
            txVersion={txVersion}
            onFile={(file) => setFile(t.id, file)}
          />
        </div>
      ))}
      {confirmingClose && (
        <Confirm
          title="Close without saving?"
          confirmLabel="Close tab"
          danger
          onConfirm={() => close(confirmingClose.id)}
          onCancel={() => setConfirmingClose(undefined)}
        >
          <p>
            {confirmingClose.file.name ? (
              <>
                Your changes to <strong>{confirmingClose.file.name}</strong> are not saved.
              </>
            ) : (
              "The SQL in this tab is not saved to a file."
            )}
          </p>
        </Confirm>
      )}
    </>
  );
}

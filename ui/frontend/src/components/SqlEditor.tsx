import { MySQL, PostgreSQL, SQLite, sql, type SQLDialect } from "@codemirror/lang-sql";
import { Compartment, EditorState, Prec } from "@codemirror/state";
import { EditorView, keymap } from "@codemirror/view";
import { basicSetup } from "codemirror";
import { type Ref, useEffect, useImperativeHandle, useRef } from "react";

import type { Dialect } from "../api";

const dialects: Record<Dialect, SQLDialect> = { postgres: PostgreSQL, mysql: MySQL, sqlite: SQLite };

export interface EditorHandle {
  /** sql returns the selection, or the whole text when nothing is selected. */
  sql(): string;
  setText(text: string): void;
  focus(): void;
}

interface Props {
  dialect: Dialect;
  onRun: (sql: string) => void;
  ref?: Ref<EditorHandle>;
}

function currentSQL(view: EditorView): string {
  const { from, to } = view.state.selection.main;
  return from === to ? view.state.doc.toString() : view.state.sliceDoc(from, to);
}

/**
 * SqlEditor wraps a CodeMirror 6 view. The text lives in CodeMirror, not in
 * React state, so typing never re-renders the screen. Mod-Enter runs.
 */
export function SqlEditor({ dialect, onRun, ref }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView>(null);
  const language = useRef(new Compartment());
  const runRef = useRef(onRun);
  runRef.current = onRun;

  useEffect(() => {
    const v = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        extensions: [
          Prec.highest(
            keymap.of([{ key: "Mod-Enter", run: (v) => (runRef.current(currentSQL(v)), true), preventDefault: true }]),
          ),
          basicSetup,
          language.current.of(sql({ dialect: dialects[dialect], upperCaseKeywords: true })),
          EditorView.theme({ "&": { height: "100%" }, ".cm-scroller": { fontFamily: "var(--mono)" } }),
        ],
      }),
    });
    view.current = v;
    return () => v.destroy();
    // The dialect is swapped below without recreating the editor.
  }, []);

  useEffect(() => {
    view.current?.dispatch({
      effects: language.current.reconfigure(sql({ dialect: dialects[dialect], upperCaseKeywords: true })),
    });
  }, [dialect]);

  useImperativeHandle(
    ref,
    () => ({
      sql: () => (view.current ? currentSQL(view.current) : ""),
      setText(text) {
        const v = view.current;
        v?.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: text }, selection: { anchor: text.length } });
      },
      focus: () => view.current?.focus(),
    }),
    [],
  );

  return <div className="editor" ref={host} />;
}

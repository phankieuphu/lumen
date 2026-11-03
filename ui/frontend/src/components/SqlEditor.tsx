import { MySQL, PostgreSQL, SQLite, sql, type SQLDialect } from "@codemirror/lang-sql";
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { Compartment, EditorState, Prec } from "@codemirror/state";
import { EditorView, keymap } from "@codemirror/view";
import { tags as t } from "@lezer/highlight";
import { basicSetup } from "codemirror";
import { type Ref, useEffect, useImperativeHandle, useRef } from "react";

import type { Dialect } from "../api";

const dialects: Record<Dialect, SQLDialect> = { postgres: PostgreSQL, mysql: MySQL, sqlite: SQLite };

// Colours come from the CSS tokens in styles.css, so the editor follows the app theme.
const theme = EditorView.theme(
  {
    "&": { height: "100%", color: "var(--fg)", backgroundColor: "var(--bg)" },
    ".cm-scroller": { fontFamily: "var(--mono)", lineHeight: "22px" },
    ".cm-content": { caretColor: "var(--fg)", padding: "10px 0" },
    ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--fg)" },
    ".cm-gutters": { backgroundColor: "var(--bg)", color: "var(--faint)", border: "none" },
    ".cm-lineNumbers .cm-gutterElement": { padding: "0 14px 0 16px", minWidth: "48px" },
    ".cm-activeLine": { backgroundColor: "var(--warn-wash)" },
    ".cm-activeLineGutter": { backgroundColor: "var(--warn-wash)", color: "var(--warn)" },
    "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection": { backgroundColor: "var(--select)" },
    ".cm-matchingBracket": { backgroundColor: "var(--active)", outline: "1px solid var(--line)" },
    ".cm-tooltip": { backgroundColor: "var(--raised)", border: "1px solid var(--line)", color: "var(--fg)" },
    ".cm-tooltip-autocomplete > ul > li[aria-selected]": { backgroundColor: "var(--active)", color: "var(--fg)" },
    ".cm-panels": { backgroundColor: "var(--panel)", color: "var(--fg)" },
    ".cm-foldPlaceholder": { backgroundColor: "var(--raised)", border: "1px solid var(--line)", color: "var(--muted)" },
  },
  { dark: true },
);

const highlight = HighlightStyle.define([
  { tag: [t.keyword, t.operatorKeyword, t.modifier], color: "var(--info)" },
  { tag: [t.function(t.variableName), t.standard(t.name), t.typeName], color: "var(--accent)" },
  { tag: [t.string, t.special(t.string)], color: "var(--warn)" },
  { tag: [t.number, t.bool, t.null], color: "var(--ok)" },
  { tag: [t.comment, t.lineComment, t.blockComment], color: "var(--faint)", fontStyle: "italic" },
  { tag: [t.special(t.name), t.labelName], color: "var(--ai)" },
]);

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
          theme,
          syntaxHighlighting(highlight),
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

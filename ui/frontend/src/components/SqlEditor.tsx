import { MySQL, PostgreSQL, SQLite, sql, type SQLDialect } from "@codemirror/lang-sql";
import { HighlightStyle, syntaxHighlighting, syntaxTree } from "@codemirror/language";
import { type Diagnostic, forceLinting, linter, lintGutter } from "@codemirror/lint";
import { Compartment, EditorState, Prec, type Range, StateEffect, StateField } from "@codemirror/state";
import { Decoration, type DecorationSet, EditorView, keymap, ViewPlugin, type ViewUpdate } from "@codemirror/view";
import { tags as t } from "@lezer/highlight";
import { basicSetup } from "codemirror";
import { type Ref, useEffect, useImperativeHandle, useRef } from "react";

import type { Dialect } from "../api";
import type { SchemaCompletion } from "../lib/completion";

import { inlineAI, type InlineRequest } from "./inlineAI";

const dialects: Record<Dialect, SQLDialect> = { postgres: PostgreSQL, mysql: MySQL, sqlite: SQLite };

/** language is the SQL support for a dialect, completing names from completion. */
const language = (dialect: Dialect, completion?: SchemaCompletion) =>
  sql({
    dialect: dialects[dialect],
    upperCaseKeywords: true,
    schema: completion?.namespace,
    defaultSchema: completion?.defaultSchema,
  });

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
    // Above the highlight colours: a dotted name keeps its part colour.
    ".cm-sql-qualifier, .cm-sql-qualifier span": { color: "var(--syn-qualifier)" },
    ".cm-sql-member, .cm-sql-member span": { color: "var(--syn-member)" },
    ".cm-ran": { backgroundColor: "var(--select)", transition: "background-color 0.3s" },
    ".cm-lintRange-error": {
      backgroundImage: "none",
      textDecoration: "underline wavy var(--danger)",
      textUnderlineOffset: "3px",
    },
    ".cm-lintRange-warning": {
      backgroundImage: "none",
      textDecoration: "underline wavy var(--warn)",
      textUnderlineOffset: "3px",
    },
    ".cm-lintRange-info": {
      backgroundImage: "none",
      textDecoration: "underline dotted var(--info)",
      textUnderlineOffset: "3px",
    },
    ".cm-diagnostic-error": { borderLeft: "3px solid var(--danger)", color: "var(--fg)" },
    ".cm-diagnostic-warning": { borderLeft: "3px solid var(--warn)", color: "var(--fg)" },
    ".cm-diagnostic-info": { borderLeft: "3px solid var(--info)", color: "var(--fg)" },
    ".cm-tooltip-lint": { padding: "2px 0" },
  },
  { dark: true },
);

// Colours are the --syn-* properties from lib/editorColors.ts.
const highlight = HighlightStyle.define([
  { tag: [t.keyword, t.operatorKeyword, t.modifier], color: "var(--syn-keyword)" },
  { tag: [t.function(t.variableName), t.standard(t.name), t.typeName], color: "var(--syn-function)" },
  { tag: [t.string, t.special(t.string)], color: "var(--syn-string)" },
  { tag: [t.number, t.bool, t.null], color: "var(--syn-number)" },
  { tag: [t.comment, t.lineComment, t.blockComment], color: "var(--syn-comment)", fontStyle: "italic" },
  { tag: [t.special(t.name), t.labelName], color: "var(--syn-variable)" },
  { tag: t.name, color: "var(--syn-name)" },
  { tag: [t.operator, t.punctuation, t.paren, t.brace, t.squareBracket], color: "var(--syn-operator)" },
]);

const qualifierMark = Decoration.mark({ class: "cm-sql-qualifier" });
const memberMark = Decoration.mark({ class: "cm-sql-member" });

/**
 * qualifiedNames marks the parts of dotted names: users in users.info is a
 * qualifier (schema, table, or alias), info a member. In a.b.c, a and b are
 * qualifiers. Only the visible part of the document is scanned.
 */
function qualifiedDecorations(view: EditorView): DecorationSet {
  const marks: Range<Decoration>[] = [];
  const doc = view.state.doc;
  for (const { from, to } of view.visibleRanges) {
    syntaxTree(view.state).iterate({
      from,
      to,
      enter: (n) => {
        if (n.name !== "Identifier" && n.name !== "QuotedIdentifier") return;
        if (n.to < doc.length && doc.sliceString(n.to, n.to + 1) === ".") marks.push(qualifierMark.range(n.from, n.to));
        else if (n.from > 0 && doc.sliceString(n.from - 1, n.from) === ".") marks.push(memberMark.range(n.from, n.to));
      },
    });
  }
  return Decoration.set(marks);
}

const qualifiedNames = ViewPlugin.fromClass(
  class {
    decorations: DecorationSet;
    constructor(view: EditorView) {
      this.decorations = qualifiedDecorations(view);
    }
    update(u: ViewUpdate) {
      if (u.docChanged || u.viewportChanged || syntaxTree(u.startState) !== syntaxTree(u.state)) {
        this.decorations = qualifiedDecorations(u.view);
      }
    }
  },
  { decorations: (v) => v.decorations },
);

/** EditorIssue is a range to mark: a syntax error, or a lint finding. */
export interface EditorIssue {
  from: number;
  to: number;
  severity: "error" | "warning" | "info";
  message: string;
}

/** What Run applies to: the selection, or the statement at the cursor. */
export interface RunTarget {
  /** The whole editor text. */
  text: string;
  /** The cursor offset in text. */
  cursor: number;
  /** The selected text, when there is a selection. */
  selection?: string;
  /** Where the selection starts in text. */
  selectionFrom?: number;
}

export interface EditorHandle {
  target(): RunTarget;
  /** flash briefly highlights [from, to), e.g. the statement that ran. */
  flash(from: number, to: number): void;
  setText(text: string): void;
  replace(from: number, to: number, text: string): void;
  focus(): void;
}

interface Props {
  dialect: Dialect;
  onRun: (target: RunTarget) => void;
  /** check returns the problems in the text, to underline. */
  check?: (sql: string) => Promise<EditorIssue[]>;
  /** The text the editor starts with. */
  initialText?: string;
  /** Called on Mod-S (as = false) and Shift-Mod-S (as = true). */
  onSave?: (as: boolean) => void;
  /** Called on Mod-O. */
  onOpen?: () => void;
  /** Called on Mod-E with the same target as Run. */
  onExplain?: (target: RunTarget) => void;
  /** Called after the text changes. */
  onChange?: () => void;
  /** Tables and columns to complete, besides keywords. */
  completion?: SchemaCompletion;
  inlineRequest?: InlineRequest;
  ref?: Ref<EditorHandle>;
}

function currentTarget(view: EditorView): RunTarget {
  const { from, to, head } = view.state.selection.main;
  const text = view.state.doc.toString();
  return from === to
    ? { text, cursor: head }
    : { text, cursor: head, selection: view.state.sliceDoc(from, to), selectionFrom: from };
}

// The flash highlight: set with a range, cleared with null.
const setFlash = StateEffect.define<{ from: number; to: number } | null>();
const flashField = StateField.define({
  create: () => Decoration.none,
  update(deco, tr) {
    deco = deco.map(tr.changes);
    for (const e of tr.effects) {
      if (e.is(setFlash)) deco = e.value ? Decoration.set([Decoration.mark({ class: "cm-ran" }).range(e.value.from, e.value.to)]) : Decoration.none;
    }
    return deco;
  },
  provide: (f) => EditorView.decorations.from(f),
});
const FLASH_MS = 700;
/** How long typing must pause before the text is checked. */
const CHECK_DELAY_MS = 400;

/**
 * SqlEditor wraps a CodeMirror 6 view. The text lives in CodeMirror, not in
 * React state, so typing never re-renders the screen. Mod-Enter runs.
 */
export function SqlEditor({ dialect, onRun, check, initialText = "", onSave, onOpen, onExplain, onChange, completion, inlineRequest, ref }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView>(null);
  const languageConf = useRef(new Compartment());
  const runRef = useRef(onRun);
  runRef.current = onRun;
  const checkRef = useRef(check);
  checkRef.current = check;
  const saveRef = useRef(onSave);
  saveRef.current = onSave;
  const openRef = useRef(onOpen);
  openRef.current = onOpen;
  const explainRef = useRef(onExplain);
  explainRef.current = onExplain;
  const inlineConf = useRef(new Compartment());
  const changeRef = useRef(onChange);
  changeRef.current = onChange;
  const flashTimer = useRef<ReturnType<typeof setTimeout>>(undefined);

  useEffect(() => {
    const v = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: initialText,
        extensions: [
          Prec.highest(
            keymap.of([
              { key: "Mod-Enter", run: (v) => (runRef.current(currentTarget(v)), true), preventDefault: true },
              { key: "Mod-s", run: () => (saveRef.current?.(false), !!saveRef.current), preventDefault: true },
              { key: "Shift-Mod-s", run: () => (saveRef.current?.(true), !!saveRef.current), preventDefault: true },
              { key: "Mod-o", run: () => (openRef.current?.(), !!openRef.current), preventDefault: true },
              {
                key: "Mod-e",
                run: (v) => (explainRef.current?.(currentTarget(v)), !!explainRef.current),
                preventDefault: true,
              },
            ]),
          ),
          basicSetup,
          languageConf.current.of(language(dialect, completion)),
          theme,
          syntaxHighlighting(highlight),
          qualifiedNames,
          flashField,
          inlineConf.current.of([]),
          // A check that fails (or no checker) just shows no marks.
          linter(
            async (v): Promise<Diagnostic[]> => {
              const text = v.state.doc.toString();
              if (!checkRef.current || !text.trim()) return [];
              const issues = await checkRef.current(text).catch(() => []);
              const len = v.state.doc.length;
              return issues.map((i) => ({
                from: Math.min(i.from, len),
                to: Math.min(i.to, len),
                severity: i.severity,
                message: i.message,
              }));
            },
            { delay: CHECK_DELAY_MS },
          ),
          lintGutter(),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) changeRef.current?.();
          }),
        ],
      }),
    });
    view.current = v;
    return () => {
      clearTimeout(flashTimer.current);
      v.destroy();
    };
    // The dialect and schema are swapped below without recreating the editor.
  }, []);

  // Swapped in place, without recreating the editor, when the dialect or the
  // schema changes.
  useEffect(() => {
    view.current?.dispatch({ effects: languageConf.current.reconfigure(language(dialect, completion)) });
  }, [dialect, completion]);

  useEffect(() => {
    // Another dialect parses differently, and another connection may have
    // other rules (production): check again now.
    if (view.current) forceLinting(view.current);
  }, [dialect, check]);

  useEffect(() => {
    view.current?.dispatch({ effects: inlineConf.current.reconfigure(inlineRequest ? inlineAI(inlineRequest) : []) });
  }, [inlineRequest]);

  useImperativeHandle(
    ref,
    () => ({
      target: () => (view.current ? currentTarget(view.current) : { text: "", cursor: 0 }),
      flash(from, to) {
        const v = view.current;
        if (!v || from >= to || to > v.state.doc.length) return;
        v.dispatch({ effects: setFlash.of({ from, to }) });
        clearTimeout(flashTimer.current);
        flashTimer.current = setTimeout(() => view.current?.dispatch({ effects: setFlash.of(null) }), FLASH_MS);
      },
      setText(text) {
        const v = view.current;
        v?.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: text }, selection: { anchor: text.length } });
      },
      replace(from, to, text) {
        view.current?.dispatch({ changes: { from, to, insert: text }, selection: { anchor: from + text.length } });
      },
      focus: () => view.current?.focus(),
    }),
    [],
  );

  return <div className="editor" ref={host} />;
}

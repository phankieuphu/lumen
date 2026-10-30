// CodeMirror look matching the app palette (see styles.css :root).
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { EditorView } from "@codemirror/view";
import { tags as t } from "@lezer/highlight";

const c = {
  bg: "#12151A",
  fg: "#E7EAF0",
  faint: "#8A93A2",
  line: "#2C333D",
  raised: "#272D37",
  accent: "#4FC9B4",
  keyword: "#7DB4FF",
  string: "#E9B65C",
  number: "#70CF8E",
};

const view = EditorView.theme(
  {
    "&": { height: "100%", backgroundColor: c.bg, color: c.fg },
    "&.cm-focused": { outline: "none" },
    ".cm-scroller": { fontFamily: "var(--mono)", fontSize: "13px", lineHeight: "22px" },
    ".cm-content": { caretColor: c.fg, padding: "12px 0" },
    ".cm-cursor, .cm-dropCursor": { borderLeftColor: c.fg, borderLeftWidth: "2px" },
    ".cm-gutters": { backgroundColor: c.bg, color: c.faint, border: "none" },
    ".cm-lineNumbers .cm-gutterElement": { minWidth: "40px", padding: "0 14px 0 0" },
    ".cm-activeLine": { backgroundColor: "rgba(255,255,255,0.025)" },
    ".cm-activeLineGutter": { backgroundColor: "transparent", color: c.fg },
    "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection": { backgroundColor: "#2B3A4F" },
    ".cm-matchingBracket": { backgroundColor: c.raised, outline: `1px solid ${c.line}` },
    ".cm-tooltip": { backgroundColor: "#1F242C", border: `1px solid ${c.line}`, color: c.fg },
    ".cm-tooltip-autocomplete ul li[aria-selected]": { backgroundColor: c.raised, color: c.fg },
    ".cm-panels": { backgroundColor: "#181C22", color: c.fg },
  },
  { dark: true },
);

const highlight = HighlightStyle.define([
  { tag: [t.keyword, t.operatorKeyword, t.modifier], color: c.keyword },
  { tag: [t.function(t.variableName), t.standard(t.name), t.typeName], color: c.accent },
  { tag: [t.string, t.special(t.string)], color: c.string },
  { tag: [t.number, t.bool, t.null], color: c.number },
  { tag: [t.comment, t.lineComment, t.blockComment], color: c.faint, fontStyle: "italic" },
  { tag: [t.punctuation, t.operator], color: "#C6CCD6" },
]);

export const editorTheme = [view, syntaxHighlighting(highlight)];

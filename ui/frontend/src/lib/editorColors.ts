// Syntax colours for the SQL editor. Each token is a CSS custom property
// (--syn-<key>) that the editor's highlight style reads, so a change
// recolours every editor at once without rebuilding it.

export interface SyntaxToken {
  key: string;
  label: string;
  /** What the token looks like, shown under the label. */
  example: string;
  fallback: string;
}

export const syntaxTokens: readonly SyntaxToken[] = [
  { key: "keyword", label: "Keywords", example: "SELECT, FROM, WHERE", fallback: "#7db4ff" },
  { key: "qualifier", label: "Name before a dot", example: "users in users.info: schema, table, or alias", fallback: "#f49ac2" },
  { key: "member", label: "Name after a dot", example: "info in users.info: table or column", fallback: "#ff9e7a" },
  { key: "name", label: "Other names", example: "tables, columns, aliases", fallback: "#e7eaf0" },
  { key: "function", label: "Functions and types", example: "count(), now(), int8", fallback: "#4fc9b4" },
  { key: "string", label: "Strings", example: "'text', \"quoted name\"", fallback: "#e9b65c" },
  { key: "number", label: "Numbers, booleans, NULL", example: "42, TRUE, NULL", fallback: "#70cf8e" },
  { key: "variable", label: "Parameters and variables", example: "?, @var, :name (MySQL and SQLite)", fallback: "#b9a1ff" },
  { key: "operator", label: "Operators and punctuation", example: "= + ( ) , ;", fallback: "#e7eaf0" },
  { key: "comment", label: "Comments", example: "-- note, /* note */", fallback: "#8a93a2" },
];

/** EditorColors are the colours the user changed: token key → #rrggbb. */
export type EditorColors = Record<string, string>;

const HEX = /^#[0-9a-f]{6}$/i;

/** cssVar is the custom property holding a token's colour. */
export const cssVar = (key: string) => `--syn-${key}`;

/**
 * withColor sets one token's colour, or drops it from the changed set when
 * it equals the default, so only real changes are stored.
 */
export function withColor(colors: EditorColors, key: string, color: string): EditorColors {
  const token = syntaxTokens.find((t) => t.key === key);
  const { [key]: _, ...rest } = colors;
  if (!token || !HEX.test(color) || color.toLowerCase() === token.fallback) return rest;
  return { ...rest, [key]: color.toLowerCase() };
}

/** colorOf is the colour a token shows: the user's, or the default. */
export function colorOf(colors: EditorColors, key: string): string {
  const c = colors[key];
  return c && HEX.test(c) ? c : (syntaxTokens.find((t) => t.key === key)?.fallback ?? "#e7eaf0");
}

/** applyEditorColors sets every token's custom property on the page root. */
export function applyEditorColors(colors: EditorColors, root: HTMLElement = document.documentElement) {
  for (const t of syntaxTokens) root.style.setProperty(cssVar(t.key), colorOf(colors, t.key));
}

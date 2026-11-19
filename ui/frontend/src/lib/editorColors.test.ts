import { describe, expect, it } from "vitest";

import { applyEditorColors, colorOf, cssVar, syntaxTokens, withColor } from "./editorColors";

describe("withColor", () => {
  it("stores a change in lower case", () => {
    expect(withColor({}, "keyword", "#FF0000")).toEqual({ keyword: "#ff0000" });
  });
  it("drops a colour set back to the default", () => {
    expect(withColor({ keyword: "#ff0000" }, "keyword", "#7DB4FF")).toEqual({});
  });
  it("ignores unknown tokens and bad colours", () => {
    expect(withColor({ name: "#111111" }, "nope", "#ff0000")).toEqual({ name: "#111111" });
    expect(withColor({}, "keyword", "red")).toEqual({});
  });
});

describe("colorOf", () => {
  it("falls back to the default for missing or bad values", () => {
    expect(colorOf({}, "keyword")).toBe("#7db4ff");
    expect(colorOf({ keyword: "url(x)" }, "keyword")).toBe("#7db4ff");
    expect(colorOf({ keyword: "#123456" }, "keyword")).toBe("#123456");
  });
});

describe("applyEditorColors", () => {
  it("sets a custom property for every token", () => {
    const props = new Map<string, string>();
    const root = { style: { setProperty: (k: string, v: string) => props.set(k, v) } } as unknown as HTMLElement;
    applyEditorColors({ qualifier: "#000000" }, root);
    expect(props.size).toBe(syntaxTokens.length);
    expect(props.get(cssVar("qualifier"))).toBe("#000000");
    expect(props.get(cssVar("member"))).toBe("#ff9e7a");
  });
});

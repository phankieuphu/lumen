import { useCallback, useEffect, useRef, useState } from "react";

import { errorMessage, useBackend } from "../api";
import { applyEditorColors, type EditorColors, withColor } from "../lib/editorColors";

/** How long colour changes settle before they are saved. */
const SAVE_DELAY_MS = 400;

export interface EditorColorsState {
  colors: EditorColors;
  setColor: (key: string, color: string) => void;
  /** reset restores one token's default, or every token's without a key. */
  reset: (key?: string) => void;
  error: string | undefined;
}

/**
 * useEditorColors loads the saved syntax colours, applies them to the page,
 * and saves changes after they settle: dragging a colour picker applies
 * every step at once but saves only the last. Mount it once, in App.
 */
export function useEditorColors(): EditorColorsState {
  const backend = useBackend();
  const [colors, setColors] = useState<EditorColors>({});
  const [error, setError] = useState<string>();
  // Set once the user changed something, so the loaded colours are not saved back.
  const dirty = useRef(false);

  useEffect(() => {
    let live = true;
    backend.editorColors().then(
      (c) => live && !dirty.current && setColors(c),
      (err: unknown) => live && setError(errorMessage(err)),
    );
    return () => {
      live = false;
    };
  }, [backend]);

  useEffect(() => applyEditorColors(colors), [colors]);

  useEffect(() => {
    if (!dirty.current) return;
    const id = setTimeout(() => {
      backend.saveEditorColors(colors).then(
        () => setError(undefined),
        (err: unknown) => setError(`Colours not saved: ${errorMessage(err)}`),
      );
    }, SAVE_DELAY_MS);
    return () => clearTimeout(id);
  }, [backend, colors]);

  const setColor = useCallback((key: string, color: string) => {
    dirty.current = true;
    setColors((c) => withColor(c, key, color));
  }, []);

  const reset = useCallback((key?: string) => {
    dirty.current = true;
    setColors((c) => {
      if (key === undefined) return {};
      const { [key]: _, ...rest } = c;
      return rest;
    });
  }, []);

  return { colors, setColor, reset, error };
}

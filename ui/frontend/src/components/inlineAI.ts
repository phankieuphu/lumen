import { Prec, StateEffect, StateField } from "@codemirror/state";
import { Decoration, EditorView, keymap, ViewPlugin, type ViewUpdate, WidgetType } from "@codemirror/view";

const setSuggestion = StateEffect.define<string>();
class GhostText extends WidgetType {
  constructor(readonly text: string) { super(); }
  eq(other: GhostText) { return this.text === other.text; }
  toDOM() {
    const span = document.createElement("span");
    span.className = "cm-ai-suggestion";
    span.setAttribute("aria-hidden", "true");
    span.textContent = this.text.replaceAll("\n", " ↵ ");
    return span;
  }
}

export type InlineRequest = (editor: string, cursor: number, signal: AbortSignal) => Promise<string>;

export function inlineAI(request: InlineRequest) {
  const text = StateField.define<{ pos: number; value: string }>({
    create: () => ({ pos: 0, value: "" }),
    update(previous, tr) {
      if (tr.docChanged || tr.selection) previous = { pos: 0, value: "" };
      for (const e of tr.effects) {
        if (e.is(setSuggestion)) previous = { pos: tr.state.selection.main.head, value: e.value };
      }
      return previous;
    },
    provide: (field) => EditorView.decorations.from(field, ({ pos, value }) => value
      ? Decoration.set([Decoration.widget({ widget: new GhostText(value), side: 1 }).range(pos)])
      : Decoration.none),
  });

  return [
    text,
    Prec.highest(keymap.of([
      {
        key: "Tab",
        run(view) {
          const { pos, value } = view.state.field(text);
          if (!value) return false;
          view.dispatch({ changes: { from: pos, insert: value }, selection: { anchor: pos + value.length } });
          return true;
        },
      },
      {
        key: "Escape",
        run(view) {
          if (!view.state.field(text).value) return false;
          view.dispatch({ effects: setSuggestion.of("") });
          return true;
        },
      },
    ])),
    ViewPlugin.fromClass(class {
      private timer?: ReturnType<typeof setTimeout>;
      private pending?: AbortController;
      private disposed = false;

      constructor(readonly view: EditorView) {}

      update(update: ViewUpdate) {
        if (!update.docChanged && !update.selectionSet && !update.focusChanged) return;
        this.cancel();
        if (!update.view.hasFocus) {
          if (update.state.field(text).value) {
            queueMicrotask(() => {
              if (!this.disposed && !this.view.hasFocus && this.view.state.field(text, false)?.value) {
                this.view.dispatch({ effects: setSuggestion.of("") });
              }
            });
          }
          return;
        }
        const { main, ranges } = update.state.selection;
        if (!update.transactions.some((tr) => tr.isUserEvent("input") || tr.isUserEvent("delete")) || !main.empty || ranges.length !== 1 || !update.state.doc.length) return;
        const doc = update.state.doc;
        this.timer = setTimeout(() => {
          const controller = new AbortController();
          this.pending = controller;
          void request(doc.toString(), main.head, controller.signal).then((value) => {
            if (!controller.signal.aborted && value) this.view.dispatch({ effects: setSuggestion.of(value) });
          }).catch(() => {});
        }, 450);
      }

      destroy() {
        this.disposed = true;
        this.cancel();
      }

      private cancel() {
        clearTimeout(this.timer);
        this.pending?.abort();
      }
    }),
  ];
}

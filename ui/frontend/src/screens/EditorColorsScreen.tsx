import type { Dialect } from "../api";
import { SqlEditor } from "../components/SqlEditor";
import { Banner } from "../components/ui";
import type { EditorColorsState } from "../hooks/useEditorColors";
import { colorOf, syntaxTokens } from "../lib/editorColors";

const SAMPLE = `-- Orders from the last week, with the customer's name
SELECT o.id, c.name, count(*) AS items, sum(i.price) AS total
FROM sales.orders o
JOIN public.customers c ON c.id = o.customer_id
JOIN sales.order_items i ON i.order_id = o.id
WHERE o.created_at > now() - interval '7 days'
  AND o.status = $1
  AND o.archived IS NOT TRUE
GROUP BY o.id, c.name
LIMIT 50;

SELECT * FROM users.info;
`;

interface Props extends EditorColorsState {
  /** The preview's dialect: the selected connection's. */
  dialect: Dialect;
}

/**
 * EditorColorsScreen sets the colour of each kind of SQL text. Changes show
 * at once, in the preview and in the query editor, and save themselves.
 */
export function EditorColorsScreen({ colors, setColor, reset, error, dialect }: Props) {
  const changed = Object.keys(colors).length > 0;
  return (
    <section className="page" aria-label="Editor colors">
      <header className="page-head">
        <div>
          <h1 className="title">Editor colors</h1>
          <p className="muted">Pick a colour for each kind of SQL text. Changes apply right away and are saved on this computer.</p>
        </div>
        <span className="spacer" />
        <button type="button" onClick={() => reset()} disabled={!changed}>
          Reset all
        </button>
      </header>
      {error && <Banner tone="danger">{error}</Banner>}
      <div className="colors-layout">
        <section className="card colors-list" aria-label="Colours">
          {syntaxTokens.map((t) => {
            const color = colorOf(colors, t.key);
            const id = `syn-${t.key}`;
            return (
              <div className="color-row" key={t.key}>
                <input
                  id={id}
                  type="color"
                  className="color-swatch"
                  value={color}
                  onChange={(e) => setColor(t.key, e.target.value)}
                />
                <label htmlFor={id} className="color-label">
                  <span style={{ color }}>{t.label}</span>
                  <span className="muted small-text">{t.example}</span>
                </label>
                <span className="mono muted small-text">{color}</span>
                <button
                  type="button"
                  className="small"
                  onClick={() => reset(t.key)}
                  disabled={!(t.key in colors)}
                  title={`Back to the default, ${t.fallback}`}
                >
                  Reset
                </button>
              </div>
            );
          })}
        </section>
        <section className="card colors-preview" aria-label="Preview">
          <header className="card-head small">
            <h2>Preview</h2>
            <span className="muted small-text">Type here to try your own SQL; it is not run.</span>
          </header>
          <SqlEditor dialect={dialect} onRun={() => {}} initialText={SAMPLE} />
        </section>
      </div>
    </section>
  );
}

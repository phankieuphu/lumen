import { type FormEvent, useState } from "react";

import { type Connection, type ConnectionInput, type Dialect, type Env, errorMessage, useBackend } from "../api";
import { Icon } from "../components/icons";
import { Banner, Confirm, Field, Segmented, SwitchRow } from "../components/ui";
import { dialectName } from "../lib/format";

const defaultPort: Record<Dialect, number> = { postgres: 5432, mysql: 3306, sqlite: 0 };

const envOptions: { value: Env; label: string }[] = [
  { value: "dev", label: "Dev" },
  { value: "staging", label: "Staging" },
  { value: "prod", label: "Production" },
];

const envNote: Record<Env, string> = {
  dev: "Development: no extra checks.",
  staging: "Staging: marked amber wherever the connection appears.",
  prod: "Production: marked red, and statements that write or change schema need confirmation before they run.",
};

const blank: ConnectionInput = {
  name: "",
  dialect: "postgres",
  host: "localhost",
  port: 5432,
  user: "",
  database: "",
  tls: "prefer",
  env: "dev",
  readOnly: false,
  maxConns: 4,
};

function toInput(c: Connection | undefined): ConnectionInput {
  if (!c) return blank;
  const { id: _i, createdAt: _c, updatedAt: _u, ...rest } = c;
  return rest;
}

interface Props {
  conn?: Connection;
  onSaved: (c: Connection) => void;
  onDeleted: () => void;
  onUse: (c: Connection) => void;
}

/**
 * ConnectionForm creates or edits a profile. When editing, an empty password
 * keeps the stored one; "Forget stored password" clears it.
 */
export function ConnectionForm({ conn, onSaved, onDeleted, onUse }: Props) {
  const backend = useBackend();
  const [form, setForm] = useState(() => toInput(conn));
  const [password, setPassword] = useState("");
  const [clearPassword, setClearPassword] = useState(false);
  const [busy, setBusy] = useState<"save" | "test" | "delete" | null>(null);
  const [error, setError] = useState<string>();
  const [tested, setTested] = useState(false);
  const [deleting, setDeleting] = useState(false);

  const set = <K extends keyof ConnectionInput>(key: K, value: ConnectionInput[K]) => {
    setForm((f) => ({ ...f, [key]: value }));
    setTested(false);
  };
  const setDialect = (d: Dialect) =>
    setForm((f) => ({ ...f, dialect: d, port: f.port === defaultPort[f.dialect] ? defaultPort[d] : f.port }));

  const input = (): ConnectionInput => {
    if (clearPassword) return { ...form, password: "" };
    // Creating: always send it. Editing: omit when blank to keep the stored one.
    return password || !conn ? { ...form, password } : form;
  };

  const act = async (kind: "save" | "test" | "delete") => {
    setBusy(kind);
    setError(undefined);
    try {
      if (kind === "test") {
        await backend.testConnection(input());
        setTested(true);
      } else if (kind === "delete") {
        await backend.deleteConnection(conn!.id);
        onDeleted();
      } else {
        onSaved(conn ? await backend.updateConnection(conn.id, input()) : await backend.createConnection(input()));
      }
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(null);
    }
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    void act("save");
  };

  const network = form.dialect !== "sqlite";
  return (
    <form className={`card form env-${form.env}`} onSubmit={submit} aria-label={conn ? `Edit ${conn.name}` : "New connection"}>
      <header className="card-head">
        <h2>{conn ? conn.name : "New connection"}</h2>
        <span className="chip">{dialectName(form.dialect)}</span>
        <span className="spacer" />
        {network && (
          <span className="muted small-text with-icon">
            <Icon name="lock" size={13} /> Passwords are kept in the OS keychain
          </span>
        )}
      </header>
      <div className="card-body">
        <div className="form-grid">
          <Field label="Name">
            <input value={form.name} onChange={(e) => set("name", e.target.value)} required maxLength={100} autoFocus={!conn} />
          </Field>
          <Field label="Engine">
            <select value={form.dialect} onChange={(e) => setDialect(e.target.value as Dialect)}>
              <option value="postgres">PostgreSQL</option>
              <option value="mysql">MySQL</option>
              <option value="sqlite">SQLite</option>
            </select>
          </Field>
          {network && (
            <>
              <Field label="Host">
                <input className="mono" value={form.host} onChange={(e) => set("host", e.target.value)} required />
              </Field>
              <Field label="Port">
                <input className="mono" type="number" min={0} max={65535} value={form.port} onChange={(e) => set("port", e.target.valueAsNumber || 0)} />
              </Field>
              <Field label="User">
                <input className="mono" value={form.user} onChange={(e) => set("user", e.target.value)} autoComplete="off" />
              </Field>
              <Field label="Password">
                <input
                  type="password"
                  value={password}
                  disabled={clearPassword}
                  placeholder={conn ? "Saved in keychain; leave blank to keep" : ""}
                  onChange={(e) => {
                    setPassword(e.target.value);
                    setTested(false);
                  }}
                  autoComplete="new-password"
                />
              </Field>
            </>
          )}
          <Field label={network ? "Database" : "Database file"} wide={!network}>
            <input className="mono" value={form.database} onChange={(e) => set("database", e.target.value)} required={!network} />
          </Field>
          {network && (
            <Field label="TLS">
              <select value={form.tls} onChange={(e) => set("tls", e.target.value as ConnectionInput["tls"])}>
                <option value="disable">disable</option>
                <option value="prefer">prefer</option>
                <option value="require">require</option>
                <option value="verify-full">verify-full</option>
              </select>
            </Field>
          )}
          <Field label="Max connections">
            <input className="mono" type="number" min={0} max={64} value={form.maxConns} onChange={(e) => set("maxConns", e.target.valueAsNumber || 0)} />
          </Field>
        </div>
        <div className="env-field">
          <span className="field-label">Environment</span>
          <div className="env-row">
            <Segmented label="Environment" value={form.env} options={envOptions} onChange={(v) => set("env", v)} />
            <p className={form.env === "prod" ? "env-note danger-text" : "env-note muted"}>{envNote[form.env]}</p>
          </div>
        </div>
      </div>
      <div className="switches">
        <SwitchRow
          label="Read-only"
          help="Blocks every statement that writes. Recommended for production."
          checked={form.readOnly}
          onChange={(v) => set("readOnly", v)}
        />
        {conn && network && (
          <SwitchRow
            label="Forget stored password"
            help="Removes the password from the keychain when you save."
            checked={clearPassword}
            onChange={setClearPassword}
          />
        )}
      </div>
      {error && <Banner tone="danger">{error}</Banner>}
      {tested && <Banner tone="ok">Connection works.</Banner>}
      <footer className="card-foot">
        <button type="button" onClick={() => void act("test")} disabled={busy !== null}>
          {busy === "test" ? "Testing…" : "Test connection"}
        </button>
        <button type="submit" className="primary" disabled={busy !== null}>
          {busy === "save" ? "Saving…" : "Save"}
        </button>
        {conn && (
          <button type="button" onClick={() => onUse(conn)}>
            Open in editor
          </button>
        )}
        <span className="spacer" />
        {conn && (
          <button type="button" className="danger" onClick={() => setDeleting(true)} disabled={busy !== null}>
            Delete
          </button>
        )}
      </footer>
      {deleting && conn && (
        <Confirm
          title="Delete connection"
          confirmLabel="Delete"
          danger
          onConfirm={() => {
            setDeleting(false);
            void act("delete");
          }}
          onCancel={() => setDeleting(false)}
        >
          Delete <strong>{conn.name}</strong> and its stored password? Its audit entries are kept.
        </Confirm>
      )}
    </form>
  );
}

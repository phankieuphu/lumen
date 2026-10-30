import { type FormEvent, useState } from "react";

import { type Connection, type ConnectionInput, type Dialect, type Env, errorMessage, type TLSMode, useBackend } from "../api";
import { Icon } from "../components/icons";
import { Confirm, ErrorText, Field, Segmented, SwitchRow } from "../components/ui";

const dialectLabel: Record<Dialect, string> = { postgres: "PostgreSQL", mysql: "MySQL", sqlite: "SQLite" };
const defaultPort: Record<Dialect, number> = { postgres: 5432, mysql: 3306, sqlite: 0 };

const dialects = (Object.keys(dialectLabel) as Dialect[]).map((d) => ({ value: d, label: dialectLabel[d] }));
const envs: { value: Env; label: string }[] = [
  { value: "dev", label: "dev" },
  { value: "staging", label: "staging" },
  { value: "prod", label: "prod" },
];
const envNote: Record<Env, { tone: Env; text: string }> = {
  dev: { tone: "dev", text: "Writes run without asking. Every write is still audited." },
  staging: { tone: "staging", text: "Writes run without asking. Every write is audited." },
  prod: { tone: "prod", text: "Every write and schema change asks for confirmation first, and is audited." },
};
const tlsModes: TLSMode[] = ["disable", "prefer", "require", "verify-full"];

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
  /** The profile to edit; undefined creates a new one. */
  conn?: Connection;
  onSaved: (c: Connection) => void;
  onDeleted: () => void;
  onOpen: (c: Connection) => void;
  onCancel: () => void;
}

/**
 * ConnectionForm creates or edits a profile. When editing, an empty password
 * keeps the stored one; "Remove stored password" clears it.
 */
export function ConnectionForm({ conn, onSaved, onDeleted, onOpen, onCancel }: Props) {
  const backend = useBackend();
  const [form, setForm] = useState(() => toInput(conn));
  const [password, setPassword] = useState("");
  const [clearPassword, setClearPassword] = useState(false);
  const [busy, setBusy] = useState<"save" | "test" | "delete" | null>(null);
  const [error, setError] = useState<string>();
  const [tested, setTested] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);

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
      } else if (kind === "delete" && conn) {
        await backend.deleteConnection(conn.id);
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
  const note = envNote[form.env];
  return (
    <form className="card" onSubmit={submit} aria-label={conn ? `Edit ${conn.name}` : "New connection"}>
      <div className="card-head">
        <h2>{conn ? conn.name : "New connection"}</h2>
        <span className="chip">{dialectLabel[form.dialect]}</span>
        <span className="spacer" />
        <span className="row muted small">
          <Icon name="key" size={14} /> Passwords are kept in the OS keychain
        </span>
      </div>

      <div className="form-section first">
        <span className="label">Engine</span>
        <Segmented label="Engine" options={dialects} value={form.dialect} onChange={setDialect} />
      </div>

      <div className="form-grid">
        <Field label="Name" wide>
          <input value={form.name} onChange={(e) => set("name", e.target.value)} required maxLength={100} autoFocus={!conn} />
        </Field>
        {network ? (
          <>
            <Field label="Host">
              <input value={form.host} onChange={(e) => set("host", e.target.value)} required />
            </Field>
            <Field label="Port">
              <input type="number" min={0} max={65535} value={form.port} onChange={(e) => set("port", e.target.valueAsNumber || 0)} />
            </Field>
            <Field label="Database">
              <input value={form.database} onChange={(e) => set("database", e.target.value)} />
            </Field>
            <Field label="User">
              <input value={form.user} onChange={(e) => set("user", e.target.value)} autoComplete="off" />
            </Field>
            <Field label="Password">
              <input
                type="password"
                value={password}
                disabled={clearPassword}
                placeholder={conn ? "Unchanged" : ""}
                onChange={(e) => {
                  setPassword(e.target.value);
                  setTested(false);
                }}
                autoComplete="new-password"
              />
            </Field>
            <Field label="TLS">
              <select value={form.tls} onChange={(e) => set("tls", e.target.value as TLSMode)}>
                {tlsModes.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
            </Field>
          </>
        ) : (
          <Field label="Database file" wide>
            <input value={form.database} onChange={(e) => set("database", e.target.value)} required />
          </Field>
        )}
        <Field label="Max connections">
          <input type="number" min={0} max={64} value={form.maxConns} onChange={(e) => set("maxConns", e.target.valueAsNumber || 0)} />
        </Field>
      </div>

      <div className="form-section">
        <span className="label">Environment</span>
        <div className="row">
          <Segmented label="Environment" options={envs} value={form.env} onChange={(v) => set("env", v)} />
          <span className={`env-note tone-${note.tone}`}>
            {note.text}
          </span>
        </div>
      </div>

      <div className="switches">
        <SwitchRow
          label="Read-only"
          help="Only reads run. Enforced by lumen and by the database session."
          checked={form.readOnly}
          onChange={(v) => set("readOnly", v)}
        />
        {conn && network && (
          <SwitchRow
            label="Remove stored password"
            help="Deletes the password from the keychain when you save."
            checked={clearPassword}
            onChange={setClearPassword}
          />
        )}
      </div>

      {(error || tested) && (
        <div className="output-message">
          <ErrorText error={error} />
          {tested && !error && (
            <p className="ok row">
              <Icon name="check" /> Connected.
            </p>
          )}
        </div>
      )}

      <div className="card-foot">
        <button type="button" onClick={() => void act("test")} disabled={busy !== null}>
          {busy === "test" ? "Testing…" : "Test connection"}
        </button>
        <button type="submit" className="primary" disabled={busy !== null}>
          {busy === "save" ? "Saving…" : "Save"}
        </button>
        {conn ? (
          <button type="button" onClick={() => onOpen(conn)}>
            Open in editor
          </button>
        ) : (
          <button type="button" onClick={onCancel}>
            Cancel
          </button>
        )}
        <span className="spacer" />
        {conn && (
          <button type="button" className="danger-outline" onClick={() => setConfirmDelete(true)} disabled={busy !== null}>
            Delete
          </button>
        )}
      </div>

      {confirmDelete && conn && (
        <Confirm
          title="Delete connection"
          confirmLabel="Delete"
          danger
          onConfirm={() => {
            setConfirmDelete(false);
            void act("delete");
          }}
          onCancel={() => setConfirmDelete(false)}
        >
          <p>
            Delete <strong>{conn.name}</strong> and its stored password? Its audit entries are kept.
          </p>
        </Confirm>
      )}
    </form>
  );
}

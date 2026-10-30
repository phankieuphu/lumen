import { type FormEvent, useState } from "react";

import { type Connection, type ConnectionInput, type Dialect, errorMessage, useBackend } from "../api";
import { ErrorText, Field } from "../components/ui";

const defaultPort: Record<Dialect, number> = { postgres: 5432, mysql: 3306, sqlite: 0 };

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
  onCancel: () => void;
}

/**
 * ConnectionForm creates or edits a profile. When editing, an empty password
 * keeps the stored one; "Remove stored password" clears it.
 */
export function ConnectionForm({ conn, onSaved, onCancel }: Props) {
  const backend = useBackend();
  const [form, setForm] = useState(() => toInput(conn));
  const [password, setPassword] = useState("");
  const [clearPassword, setClearPassword] = useState(false);
  const [busy, setBusy] = useState<"save" | "test" | null>(null);
  const [error, setError] = useState<string>();
  const [tested, setTested] = useState(false);

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

  const act = async (kind: "save" | "test") => {
    setBusy(kind);
    setError(undefined);
    try {
      if (kind === "test") {
        await backend.testConnection(input());
        setTested(true);
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
    <form className="panel form" onSubmit={submit}>
      <h2>{conn ? `Edit ${conn.name}` : "New connection"}</h2>
      <div className="form-grid">
        <Field label="Name" wide>
          <input value={form.name} onChange={(e) => set("name", e.target.value)} required maxLength={100} autoFocus />
        </Field>
        <Field label="Engine">
          <select value={form.dialect} onChange={(e) => setDialect(e.target.value as Dialect)}>
            <option value="postgres">PostgreSQL</option>
            <option value="mysql">MySQL</option>
            <option value="sqlite">SQLite</option>
          </select>
        </Field>
        <Field label="Environment">
          <select value={form.env} onChange={(e) => set("env", e.target.value as ConnectionInput["env"])}>
            <option value="dev">dev</option>
            <option value="staging">staging</option>
            <option value="prod">prod</option>
          </select>
        </Field>
        {network && (
          <>
            <Field label="Host">
              <input value={form.host} onChange={(e) => set("host", e.target.value)} required />
            </Field>
            <Field label="Port">
              <input type="number" min={0} max={65535} value={form.port} onChange={(e) => set("port", e.target.valueAsNumber || 0)} />
            </Field>
            <Field label="User">
              <input value={form.user} onChange={(e) => set("user", e.target.value)} autoComplete="off" />
            </Field>
            <Field label="Password">
              <input
                type="password"
                value={password}
                disabled={clearPassword}
                placeholder={conn ? "unchanged" : ""}
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
          <input value={form.database} onChange={(e) => set("database", e.target.value)} required={!network} />
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
          <input type="number" min={0} max={64} value={form.maxConns} onChange={(e) => set("maxConns", e.target.valueAsNumber || 0)} />
        </Field>
      </div>
      <label className="check">
        <input type="checkbox" checked={form.readOnly} onChange={(e) => set("readOnly", e.target.checked)} /> Read-only
      </label>
      {conn && network && (
        <label className="check">
          <input type="checkbox" checked={clearPassword} onChange={(e) => setClearPassword(e.target.checked)} /> Remove stored password
        </label>
      )}
      <ErrorText error={error} />
      {tested && <p className="ok">Connection works.</p>}
      <div className="actions">
        <button type="button" onClick={() => void act("test")} disabled={busy !== null}>
          {busy === "test" ? "Testing…" : "Test"}
        </button>
        <span className="spacer" />
        <button type="button" onClick={onCancel}>
          Cancel
        </button>
        <button type="submit" className="primary" disabled={busy !== null}>
          {busy === "save" ? "Saving…" : "Save"}
        </button>
      </div>
    </form>
  );
}
